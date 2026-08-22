package admin

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
)

// TestRefreshAccountByIDTriggersUsageProbe 验证 issue #300：手动刷新账号后，
// 会顺带触发一次用量探针（wham），从服务端权威数据同步订阅到期时间，
// 而不是仅依赖可能滞后的 token JWT。
func TestRefreshAccountByIDTriggersUsageProbe(t *testing.T) {
	db := newTestAdminDB(t)
	store := auth.NewStore(db, cache.NewMemory(1), nil)

	id, err := db.InsertAccountWithCredentials(context.Background(), "renew", map[string]interface{}{
		"refresh_token": "rt-renew",
		"access_token":  "at-renew",
		"email":         "renew@example.com",
		"account_id":    "acc-renew",
	}, "")
	if err != nil {
		t.Fatalf("InsertAccountWithCredentials: %v", err)
	}
	if err := store.LoadAccountByID(context.Background(), id); err != nil {
		t.Fatalf("LoadAccountByID: %v", err)
	}

	refreshed := false
	probedID := int64(0)
	handler := &Handler{
		db:    db,
		store: store,
		refreshAccount: func(context.Context, int64) error {
			refreshed = true
			return nil
		},
		probeUsage: func(_ context.Context, acc *auth.Account) error {
			if acc != nil {
				probedID = acc.DBID
			}
			return nil
		},
	}

	if err := handler.refreshAccountByID(context.Background(), id); err != nil {
		t.Fatalf("refreshAccountByID: %v", err)
	}
	if !refreshed {
		t.Fatal("token refresh was not invoked")
	}
	if probedID != id {
		t.Fatalf("usage probe ran for account %d, want %d (subscription expiry sync after refresh)", probedID, id)
	}
}

// TestRefreshAccountByIDSkipsProbeOnRefreshFailure 验证刷新失败时不再触发探针。
func TestRefreshAccountByIDSkipsProbeOnRefreshFailure(t *testing.T) {
	db := newTestAdminDB(t)
	store := auth.NewStore(db, cache.NewMemory(1), nil)

	probed := false
	handler := &Handler{
		db:    db,
		store: store,
		refreshAccount: func(context.Context, int64) error {
			return context.DeadlineExceeded
		},
		probeUsage: func(context.Context, *auth.Account) error {
			probed = true
			return nil
		},
	}

	if err := handler.refreshAccountByID(context.Background(), 1); err == nil {
		t.Fatal("refreshAccountByID should return the refresh error")
	}
	if probed {
		t.Fatal("usage probe should not run when token refresh fails")
	}
}

func TestRefreshImportedAccountMarksUnauthorizedOnInvalidatedRT(t *testing.T) {
	db := newTestAdminDB(t)
	store := auth.NewStore(db, cache.NewMemory(1), nil)

	id, err := db.InsertAccountWithCredentials(context.Background(), "import-1", map[string]interface{}{
		"refresh_token": "rt-dead",
	}, "")
	if err != nil {
		t.Fatalf("InsertAccountWithCredentials: %v", err)
	}
	if err := store.LoadAccountByID(context.Background(), id); err != nil {
		t.Fatalf("LoadAccountByID: %v", err)
	}
	if got := store.FindByID(id).RuntimeStatus(); got != "refreshing" {
		t.Fatalf("RuntimeStatus() before refresh = %q, want refreshing", got)
	}

	probed := false
	handler := &Handler{
		db:    db,
		store: store,
		refreshAccount: func(context.Context, int64) error {
			return fmt.Errorf(`刷新失败（重试 3 次）: 刷新失败 (status 401): {"error":{"message":"Your session has ended. Please log in again.","code":"refresh_token_invalidated"}}`)
		},
		probeUsage: func(context.Context, *auth.Account) error {
			probed = true
			return nil
		},
	}

	handler.refreshImportedAccountAndProbe(context.Background(), id, "import_refresh")
	if probed {
		t.Fatal("usage probe should not run when imported RT refresh fails")
	}
	acc := store.FindByID(id)
	if acc == nil {
		t.Fatal("imported account disappeared after refresh failure")
	}
	if got := acc.RuntimeStatus(); got != "unauthorized" {
		t.Fatalf("RuntimeStatus() = %q, want unauthorized", got)
	}
}

