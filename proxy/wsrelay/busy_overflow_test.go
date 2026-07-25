package wsrelay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/proxy"
	"github.com/gorilla/websocket"
)

// setBusyRuntimeSettings 临时修改 busy 策略相关运行时设置，测试结束自动还原。
func setBusyRuntimeSettings(t *testing.T, mutate func(*proxy.RuntimeSettings)) {
	t.Helper()
	prev := proxy.CurrentRuntimeSettings()
	next := prev
	mutate(&next)
	proxy.ApplyRuntimeSettings(next)
	t.Cleanup(func() { proxy.ApplyRuntimeSettings(prev) })
}

// newBusyTestConnection 构造一条"已连接且被在途请求占用"的 fake 连接并入池。
func newBusyTestConnection(t *testing.T, manager *Manager, account *auth.Account, wsURL, sessionKey string) (*WsConnection, *PendingRequest) {
	t.Helper()
	key := manager.poolKey(account.ID(), wsURL, sessionKey, "")
	session := NewSession(account.ID(), manager)
	session.SetConnected(true)
	blocking := session.AddPendingRequest(sessionKey)
	conn := &WsConnection{
		session:  session,
		URL:      wsURL,
		PoolKey:  key,
		httpResp: &http.Response{StatusCode: http.StatusSwitchingProtocols},
	}
	conn.SetState(StateConnected)
	conn.Touch()
	manager.connections.Store(key, conn)
	manager.sessions.Store(key, session)
	return conn, blocking
}

func TestBusyAcquireMaxWaitConfigurable(t *testing.T) {
	setBusyRuntimeSettings(t, func(s *proxy.RuntimeSettings) { s.CodexWSBusyMaxWaitSec = 1 })

	manager := NewManager()
	t.Cleanup(manager.Stop)
	manager.probeFunc = func(wc *WsConnection) bool { return true }

	account := &auth.Account{DBID: 42}
	wsURL := "wss://example.test/responses"
	busy, blocking := newBusyTestConnection(t, manager, account, wsURL, "session-1")
	t.Cleanup(func() { busy.session.RemovePendingRequest(blocking.RequestID) })

	start := time.Now()
	_, _, err := manager.AcquireConnection(context.Background(), account, wsURL, "session-1", http.Header{}, "")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected busy acquire to time out")
	}
	if !strings.Contains(err.Error(), "waiting for busy session") {
		t.Fatalf("err = %v, want busy session timeout", err)
	}
	if elapsed < 900*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("elapsed = %s, want ~1s configured max wait", elapsed)
	}
}

func TestBusyOverflowReusesIdleSiblingSlot(t *testing.T) {
	setBusyRuntimeSettings(t, func(s *proxy.RuntimeSettings) {
		s.CodexWSBusyOverflow = true
		s.CodexWSBusyPatienceSec = 0
	})

	manager := NewManager()
	t.Cleanup(manager.Stop)
	manager.probeFunc = func(wc *WsConnection) bool { return true }

	account := &auth.Account{DBID: 42, DynamicConcurrencyLimit: 4}
	wsURL := "wss://example.test/responses"
	busy, blocking := newBusyTestConnection(t, manager, account, wsURL, "session-1")
	t.Cleanup(func() { busy.session.RemovePendingRequest(blocking.RequestID) })

	// 预置一条空闲的 overflow 兄弟连接
	siblingKey := manager.poolKey(account.ID(), wsURL, "session-1#ovf-1", "")
	siblingSession := NewSession(account.ID(), manager)
	siblingSession.SetConnected(true)
	sibling := &WsConnection{
		session:  siblingSession,
		URL:      wsURL,
		PoolKey:  siblingKey,
		httpResp: &http.Response{StatusCode: http.StatusSwitchingProtocols},
	}
	sibling.SetState(StateConnected)
	sibling.Touch()
	manager.connections.Store(siblingKey, sibling)
	manager.sessions.Store(siblingKey, siblingSession)

	got, pr, err := manager.AcquireConnection(context.Background(), account, wsURL, "session-1", http.Header{}, "")
	if err != nil {
		t.Fatalf("AcquireConnection() error = %v", err)
	}
	if got != sibling {
		t.Fatal("expected idle overflow sibling connection to be reused")
	}
	if pr == nil {
		t.Fatal("expected pending request reservation on sibling")
	}
	siblingSession.RemovePendingRequest(pr.RequestID)
}

func TestBusyOverflowCreatesSiblingConnection(t *testing.T) {
	setBusyRuntimeSettings(t, func(s *proxy.RuntimeSettings) {
		s.CodexWSBusyOverflow = true
		s.CodexWSBusyPatienceSec = 0
	})

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	manager := NewManager()
	t.Cleanup(manager.Stop)
	account := &auth.Account{DBID: 42, DynamicConcurrencyLimit: 4}
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	busy, blocking := newBusyTestConnection(t, manager, account, wsURL, "session-1")
	t.Cleanup(func() { busy.session.RemovePendingRequest(blocking.RequestID) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, pr, err := manager.AcquireConnection(ctx, account, wsURL, "session-1", http.Header{}, "")
	if err != nil {
		t.Fatalf("AcquireConnection() error = %v", err)
	}
	if got == busy {
		t.Fatal("expected a new overflow sibling connection, got the busy one")
	}
	if pr == nil {
		t.Fatal("expected pending request reservation")
	}
	if !strings.Contains(got.session.ID, busyOverflowKeyInfix) {
		t.Fatalf("sibling session ID = %q, want overflow slot key", got.session.ID)
	}
	got.session.RemovePendingRequest(pr.RequestID)
	manager.DiscardConnection(got)
}

func TestBusyOverflowDisabledKeepsWaiting(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)
	manager.probeFunc = func(wc *WsConnection) bool { return true }

	account := &auth.Account{DBID: 42, DynamicConcurrencyLimit: 4}
	wsURL := "wss://example.test/responses"
	busy, blocking := newBusyTestConnection(t, manager, account, wsURL, "session-1")
	t.Cleanup(func() { busy.session.RemovePendingRequest(blocking.RequestID) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, err := manager.AcquireConnection(ctx, account, wsURL, "session-1", http.Header{}, "")
	if err == nil {
		t.Fatal("expected acquire to keep waiting (ctx timeout) when overflow is disabled")
	}
	// 未开启 overflow 时不应出现兄弟槽位连接
	overflowKey := manager.poolKey(account.ID(), wsURL, "session-1#ovf-1", "")
	if _, ok := manager.connections.Load(overflowKey); ok {
		t.Fatal("overflow sibling must not be created when disabled")
	}
}

func TestBusyOverflowKeyNotRecursive(t *testing.T) {
	if !isBusyOverflowSessionKey("session-1#ovf-1") {
		t.Fatal("overflow slot key should be recognized")
	}
	if isBusyOverflowSessionKey("session-1") {
		t.Fatal("plain session key must not be treated as overflow slot")
	}
}
