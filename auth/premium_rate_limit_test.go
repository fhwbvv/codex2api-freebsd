package auth

import (
	"context"
	"testing"
	"time"
)

func newPremium5hTestAccount(plan string, resetAt time.Time) *Account {
	return &Account{
		DBID:                1,
		AccessToken:         "token",
		PlanType:            plan,
		Status:              StatusReady,
		HealthTier:          HealthTierHealthy,
		UsagePercent5h:      100,
		UsagePercent5hValid: true,
		Reset5hAt:           resetAt,
		UsageUpdatedAt:      time.Now().Add(-20 * time.Minute),
	}
}

func TestPremium5hRateLimitedAccountIsFencedFromScheduling(t *testing.T) {
	acc := newPremium5hTestAccount("plus", time.Now().Add(45*time.Minute))

	snapshot := acc.GetSchedulerDebugSnapshot(4)
	if got := acc.RuntimeStatus(); got != "rate_limited" {
		t.Fatalf("RuntimeStatus() = %q, want rate_limited", got)
	}
	if acc.IsAvailable() {
		t.Fatal("IsAvailable() = true, want false for premium 5h rate limited account")
	}
	if snapshot.HealthTier != string(HealthTierRisky) {
		t.Fatalf("HealthTier = %q, want %q", snapshot.HealthTier, HealthTierRisky)
	}
	if snapshot.DynamicConcurrencyLimit != 1 {
		t.Fatalf("DynamicConcurrencyLimit = %d, want 1", snapshot.DynamicConcurrencyLimit)
	}
}

func TestPremium5hRateLimitExpiresAndUsageProbeResumes(t *testing.T) {
	acc := newPremium5hTestAccount("team", time.Now().Add(-time.Minute))
	acc.Status = StatusCooldown
	acc.CooldownReason = "rate_limited"
	acc.CooldownUtil = time.Now().Add(-time.Minute)

	snapshot := acc.GetSchedulerDebugSnapshot(4)
	if got := acc.RuntimeStatus(); got != "active" {
		t.Fatalf("RuntimeStatus() = %q, want active after reset expires", got)
	}
	if !acc.IsAvailable() {
		t.Fatal("IsAvailable() = false, want true after reset expires")
	}
	if snapshot.HealthTier != string(HealthTierHealthy) {
		t.Fatalf("HealthTier = %q, want %q", snapshot.HealthTier, HealthTierHealthy)
	}
	if snapshot.DynamicConcurrencyLimit != 4 {
		t.Fatalf("DynamicConcurrencyLimit = %d, want 4", snapshot.DynamicConcurrencyLimit)
	}
	if !acc.NeedsUsageProbe(10 * time.Minute) {
		t.Fatal("NeedsUsageProbe() = false, want true after reset expires and snapshot becomes stale")
	}
}

func TestPremium5hRateLimitedSkipsUsageProbeBeforeReset(t *testing.T) {
	acc := newPremium5hTestAccount("pro", time.Now().Add(30*time.Minute))

	if acc.NeedsUsageProbe(10 * time.Minute) {
		t.Fatal("NeedsUsageProbe() = true, want false before premium 5h reset time")
	}
}

func TestNormalizePlanTypeFoldsProliteIntoPro(t *testing.T) {
	cases := map[string]string{
		"prolite":   "pro",
		"ProLite":   "pro",
		" prolite ": "pro",
		"pro_lite":  "pro",
		"pro-lite":  "pro",
		"pro":       "pro",
		"plus":      "plus",
		"free":      "free",
		"":          "",
	}
	for input, want := range cases {
		if got := NormalizePlanType(input); got != want {
			t.Errorf("NormalizePlanType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestProliteIsTreatedAsPremium5hPlan(t *testing.T) {
	acc := newPremium5hTestAccount("prolite", time.Now().Add(30*time.Minute))
	if !acc.IsPremium5hPlan() {
		t.Fatal("prolite should be recognized as a premium 5h plan")
	}
	if !IsPlusOrHigherPlan("prolite") {
		t.Fatal("prolite should qualify as plus-or-higher for image routing")
	}
	if got := defaultScoreBiasForPlan("prolite"); got != 50 {
		t.Fatalf("defaultScoreBiasForPlan(prolite) = %d, want 50", got)
	}
}

func TestCleanByRuntimeStatusSkipsPremium5hRateLimitedAccount(t *testing.T) {
	acc := newPremium5hTestAccount("plus", time.Now().Add(20*time.Minute))
	store := &Store{
		accounts: []*Account{acc},
	}

	if cleaned := store.CleanByRuntimeStatus(context.Background(), "rate_limited"); cleaned != 0 {
		t.Fatalf("CleanByRuntimeStatus() cleaned = %d, want 0", cleaned)
	}
	if store.AccountCount() != 1 {
		t.Fatalf("AccountCount() = %d, want 1", store.AccountCount())
	}
}
