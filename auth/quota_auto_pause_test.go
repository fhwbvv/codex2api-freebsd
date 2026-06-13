package auth

import (
	"testing"
	"time"
)

func newQuotaAutoPauseTestAccount() *Account {
	acc := &Account{
		DBID:        1,
		AccessToken: "token",
		PlanType:    "plus",
		Status:      StatusReady,
		HealthTier:  HealthTierHealthy,
	}
	return acc
}

func setAutoPauseThresholds(acc *Account) {
	acc.recomputeEffectiveAutoPause(nil)
}

func TestQuotaAutoPause5hThresholdFencesAccount(t *testing.T) {
	acc := newQuotaAutoPauseTestAccount()
	acc.AutoPause5hThreshold = 0.95
	acc.UsagePercent5h = 95
	acc.UsagePercent5hValid = true
	acc.Reset5hAt = time.Now().Add(time.Hour)
	setAutoPauseThresholds(acc)

	if acc.IsAvailable() {
		t.Fatal("IsAvailable() = true, want false after 5h auto-pause threshold is reached")
	}
	if got := acc.RuntimeStatus(); got != "quota_paused" {
		t.Fatalf("RuntimeStatus() = %q, want quota_paused after auto-pause threshold is reached", got)
	}
	_, _, _, _, available := acc.fastSchedulerSnapshot(4, time.Now())
	if available {
		t.Fatal("fastSchedulerSnapshot available = true, want false")
	}
}

func TestQuotaAutoPause5hThresholdRefreshesMissing5hBeforeFencing(t *testing.T) {
	acc := newQuotaAutoPauseTestAccount()
	acc.AutoPause5hThreshold = 0.95
	acc.UsagePercent7d = 12
	acc.UsagePercent7dValid = true
	acc.UsageUpdatedAt = time.Now()
	setAutoPauseThresholds(acc)

	if !acc.NeedsUsageProbe(10 * time.Minute) {
		t.Fatal("NeedsUsageProbe() = false, want true when 7d is fresh but 5h snapshot is missing")
	}

	acc.SetUsageSnapshot5h(95, time.Now().Add(time.Hour))

	if acc.IsAvailable() {
		t.Fatal("IsAvailable() = true, want false after refreshed 5h usage reaches the threshold")
	}
	if got := acc.RuntimeStatus(); got != "quota_paused" {
		t.Fatalf("RuntimeStatus() = %q, want quota_paused after refreshed 5h usage reaches the threshold", got)
	}
}

func TestQuotaAutoPauseIgnoresBelowThresholdAndDisabledWindow(t *testing.T) {
	acc := newQuotaAutoPauseTestAccount()
	acc.AutoPause5hThreshold = 0.95
	acc.UsagePercent5h = 94.9
	acc.UsagePercent5hValid = true
	acc.Reset5hAt = time.Now().Add(time.Hour)
	setAutoPauseThresholds(acc)

	if !acc.IsAvailable() {
		t.Fatal("IsAvailable() = false, want true below threshold")
	}

	acc.UsagePercent5h = 99
	acc.AutoPause5hDisabled = true
	setAutoPauseThresholds(acc)
	if !acc.IsAvailable() {
		t.Fatal("IsAvailable() = false, want true when 5h auto-pause is disabled")
	}
	if got := acc.RuntimeStatus(); got != "active" {
		t.Fatalf("RuntimeStatus() = %q, want active when 5h auto-pause is disabled", got)
	}
}

func TestQuotaAutoPauseStopsAfterResetTime(t *testing.T) {
	acc := newQuotaAutoPauseTestAccount()
	acc.AutoPause5hThreshold = 0.95
	acc.UsagePercent5h = 99
	acc.UsagePercent5hValid = true
	acc.Reset5hAt = time.Now().Add(-time.Minute)
	setAutoPauseThresholds(acc)

	if !acc.IsAvailable() {
		t.Fatal("IsAvailable() = false, want true after reset time has passed")
	}
}

func TestQuotaAutoPause7dThresholdFencesAccount(t *testing.T) {
	acc := newQuotaAutoPauseTestAccount()
	acc.AutoPause7dThreshold = 0.9
	acc.UsagePercent7d = 91
	acc.UsagePercent7dValid = true
	setAutoPauseThresholds(acc)

	if acc.IsAvailable() {
		t.Fatal("IsAvailable() = true, want false after 7d auto-pause threshold is reached")
	}
}
