package database

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestGetAPIKeyAccountWindowUsageSplitsByAccount(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	keyID, err := db.InsertAPIKey(ctx, "scoped", "sk-scope-usage-a-1234567890")
	if err != nil {
		t.Fatalf("InsertAPIKey 返回错误: %v", err)
	}
	otherKeyID, err := db.InsertAPIKey(ctx, "other", "sk-scope-usage-b-1234567890")
	if err != nil {
		t.Fatalf("InsertAPIKey 返回错误: %v", err)
	}

	insert := func(apiKeyID, accountID int64, statusCode, totalTokens int, userBilled float64, at time.Time) {
		t.Helper()
		if _, err := db.conn.ExecContext(ctx, `
			INSERT INTO usage_logs (api_key_id, account_id, endpoint, model, status_code, total_tokens, user_billed, created_at)
			VALUES ($1, $2, '/v1/responses', 'gpt-5.4', $3, $4, $5, $6)
		`, apiKeyID, accountID, statusCode, totalTokens, userBilled, sqliteTimeParam(at)); err != nil {
			t.Fatalf("insert usage log: %v", err)
		}
	}

	now := time.Now()
	insert(keyID, 1, 200, 100, 0.10, now)
	insert(keyID, 1, 500, 50, 0.05, now)
	insert(keyID, 2, 200, 300, 0.30, now)
	insert(keyID, 1, 499, 900, 9.99, now)                   // 客户端取消不计入
	insert(keyID, 1, 200, 700, 7.00, now.Add(-8*time.Hour)) // 窗口外
	insert(otherKeyID, 1, 200, 400, 0.40, now)              // 另一个 Key 不串

	usage, err := db.GetAPIKeyAccountWindowUsage(ctx, keyID, 5*time.Hour)
	if err != nil {
		t.Fatalf("GetAPIKeyAccountWindowUsage 返回错误: %v", err)
	}
	if len(usage) != 2 {
		t.Fatalf("usage covered %d accounts, want 2: %+v", len(usage), usage)
	}
	if got := usage[1]; got.Requests != 2 || got.Tokens != 150 || math.Abs(got.UserBilled-0.15) > 1e-9 {
		t.Fatalf("account 1 usage = %+v, want 2 requests / 150 tokens / $0.15", got)
	}
	if got := usage[2]; got.Requests != 1 || got.Tokens != 300 {
		t.Fatalf("account 2 usage = %+v, want 1 request / 300 tokens", got)
	}

	// 更长的窗口把之前的用量也纳进来。
	usage, err = db.GetAPIKeyAccountWindowUsage(ctx, keyID, 24*time.Hour)
	if err != nil {
		t.Fatalf("GetAPIKeyAccountWindowUsage(1d) 返回错误: %v", err)
	}
	if got := usage[1]; got.Requests != 3 || got.Tokens != 850 {
		t.Fatalf("account 1 usage over 1d = %+v, want 3 requests / 850 tokens", got)
	}
}

