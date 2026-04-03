package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestNewSQLiteInitializesFreshDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")

	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}
	defer db.Close()

	if got := db.Driver(); got != "sqlite" {
		t.Fatalf("Driver() = %q, want %q", got, "sqlite")
	}
}

func TestSQLiteUsageLogsHasAPIKeyColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")

	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}
	defer db.Close()

	columns, err := db.sqliteTableColumns(context.Background(), "usage_logs")
	if err != nil {
		t.Fatalf("sqliteTableColumns 返回错误: %v", err)
	}

	for _, name := range []string{"api_key_id", "api_key_name", "api_key_masked"} {
		if _, ok := columns[name]; !ok {
			t.Fatalf("usage_logs 缺少列 %q", name)
		}
	}
}

func TestUsageLogsFilterByAPIKeyID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")

	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	targetAPIKeyID := int64(7)

	logs := []*UsageLogInput{
		{
			AccountID:    1,
			Endpoint:     "/v1/chat/completions",
			Model:        "gpt-5.4",
			StatusCode:   200,
			DurationMs:   120,
			APIKeyID:     targetAPIKeyID,
			APIKeyName:   "Team A",
			APIKeyMasked: "sk-a****...****1111",
		},
		{
			AccountID:    1,
			Endpoint:     "/v1/responses",
			Model:        "gpt-5.4",
			StatusCode:   200,
			DurationMs:   220,
			APIKeyID:     targetAPIKeyID,
			APIKeyName:   "Team A",
			APIKeyMasked: "sk-a****...****1111",
		},
		{
			AccountID:    2,
			Endpoint:     "/v1/responses",
			Model:        "gpt-5.4-mini",
			StatusCode:   200,
			DurationMs:   320,
			APIKeyID:     8,
			APIKeyName:   "Team B",
			APIKeyMasked: "sk-b****...****2222",
		},
	}

	for _, usageLog := range logs {
		if err := db.InsertUsageLog(ctx, usageLog); err != nil {
			t.Fatalf("InsertUsageLog 返回错误: %v", err)
		}
	}
	db.flushLogs()

	recentLogs, err := db.ListRecentUsageLogs(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentUsageLogs 返回错误: %v", err)
	}
	if len(recentLogs) != len(logs) {
		t.Fatalf("recentLogs 长度 = %d, want %d", len(recentLogs), len(logs))
	}

	foundSnapshot := false
	for _, usageLog := range recentLogs {
		if usageLog.APIKeyID == targetAPIKeyID {
			foundSnapshot = true
			if usageLog.APIKeyName != "Team A" {
				t.Fatalf("APIKeyName = %q, want %q", usageLog.APIKeyName, "Team A")
			}
			if usageLog.APIKeyMasked != "sk-a****...****1111" {
				t.Fatalf("APIKeyMasked = %q, want %q", usageLog.APIKeyMasked, "sk-a****...****1111")
			}
		}
	}
	if !foundSnapshot {
		t.Fatal("未找到带 API 密钥快照的最近日志")
	}

	page, err := db.ListUsageLogsByTimeRangePaged(ctx, UsageLogFilter{
		Start:    now.Add(-1 * time.Hour),
		End:      now.Add(1 * time.Hour),
		Page:     1,
		PageSize: 10,
		APIKeyID: &targetAPIKeyID,
	})
	if err != nil {
		t.Fatalf("ListUsageLogsByTimeRangePaged 返回错误: %v", err)
	}

	if page.Total != 2 {
		t.Fatalf("page.Total = %d, want %d", page.Total, 2)
	}
	if len(page.Logs) != 2 {
		t.Fatalf("len(page.Logs) = %d, want %d", len(page.Logs), 2)
	}
	for _, usageLog := range page.Logs {
		if usageLog.APIKeyID != targetAPIKeyID {
			t.Fatalf("APIKeyID = %d, want %d", usageLog.APIKeyID, targetAPIKeyID)
		}
		if usageLog.APIKeyName != "Team A" {
			t.Fatalf("APIKeyName = %q, want %q", usageLog.APIKeyName, "Team A")
		}
	}
}
