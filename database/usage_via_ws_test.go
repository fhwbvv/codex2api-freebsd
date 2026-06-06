package database

import (
	"context"
	"path/filepath"
	"testing"
)

// TestUsageLogViaWebsocketRoundTrip 验证 via_websocket 字段从写入到读回完整保留
// （覆盖 InsertUsageLog 的批量 INSERT 与 ListRecentUsageLogs 的 SELECT/Scan）。
func TestUsageLogViaWebsocketRoundTrip(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "codex2api.db"))
	if err != nil {
		t.Fatalf("New(sqlite): %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// 一条走 WS、一条不走 WS
	if err := db.InsertUsageLog(ctx, &UsageLogInput{
		Endpoint: "/v1/responses", Model: "gpt-5.5", StatusCode: 200, ViaWebsocket: true,
	}); err != nil {
		t.Fatalf("InsertUsageLog ws: %v", err)
	}
	if err := db.InsertUsageLog(ctx, &UsageLogInput{
		Endpoint: "/v1/responses", Model: "gpt-5.5", StatusCode: 200, ViaWebsocket: false,
	}); err != nil {
		t.Fatalf("InsertUsageLog http: %v", err)
	}
	db.flushLogs()

	logs, err := db.ListRecentUsageLogs(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentUsageLogs: %v", err)
	}
	if len(logs) < 2 {
		t.Fatalf("got %d logs, want >= 2", len(logs))
	}

	var sawWS, sawHTTP bool
	for _, l := range logs {
		if l.ViaWebsocket {
			sawWS = true
		} else {
			sawHTTP = true
		}
	}
	if !sawWS {
		t.Error("expected at least one log with ViaWebsocket=true (字段未正确写入/读回)")
	}
	if !sawHTTP {
		t.Error("expected at least one log with ViaWebsocket=false")
	}
}
