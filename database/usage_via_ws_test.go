package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"
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

func TestUsageLogClientIPRoundTripAndFilter(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "codex2api.db"))
	if err != nil {
		t.Fatalf("New(sqlite): %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if err := db.InsertUsageLog(ctx, &UsageLogInput{
		Endpoint:   "/v1/responses",
		Model:      "gpt-5.5",
		StatusCode: 200,
		ClientIP:   "203.0.113.42",
	}); err != nil {
		t.Fatalf("InsertUsageLog with client ip: %v", err)
	}
	if err := db.InsertUsageLog(ctx, &UsageLogInput{
		Endpoint:   "/v1/responses",
		Model:      "gpt-5.5",
		StatusCode: 200,
		ClientIP:   "198.51.100.7",
	}); err != nil {
		t.Fatalf("InsertUsageLog with other client ip: %v", err)
	}
	db.flushLogs()

	logs, err := db.ListRecentUsageLogs(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentUsageLogs: %v", err)
	}
	var sawIP bool
	for _, l := range logs {
		if l.ClientIP == "203.0.113.42" {
			sawIP = true
			break
		}
	}
	if !sawIP {
		t.Fatalf("expected usage log with ClientIP=203.0.113.42, got %#v", logs)
	}

	page, err := db.ListUsageLogsByTimeRangePaged(ctx, UsageLogFilter{
		Start:    time.Now().Add(-1 * time.Hour),
		End:      time.Now().Add(1 * time.Hour),
		Page:     1,
		PageSize: 10,
		Email:    "203.0.113",
	})
	if err != nil {
		t.Fatalf("ListUsageLogsByTimeRangePaged with ip filter: %v", err)
	}
	if page.Total != 1 || len(page.Logs) != 1 || page.Logs[0].ClientIP != "203.0.113.42" {
		t.Fatalf("ip filter page = total:%d logs:%#v, want one 203.0.113.42 log", page.Total, page.Logs)
	}
}
