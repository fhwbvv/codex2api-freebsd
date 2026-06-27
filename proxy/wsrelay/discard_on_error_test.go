package wsrelay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestDiscardConnectionClosesAndRemovesFromPool 验证 DiscardConnection 关闭底层连接
// 并把连接/会话从池中移除。
func TestDiscardConnectionClosesAndRemovesFromPool(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)

	key := manager.poolKey(1, "wss://example.test/responses", "session-1", "")
	session := NewSession(1, manager)
	session.SetConnected(true)
	wc := &WsConnection{session: session, URL: "wss://example.test/responses", PoolKey: key}
	wc.SetState(StateConnected)
	wc.Touch()
	manager.connections.Store(key, wc)
	manager.sessions.Store(key, session)

	manager.DiscardConnection(wc)

	if _, ok := manager.connections.Load(key); ok {
		t.Fatal("expected discarded connection to be removed from pool")
	}
	if _, ok := manager.sessions.Load(key); ok {
		t.Fatal("expected discarded session to be removed from pool")
	}
	if wc.IsConnected() {
		t.Fatal("expected discarded connection to be closed (state != connected)")
	}
}

// TestDiscardConnectionKeepsReplacedConnection 验证 DiscardConnection 使用
// CompareAndDelete 精确删除：当池中同 PoolKey 已被替换为新连接时，丢弃旧连接不得误删新连接。
func TestDiscardConnectionKeepsReplacedConnection(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)

	key := manager.poolKey(1, "wss://example.test/responses", "session-1", "")

	oldSession := NewSession(1, manager)
	oldSession.SetConnected(true)
	oldConn := &WsConnection{session: oldSession, PoolKey: key}
	oldConn.SetState(StateConnected)

	// 池中已被替换为同 PoolKey 的新连接
	newSession := NewSession(1, manager)
	newSession.SetConnected(true)
	newConn := &WsConnection{session: newSession, PoolKey: key}
	newConn.SetState(StateConnected)
	newConn.Touch()
	manager.connections.Store(key, newConn)
	manager.sessions.Store(key, newSession)

	manager.DiscardConnection(oldConn)

	if v, ok := manager.connections.Load(key); !ok || v.(*WsConnection) != newConn {
		t.Fatal("DiscardConnection must not remove a replaced connection under the same key")
	}
	if v, ok := manager.sessions.Load(key); !ok || v.(*Session) != newSession {
		t.Fatal("DiscardConnection must not remove the replaced session under the same key")
	}
}

// TestWsResponseCloseDiscardsBrokenConnection 验证读流标记 connBroken 后，Close()
// 销毁坏连接并移出连接池，而不是归还复用。
func TestWsResponseCloseDiscardsBrokenConnection(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)

	key := manager.poolKey(1, "wss://example.test/responses", "session-1", "")
	session := NewSession(1, manager)
	session.SetConnected(true)
	wc := &WsConnection{session: session, URL: "wss://example.test/responses", PoolKey: key}
	wc.SetState(StateConnected)
	wc.Touch()
	pr := session.AddPendingRequest("session-1")
	manager.connections.Store(key, wc)
	manager.sessions.Store(key, session)

	wsResp := &WsResponse{conn: wc, pendingReq: pr, sessionID: "session-1", manager: manager}
	wsResp.markConnBroken()

	if err := wsResp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := manager.connections.Load(key); ok {
		t.Fatal("broken connection must be removed from pool")
	}
	if _, ok := manager.sessions.Load(key); ok {
		t.Fatal("broken session must be removed from pool")
	}
	if wc.IsConnected() {
		t.Fatal("broken connection must be closed")
	}
	if session.PendingCount() != 0 {
		t.Fatalf("pending must be cleared, got %d", session.PendingCount())
	}
}

// TestWsResponseCloseReleasesHealthyConnection 验证正常完成(未标记 connBroken)的
// 响应关闭后，连接仍留在池中、保持 connected，可被后续请求复用。
func TestWsResponseCloseReleasesHealthyConnection(t *testing.T) {
	manager := NewManager()
	t.Cleanup(manager.Stop)

	key := manager.poolKey(1, "wss://example.test/responses", "session-1", "")
	session := NewSession(1, manager)
	session.SetConnected(true)
	wc := &WsConnection{session: session, URL: "wss://example.test/responses", PoolKey: key}
	wc.SetState(StateConnected)
	wc.Touch()
	pr := session.AddPendingRequest("session-1")
	manager.connections.Store(key, wc)
	manager.sessions.Store(key, session)

	wsResp := &WsResponse{conn: wc, pendingReq: pr, sessionID: "session-1", manager: manager}

	if err := wsResp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := manager.connections.Load(key); !ok {
		t.Fatal("healthy connection should remain in pool for reuse")
	}
	if !wc.IsConnected() {
		t.Fatal("healthy connection should stay connected")
	}
	if session.PendingCount() != 0 {
		t.Fatalf("pending must be cleared, got %d", session.PendingCount())
	}
}

// TestReadStreamDiscardsConnectionOnAbnormalClose 端到端验证 issue #267 场景：
// 上游发送 close 1009 (message too big) 后，ReadStream 返回 read error 并标记坏连接，
// Close() 把连接从池中移除并关闭底层 socket（避免复用与 CLOSE_WAIT 滞留）。
func TestReadStreamDiscardsConnectionOnAbnormalClose(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		// 模拟上游异常关闭：发送 close 1009 (message too big)
		deadline := time.Now().Add(time.Second)
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseMessageTooBig, "message too big"),
			deadline,
		)
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}

	manager := NewManager()
	t.Cleanup(manager.Stop)

	key := manager.poolKey(1, wsURL, "session-1", "")
	session := NewSession(1, manager)
	session.SetConnected(true)
	wc := NewWsConnection(conn, session, wsURL)
	wc.PoolKey = key
	pr := session.AddPendingRequest("session-1")
	manager.connections.Store(key, wc)
	manager.sessions.Store(key, session)

	wsResp := &WsResponse{
		conn:        wc,
		pendingReq:  pr,
		sessionID:   "session-1",
		manager:     manager,
		readErrChan: make(chan error, 1),
	}

	err = wsResp.ReadStream(func(data []byte) bool { return true })
	if err == nil {
		t.Fatal("expected read error from abnormal close 1009")
	}
	if !strings.Contains(err.Error(), "websocket read error") {
		t.Fatalf("err = %v, want wrapped websocket read error", err)
	}

	if cerr := wsResp.Close(); cerr != nil {
		t.Fatalf("Close: %v", cerr)
	}
	if _, ok := manager.connections.Load(key); ok {
		t.Fatal("expected broken connection removed from pool after abnormal close")
	}
	if _, ok := manager.sessions.Load(key); ok {
		t.Fatal("expected broken session removed from pool after abnormal close")
	}
	if wc.IsConnected() {
		t.Fatal("expected broken connection to be closed")
	}
}
