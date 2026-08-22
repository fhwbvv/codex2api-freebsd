package auth

import (
	"errors"
	"testing"

	"github.com/codex2api/database"
)

func newPermanentFailureTestStore() (*Store, *Account) {
	store := NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 4, TestConcurrency: 1, TestModel: "gpt-5.4"})
	acc := &Account{DBID: 1, RefreshToken: "rt-dead", PlanType: "plus"}
	store.AddAccount(acc)
	return store, acc
}

func TestPermanentRefreshFailureEscalatesToTerminalError(t *testing.T) {
	store, acc := newPermanentFailureTestStore()
	failure := errors.New(`刷新失败 (status 401): {"error":{"code":"refresh_token_invalidated"}}`)

	for i := 1; i < permanentRefreshFailureTerminalLimit; i++ {
		store.markPermanentRefreshFailure(acc, failure)
		if got := acc.RuntimeStatus(); got != "unauthorized" {
			t.Fatalf("failure #%d RuntimeStatus() = %q, want unauthorized", i, got)
		}
	}
	store.markPermanentRefreshFailure(acc, failure)
	if got := acc.RuntimeStatus(); got != "error" {
		t.Fatalf("RuntimeStatus() after %d failures = %q, want error", permanentRefreshFailureTerminalLimit, got)
	}
}

func TestTerminalRefreshFailureExitsRecoveryProbeRotation(t *testing.T) {
	store, acc := newPermanentFailureTestStore()
	failure := errors.New("invalid_grant")
	for range permanentRefreshFailureTerminalLimit {
		store.markPermanentRefreshFailure(acc, failure)
	}

	// banned 层在 runtimeStatusLocked 里无条件显示 unauthorized、且是恢复
	// 探测的准入层——终态账号必须已降出 banned,否则永远进不了 error 筛选。
	acc.mu.RLock()
	tier := acc.HealthTier
	acc.mu.RUnlock()
	if tier == HealthTierBanned {
		t.Fatal("terminal account must leave the banned tier (banned masks error display and feeds the probe rotation)")
	}
	if acc.NeedsRecoveryProbe(0) {
		t.Fatal("terminal dead-RT account must exit the recovery probe rotation")
	}

	// 兜底闸:请求侧 401 等路径把账号重新压回 banned 层时,判死计数未清零
	// 就仍不该拿同一个死 RT 去探测。
	acc.mu.Lock()
	acc.HealthTier = HealthTierBanned
	acc.mu.Unlock()
	if acc.NeedsRecoveryProbe(0) {
		t.Fatal("re-banned terminal account must stay out of the probe rotation while the counter stands")
	}

	// 人工清理(重新授权入口)重置判死计数,恢复探测资格。
	store.ClearCooldown(acc)
	acc.mu.Lock()
	acc.HealthTier = HealthTierBanned
	acc.mu.Unlock()
	if !acc.NeedsRecoveryProbe(0) {
		t.Fatal("ClearCooldown must reset the terminal counter and restore probe eligibility")
	}
}
