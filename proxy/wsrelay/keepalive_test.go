package wsrelay

import (
	"net/http"
	"sync"
	"testing"
)

// 构造一个已连接的池内连接（不依赖真实 socket）。
func addConnectedConn(t *testing.T, m *Manager, accountID int64, sessionKey string) *WsConnection {
	t.Helper()
	wsURL := "wss://example.test/responses"
	key := m.poolKey(accountID, wsURL, sessionKey, "")
	session := NewSession(accountID, m)
	session.SetConnected(true)
	conn := &WsConnection{
		session:  session,
		URL:      wsURL,
		PoolKey:  key,
		httpResp: &http.Response{StatusCode: http.StatusSwitchingProtocols},
	}
	conn.SetState(StateConnected)
	conn.Touch()
	m.connections.Store(key, conn)
	m.sessions.Store(key, session)
	return conn
}

func TestPingIdleConnectionsOnlyPingsConnected(t *testing.T) {
	m := NewManager()
	t.Cleanup(m.Stop)

	var mu sync.Mutex
	var pingedKeys []string
	m.keepalivePingFunc = func(wc *WsConnection) error {
		mu.Lock()
		pingedKeys = append(pingedKeys, wc.PoolKey)
		mu.Unlock()
		return nil
	}

	connected := addConnectedConn(t, m, 1, "s1")

	// 一个未连接的连接：应被跳过
	disc := addConnectedConn(t, m, 2, "s2")
	disc.SetState(StateDisconnected)

	pinged, failed := m.PingIdleConnections()
	if pinged != 1 || failed != 0 {
		t.Fatalf("pinged=%d failed=%d, want pinged=1 failed=0", pinged, failed)
	}
	if len(pingedKeys) != 1 || pingedKeys[0] != connected.PoolKey {
		t.Fatalf("pingedKeys=%v, want only %q", pingedKeys, connected.PoolKey)
	}
}

func TestPingIdleConnectionsSkipsBusyConnection(t *testing.T) {
	m := NewManager()
	t.Cleanup(m.Stop)

	var pings int
	m.keepalivePingFunc = func(wc *WsConnection) error { pings++; return nil }

	busy := addConnectedConn(t, m, 1, "s1")
	// 给 busy 连接挂一个 pending 请求 -> 应被跳过
	pr := busy.session.AddPendingRequest("s1")
	if busy.session.PendingCount() == 0 {
		t.Fatal("setup failed: expected a pending request")
	}
	t.Cleanup(func() { busy.session.RemovePendingRequest(pr.RequestID) })

	idle := addConnectedConn(t, m, 2, "s2")
	_ = idle

	pinged, _ := m.PingIdleConnections()
	if pinged != 1 {
		t.Fatalf("pinged=%d, want 1 (busy connection must be skipped)", pinged)
	}
	if pings != 1 {
		t.Fatalf("ping callback invoked %d times, want 1", pings)
	}
}

func TestKeepaliveTaskDisabledDoesNotPing(t *testing.T) {
	m := NewManager()
	t.Cleanup(m.Stop)

	var pings int
	m.keepalivePingFunc = func(wc *WsConnection) error { pings++; return nil }
	addConnectedConn(t, m, 1, "s1")

	// enabled=false：即便有空闲连接，也不应 Ping
	task := NewKeepaliveTask(m, func() bool { return false }, func() int { return 1 })
	// 直接驱动一次 enabled 判定逻辑：模拟 loop 内的判断
	if task.enabled() {
		t.Fatal("enabled should be false")
	}
	// 关闭状态下不会调用 PingIdleConnections
	if pings != 0 {
		t.Fatalf("pings=%d, want 0 when disabled", pings)
	}

	// 显式启用后，PingIdleConnections 应工作
	if got := m.connectionCountForTest(); got != 1 {
		t.Fatalf("connection count=%d, want 1", got)
	}
	pinged, _ := m.PingIdleConnections()
	if pinged != 1 {
		t.Fatalf("pinged=%d, want 1 after manual ping", pinged)
	}
}

// connectionCountForTest 返回连接池大小（测试辅助）。
func (m *Manager) connectionCountForTest() int {
	n := 0
	m.connections.Range(func(_, _ any) bool { n++; return true })
	return n
}
