package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// attempt_index 是 1-based：首次尝试写 1，第一次重试写 2。请求构成里的「重试」曾经写成
// attempt_index > 0，于是每个请求都被算成重试，这个指标恒等于总请求数（界面上显示 100%）。
func TestFeatureStatsRetryCountsOnlyRetryAttempts(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "retry-stats.db"))
	if err != nil {
		t.Fatalf("New(sqlite): %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	const accountID int64 = 42
	logs := []*UsageLogInput{
		// 一次成功的首次尝试：不是重试。
		{AccountID: accountID, Endpoint: "/v1/responses", Model: "gpt-5.4", StatusCode: 200, AttemptIndex: 1},
		// 失败的首次尝试（触发了重试）+ 成功的第二次尝试：只算 1 次重试。
		{AccountID: accountID, Endpoint: "/v1/responses", Model: "gpt-5.4", StatusCode: 429, AttemptIndex: 1, IsRetryAttempt: true},
		{AccountID: accountID, Endpoint: "/v1/responses", Model: "gpt-5.4", StatusCode: 200, AttemptIndex: 2},
		// 隐藏的续想轮不算尝试，attempt_index 保持 0。
		{AccountID: accountID, Endpoint: "/v1/responses", Model: "gpt-5.4", StatusCode: 200},
	}
	for _, input := range logs {
		if err := db.InsertUsageLog(ctx, input); err != nil {
			t.Fatalf("InsertUsageLog: %v", err)
		}
	}
	db.flushLogs()

	now := time.Now()
	stats, err := db.GetUsageStats(ctx, now.Add(-time.Hour), now.Add(time.Hour), "")
	if err != nil {
		t.Fatalf("GetUsageStats: %v", err)
	}
	if got := stats.FeatureStats.RetryRequests; got != 1 {
		t.Fatalf("RetryRequests = %d, want 1 (只有 attempt_index=2 那条是重试)", got)
	}
	if got := stats.FeatureStats.ErrorRequests; got != 1 {
		t.Fatalf("ErrorRequests = %d, want 1", got)
	}

	// 账号用量详情里的 retry 走的是另一条 SQL，同一个口径，别只修一边。
	detail, err := db.GetAccountUsageStats(ctx, accountID, 7)
	if err != nil {
		t.Fatalf("GetAccountUsageStats: %v", err)
	}
	if got := detail.RetryRequests; got != 1 {
		t.Fatalf("account RetryRequests = %d, want 1", got)
	}
}
