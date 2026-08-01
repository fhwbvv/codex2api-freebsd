package wsrelay

import (
	"testing"
	"time"

	"github.com/codex2api/proxy"
)

func setWeakNetworkModeForTest(t *testing.T, enabled bool) {
	t.Helper()
	previous := proxy.CurrentRuntimeSettings()
	next := previous
	next.CodexWSWeakNetworkMode = enabled
	proxy.ApplyRuntimeSettings(next)
	t.Cleanup(func() { proxy.ApplyRuntimeSettings(previous) })
}

func TestWeakNetworkModeUsesConservativeConnectionWindows(t *testing.T) {
	setWeakNetworkModeForTest(t, false)

	now := time.Now()
	wc := &WsConnection{}
	wc.lastUsed.Store(now.Add(-20 * time.Second).UnixNano())
	wc.createdAt = now.Add(-4 * time.Minute).UnixNano()
	session := NewSession(1, nil)
	session.LastActiveAt = now.Add(-20 * time.Second)

	if wc.IsExpired() || wc.IsOverAge() || session.IsExpired() {
		t.Fatal("normal mode must retain a connection that is idle for 20s and 4m old")
	}

	next := proxy.CurrentRuntimeSettings()
	next.CodexWSWeakNetworkMode = true
	proxy.ApplyRuntimeSettings(next)

	if !wc.IsExpired() {
		t.Fatal("weak-network mode must expire a connection after 15s idle")
	}
	if !wc.IsOverAge() {
		t.Fatal("weak-network mode must rotate a connection after 3m total lifetime")
	}
	if !session.IsExpired() {
		t.Fatal("weak-network mode must expire the matching session after 15s idle")
	}
}

func TestWeakNetworkModeSuppressesIdleKeepalive(t *testing.T) {
	setWeakNetworkModeForTest(t, true)

	manager := NewManager()
	t.Cleanup(manager.Stop)
	addConnectedConn(t, manager, 1, "weak-idle")

	pings := 0
	manager.keepalivePingFunc = func(*WsConnection) error {
		pings++
		return nil
	}

	pinged, failed := manager.PingIdleConnections()
	if pinged != 0 || failed != 0 || pings != 0 {
		t.Fatalf("weak-network keepalive = pinged:%d failed:%d callbacks:%d, want all zero", pinged, failed, pings)
	}
}

func TestWeakNetworkModeStopsIdleSessionHeartbeat(t *testing.T) {
	setWeakNetworkModeForTest(t, true)

	session := NewSession(1, nil)
	session.SetConnected(true)
	session.StartHeartbeat(func() error { return nil })
	t.Cleanup(session.StopHeartbeat)

	pings := 0
	session.heartbeatTick(func() error {
		pings++
		return nil
	})

	if pings != 0 {
		t.Fatalf("idle session heartbeat callbacks = %d, want 0", pings)
	}
	session.mu.RLock()
	timer := session.heartbeatTimer
	session.mu.RUnlock()
	if timer != nil {
		t.Fatal("weak-network mode must stop the idle session heartbeat timer")
	}
}

func TestWeakNetworkModeRetainsHeartbeatForInFlightRequest(t *testing.T) {
	setWeakNetworkModeForTest(t, true)

	session := NewSession(1, nil)
	session.SetConnected(true)
	session.StartHeartbeat(func() error { return nil })
	t.Cleanup(session.StopHeartbeat)
	pending := session.AddPendingRequest("weak-busy")
	t.Cleanup(func() { session.RemovePendingRequest(pending.RequestID) })

	pings := 0
	session.heartbeatTick(func() error {
		pings++
		return nil
	})

	if pings != 1 {
		t.Fatalf("in-flight session heartbeat callbacks = %d, want 1", pings)
	}
}