func TestRefreshImportedAccountRetriesTransientFailureBeforeError(t *testing.T) {
	db := newTestAdminDB(t)
	store := auth.NewStore(db, cache.NewMemory(1), nil)

	id, err := db.InsertAccountWithCredentials(context.Background(), "import-20", map[string]interface{}{
		"refresh_token": "rt-timeout",
	}, "")
	if err != nil {
		t.Fatalf("InsertAccountWithCredentials: %v", err)
	}
	if err := store.LoadAccountByID(context.Background(), id); err != nil {
		t.Fatalf("LoadAccountByID: %v", err)
	}

	originalDelay := importRefreshTransientRetryDelay
	importRefreshTransientRetryDelay = 5 * time.Millisecond
	t.Cleanup(func() { importRefreshTransientRetryDelay = originalDelay })

	var attempts atomic.Int64
	handler := &Handler{
		db:    db,
		store: store,
		refreshAccount: func(context.Context, int64) error {
			attempts.Add(1)
			return fmt.Errorf("刷新失败（重试 3 次）: context deadline exceeded")
		},
	}

	handler.refreshImportedAccountAndProbe(context.Background(), id, "import_refresh")
	acc := store.FindByID(id)
	if acc == nil {
		t.Fatal("imported account disappeared after refresh failure")
	}
	// 瞬时失败不再一次标死:第一次失败后应保持「刷新中」等待延迟重试。
	if got := acc.RuntimeStatus(); got != "refreshing" {
		t.Fatalf("RuntimeStatus() after first transient failure = %q, want refreshing", got)
	}

	deadline := time.Now().Add(5 * time.Second)
	for acc.RuntimeStatus() != "error" {
		if time.Now().After(deadline) {
			t.Fatalf("RuntimeStatus() = %q after retries, want error (attempts=%d)", acc.RuntimeStatus(), attempts.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := attempts.Load(); got != int64(importRefreshTransientRetryLimit)+1 {
		t.Fatalf("refresh attempts = %d, want %d (1 initial + %d retries)", got, importRefreshTransientRetryLimit+1, importRefreshTransientRetryLimit)
	}
}

func TestRefreshImportedAccountSkipsRemarkWhenStoreAlreadyMarked(t *testing.T) {
	db := newTestAdminDB(t)
	store := auth.NewStore(db, cache.NewMemory(1), nil)

	id, err := db.InsertAccountWithCredentials(context.Background(), "import-30", map[string]interface{}{
		"refresh_token": "rt-dead-marked",
	}, "")
	if err != nil {
		t.Fatalf("InsertAccountWithCredentials: %v", err)
	}
	if err := store.LoadAccountByID(context.Background(), id); err != nil {
		t.Fatalf("LoadAccountByID: %v", err)
	}

	handler := &Handler{
		db:    db,
		store: store,
		// 模拟 store 刷新路径:内部已对不可重试错误标了未授权冷却,
		// 错误再冒回导入收尾。此时不能重复标记,更不能降级成 error。
		refreshAccount: func(_ context.Context, accountID int64) error {
			acc := store.FindByID(accountID)
			store.MarkCooldownWithError(acc, 24*time.Hour, "unauthorized", "invalid_grant")
			return fmt.Errorf("刷新失败（重试 3 次）: 刷新失败 (status 400): invalid_grant")
		},
	}

	handler.refreshImportedAccountAndProbe(context.Background(), id, "import_refresh")
	acc := store.FindByID(id)
	if acc == nil {
		t.Fatal("imported account disappeared after refresh failure")
	}
	if got := acc.RuntimeStatus(); got != "unauthorized" {
		t.Fatalf("RuntimeStatus() = %q, want unauthorized (must not be re-marked or downgraded)", got)
	}
	acc.Mu().RLock()
	streak := acc.FailureStreak
	acc.Mu().RUnlock()
	if streak > 1 {
		t.Fatalf("FailureStreak = %d, want <= 1 (double-marking escalates the adaptive cooldown)", streak)
	}
}
