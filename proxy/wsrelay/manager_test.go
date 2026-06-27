package wsrelay

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/codex2api/auth"
)

func TestManagerStopIdempotent(t *testing.T) {
	manager := NewManager()

	for i := 0; i < 3; i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Stop call %d panicked: %v", i+1, r)
				}
			}()
			manager.Stop()
		}()
	}
}

func TestManagerStopConcurrent(t *testing.T) {
	manager := NewManager()

	const callers = 32
	start := make(chan struct{})
	panicCh := make(chan any, callers)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(callers)

	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCh <- r
				}
			}()
			<-start
			manager.Stop()
		}()
	}

	close(start)
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent Stop calls timed out")
	}
	close(panicCh)

	for r := range panicCh {
		t.Fatalf("concurrent Stop panicked: %v", r)
	}
}

func TestRemoveConnectionUsesEffectiveProxyKey(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)

	account := &auth.Account{DBID: 42, ProxyURL: " http://proxy-a.example:8080 "}
	wsURL := "wss://example.test/responses"
	sessionKey := "session-1"
	proxyURL := effectiveProxyURL(account, "")
	key := manager.poolKey(account.ID(), wsURL, sessionKey, proxyURL)

	session := NewSession(account.ID(), manager)
	session.SetConnected(true)
	conn := &WsConnection{session: session, URL: wsURL, PoolKey: key}
	conn.SetState(StateConnected)
	conn.Touch()
	manager.connections.Store(key, conn)
	manager.sessions.Store(key, session)

	manager.RemoveConnection(account.ID(), wsURL, sessionKey, effectiveProxyURL(account, ""))

	if _, ok := manager.connections.Load(key); ok {
		t.Fatal("expected connection stored under effective proxy key to be removed")
	}
	if _, ok := manager.sessions.Load(key); ok {
		t.Fatal("expected session stored under effective proxy key to be removed")
	}
	if conn.IsConnected() {
		t.Fatal("expected removed connection to be closed")
	}
}

func TestAcquireConnectionReusesIdleConnectedConnection(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)
	// 测试环境无真实 WebSocket 连接，注入探活函数跳过 Ping
	manager.probeFunc = func(wc *WsConnection) bool { return true }

	account := &auth.Account{DBID: 42}
	wsURL := "wss://example.test/responses"
	key := manager.poolKey(account.ID(), wsURL, "session-1", "")

	session := NewSession(account.ID(), manager)
	session.SetConnected(true)
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

	got, pr, err := manager.AcquireConnection(context.Background(), account, wsURL, "session-1", http.Header{}, "")
	if err != nil {
		t.Fatalf("AcquireConnection() error = %v", err)
	}
	if got != conn {
		t.Fatal("expected existing connection to be reused")
	}
	if pr == nil {
		t.Fatal("expected pending request reservation")
	}
	if session.PendingCount() != 1 {
		t.Fatalf("PendingCount = %d, want %d", session.PendingCount(), 1)
	}
	session.RemovePendingRequest(pr.RequestID)
}

func TestPoolKeyIncludesSessionKey(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)

	keyA := manager.poolKey(42, "wss://example.test/responses", "session-a", "")
	keyB := manager.poolKey(42, "wss://example.test/responses", "session-b", "")
	if keyA == keyB {
		t.Fatal("expected different session keys to produce different pool keys")
	}
}

func TestPoolKeyKeepsSameSessionStable(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)

	keyA := manager.poolKey(42, "wss://example.test/responses", "session-a", "http://proxy-a")
	keyB := manager.poolKey(42, "wss://example.test/responses", "session-a", "http://proxy-a")
	if keyA != keyB {
		t.Fatal("expected identical session keys to produce the same pool key")
	}
}

func TestPoolKeyIncludesProxyScope(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)

	keyA := manager.poolKey(42, "wss://example.test/responses", "session-a", "http://proxy-a")
	keyB := manager.poolKey(42, "wss://example.test/responses", "session-a", "http://proxy-b")
	if keyA == keyB {
		t.Fatal("expected different proxies to produce different pool keys")
	}
}