func TestDeleteAccountGroupPrunesScopeLimits(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	keptID, err := db.CreateAccountGroup(ctx, "kept", "", "", 0, 0, sql.NullInt64{})
	if err != nil {
		t.Fatalf("CreateAccountGroup 返回错误: %v", err)
	}
	doomedID, err := db.CreateAccountGroup(ctx, "doomed", "", "", 0, 0, sql.NullInt64{})
	if err != nil {
		t.Fatalf("CreateAccountGroup 返回错误: %v", err)
	}

	keyID, err := db.InsertAPIKeyWithOptions(ctx, APIKeyInput{
		Name:            "scoped",
		Key:             "sk-scope-prune-1234567890",
		AllowedGroupIDs: []int64{keptID, doomedID},
		Limits: APIKeyLimits{ScopeLimits: []APIKeyScopeLimit{
			{ScopeType: APIKeyScopeTypeGroup, ScopeID: keptID, Cost1d: 5},
			{ScopeType: APIKeyScopeTypeGroup, ScopeID: doomedID, Cost1d: 3},
			{ScopeType: APIKeyScopeTypeAccount, ScopeID: doomedID, Cost1d: 1},
		}},
	})
	if err != nil {
		t.Fatalf("InsertAPIKeyWithOptions 返回错误: %v", err)
	}

	if err := db.DeleteAccountGroup(ctx, doomedID); err != nil {
		t.Fatalf("DeleteAccountGroup 返回错误: %v", err)
	}

	row, err := db.GetAPIKeyByID(ctx, keyID)
	if err != nil {
		t.Fatalf("GetAPIKeyByID 返回错误: %v", err)
	}
	if len(row.Limits.ScopeLimits) != 2 {
		t.Fatalf("scope limits = %+v, want the deleted group entry pruned", row.Limits.ScopeLimits)
	}
	for _, scope := range row.Limits.ScopeLimits {
		if scope.ResolveScopeType() == APIKeyScopeTypeGroup && scope.ScopeID == doomedID {
			t.Fatalf("deleted group scope survived: %+v", row.Limits.ScopeLimits)
		}
	}
	// 同 ID 的账号维度条目不能被顺带删掉。
	foundAccountScope := false
	for _, scope := range row.Limits.ScopeLimits {
		if scope.ResolveScopeType() == APIKeyScopeTypeAccount && scope.ScopeID == doomedID {
			foundAccountScope = true
		}
	}
	if !foundAccountScope {
		t.Fatalf("account scope with the same ID was pruned: %+v", row.Limits.ScopeLimits)
	}
}

func TestScopeCountersAccumulateAcrossGroupAndAccount(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	groupID, err := db.CreateAccountGroup(ctx, "premium", "", "", 0, 0, sql.NullInt64{})
	if err != nil {
		t.Fatalf("CreateAccountGroup: %v", err)
	}
	accountID := int64(4242)
	if _, err := db.conn.ExecContext(ctx, `INSERT INTO account_group_members (account_id, group_id) VALUES (?, ?)`, accountID, groupID); err != nil {
		t.Fatalf("insert group member: %v", err)
	}

	keyID, err := db.InsertAPIKeyWithOptions(ctx, APIKeyInput{
		Name: "cumulative",
		Key:  "sk-scope-cumulative-1234567890",
		Limits: APIKeyLimits{ScopeLimits: []APIKeyScopeLimit{
			{ScopeType: APIKeyScopeTypeGroup, ScopeID: groupID, QuotaCost: 1},
		}},
	})
	if err != nil {
		t.Fatalf("InsertAPIKeyWithOptions: %v", err)
	}

	log := func(tokens int) {
		t.Helper()
		if err := db.InsertUsageLog(ctx, &UsageLogInput{
			APIKeyID:     keyID,
			AccountID:    accountID,
			Endpoint:     "/v1/responses",
			Model:        "gpt-5.4",
			StatusCode:   200,
			TotalTokens:  tokens,
			InputTokens:  tokens / 2,
			OutputTokens: tokens - tokens/2,
		}); err != nil {
			t.Fatalf("InsertUsageLog: %v", err)
		}
		db.FlushUsageLogs()
	}
	log(100)
	log(50)

	counters, err := db.ListAPIKeyScopeCounters(ctx, keyID)
	if err != nil {
		t.Fatalf("ListAPIKeyScopeCounters: %v", err)
	}
	group := counters[APIKeyScopeCounterKey{ScopeType: APIKeyScopeTypeGroup, ScopeID: groupID}]
	if group.UsedRequests != 2 || group.UsedTokens != 150 {
		t.Fatalf("group counter = %+v, want 2 requests / 150 tokens", group)
	}
	if group.UsedCost <= 0 {
		t.Fatalf("group counter cost = %v, want the same figure user_billed got", group.UsedCost)
	}
	// 账号维度同时记账：同一笔消耗既算在分组上，也算在账号上。
	account := counters[APIKeyScopeCounterKey{ScopeType: APIKeyScopeTypeAccount, ScopeID: accountID}]
	if account.UsedRequests != 2 || account.UsedTokens != 150 {
		t.Fatalf("account counter = %+v, want 2 requests / 150 tokens", account)
	}

	// 重置只清零目标那一条，并记一次重置。
	if err := db.ResetAPIKeyScopeCounter(ctx, keyID, APIKeyScopeTypeGroup, groupID); err != nil {
		t.Fatalf("ResetAPIKeyScopeCounter: %v", err)
	}
	counters, err = db.ListAPIKeyScopeCounters(ctx, keyID)
	if err != nil {
		t.Fatalf("ListAPIKeyScopeCounters: %v", err)
	}
	group = counters[APIKeyScopeCounterKey{ScopeType: APIKeyScopeTypeGroup, ScopeID: groupID}]
	if group.UsedRequests != 0 || group.UsedTokens != 0 || group.UsedCost != 0 {
		t.Fatalf("group counter after reset = %+v, want zeroed", group)
	}
	if group.ResetCount != 1 || !group.LastResetAt.Valid {
		t.Fatalf("group counter reset bookkeeping = %+v, want reset_count=1 with a timestamp", group)
	}
	if account := counters[APIKeyScopeCounterKey{ScopeType: APIKeyScopeTypeAccount, ScopeID: accountID}]; account.UsedRequests != 2 {
		t.Fatalf("account counter = %+v, want untouched by the group reset", account)
	}
}