func TestCanReuseConnection(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)

	t.Run("idle connected session can be reused", func(t *testing.T) {
		session := NewSession(42, manager)
		session.SetConnected(true)
		conn := &WsConnection{session: session}
		conn.SetState(StateConnected)
		conn.Touch()

		if !canReuseConnection(conn) {
			t.Fatal("expected connection to be reusable")
		}
	})

	t.Run("pending request blocks reuse", func(t *testing.T) {
		session := NewSession(42, manager)
		session.SetConnected(true)
		pending := session.AddPendingRequest("session-a")
		t.Cleanup(func() { session.RemovePendingRequest(pending.RequestID) })

		conn := &WsConnection{session: session}
		conn.SetState(StateConnected)
		conn.Touch()

		if canReuseConnection(conn) {
			t.Fatal("expected connection with pending request to be non-reusable")
		}
	})

	t.Run("expired connection cannot be reused", func(t *testing.T) {
		session := NewSession(42, manager)
		session.SetConnected(true)
		conn := &WsConnection{session: session}
		conn.SetState(StateConnected)
		conn.lastUsed.Store(time.Now().Add(-IdleTimeout - time.Second).UnixNano())

		if canReuseConnection(conn) {
			t.Fatal("expected expired connection to be non-reusable")
		}
	})
}

func TestAcquireConnectionWaitsWhileSessionHasPendingRequest(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)
	manager.probeFunc = func(wc *WsConnection) bool { return true }

	account := &auth.Account{DBID: 42}
	wsURL := "wss://example.test/responses"
	key := manager.poolKey(account.ID(), wsURL, "session-1", "")

	session := NewSession(account.ID(), manager)
	session.SetConnected(true)
	blocking := session.AddPendingRequest("session-1")
	t.Cleanup(func() { session.RemovePendingRequest(blocking.RequestID) })

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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, _, err := manager.AcquireConnection(ctx, account, wsURL, "session-1", http.Header{}, "")
	if err == nil {
		t.Fatal("expected acquire to stop when session stays busy until context timeout")
	}
}

func newTestSlotConnection(manager *Manager, account *auth.Account, wsURL, slotSession string) (*WsConnection, *Session) {
	key := manager.poolKey(account.ID(), wsURL, slotSession, "")
	session := NewSession(account.ID(), manager)
	session.SetConnected(true)
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
	return conn, session
}

func TestAcquireReusableConnectionReusesIdleSlot(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)
	manager.probeFunc = func(wc *WsConnection) bool { return true }

	account := &auth.Account{DBID: 42}
	wsURL := "wss://example.test/responses"
	conn, _ := newTestSlotConnection(manager, account, wsURL, "cache-key#0")

	got, pr, usedKey, err := manager.AcquireReusableConnection(context.Background(), account, wsURL, "cache-key", "stateless-xyz", 4, http.Header{}, "")
	if err != nil {
		t.Fatalf("AcquireReusableConnection() error = %v", err)
	}
	if got != conn {
		t.Fatal("expected idle slot connection to be reused")
	}
	if usedKey != "cache-key#0" {
		t.Fatalf("usedKey = %q, want cache-key#0", usedKey)
	}
	got.session.RemovePendingRequest(pr.RequestID)
}

func TestAcquireReusableConnectionSkipsBusySlot(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)
	manager.probeFunc = func(wc *WsConnection) bool { return true }

	account := &auth.Account{DBID: 42}
	wsURL := "wss://example.test/responses"
	_, busySession := newTestSlotConnection(manager, account, wsURL, "cache-key#0")
	busySession.AddPendingRequest("cache-key#0") // 占用 slot 0
	idleConn, _ := newTestSlotConnection(manager, account, wsURL, "cache-key#1")

	got, pr, usedKey, err := manager.AcquireReusableConnection(context.Background(), account, wsURL, "cache-key", "stateless-xyz", 4, http.Header{}, "")
	if err != nil {
		t.Fatalf("AcquireReusableConnection() error = %v", err)
	}
	if got != idleConn {
		t.Fatal("expected busy slot 0 to be skipped and idle slot 1 reused")
	}
	if usedKey != "cache-key#1" {
		t.Fatalf("usedKey = %q, want cache-key#1", usedKey)
	}
	got.session.RemovePendingRequest(pr.RequestID)
}