func TestScopeCountersSkipKeysWithoutCumulativeQuota(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	// 只配滑动窗口、没配累计额度的 Key 不该产生计数器行（落库热路径的成本要为零）。
	keyID, err := db.InsertAPIKeyWithOptions(ctx, APIKeyInput{
		Name: "windows-only",
		Key:  "sk-scope-windows-1234567890",
		Limits: APIKeyLimits{ScopeLimits: []APIKeyScopeLimit{
			{ScopeType: APIKeyScopeTypeAccount, ScopeID: 7, Cost1d: 5},
		}},
	})
	if err != nil {
		t.Fatalf("InsertAPIKeyWithOptions: %v", err)
	}
	if err := db.InsertUsageLog(ctx, &UsageLogInput{
		APIKeyID: keyID, AccountID: 7, Model: "gpt-5.4", StatusCode: 200,
		TotalTokens: 100, InputTokens: 50, OutputTokens: 50,
	}); err != nil {
		t.Fatalf("InsertUsageLog: %v", err)
	}
	db.FlushUsageLogs()

	counters, err := db.ListAPIKeyScopeCounters(ctx, keyID)
	if err != nil {
		t.Fatalf("ListAPIKeyScopeCounters: %v", err)
	}
	if len(counters) != 0 {
		t.Fatalf("counters = %+v, want none for a key without cumulative quota", counters)
	}
}

func TestDeleteAPIKeyRemovesScopeCounters(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	keyID, err := db.InsertAPIKey(ctx, "doomed", "sk-scope-delete-1234567890")
	if err != nil {
		t.Fatalf("InsertAPIKey: %v", err)
	}
	if err := db.ResetAPIKeyScopeCounter(ctx, keyID, APIKeyScopeTypeGroup, 3); err != nil {
		t.Fatalf("ResetAPIKeyScopeCounter: %v", err)
	}
	if counters, _ := db.ListAPIKeyScopeCounters(ctx, keyID); len(counters) != 1 {
		t.Fatalf("counters = %+v, want one row before deletion", counters)
	}
	if err := db.DeleteAPIKey(ctx, keyID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	counters, err := db.ListAPIKeyScopeCounters(ctx, keyID)
	if err != nil {
		t.Fatalf("ListAPIKeyScopeCounters: %v", err)
	}
	if len(counters) != 0 {
		t.Fatalf("counters = %+v, want the rows removed with the key", counters)
	}
}
