package auth

import (
	"testing"
	"time"
)

func newQuotaAutoPauseTestAccount() *Account {
	return &Account{
		DBID:        1,
		AccessToken: "token",
		PlanType:    "plus",
		Status:      StatusReady,
		HealthTier:  HealthTierHealthy,
	}
}

func TestQuotaAutoPause5hThresholdFencesAccount(t *testing.T) {
	acc := newQuotaAutoPauseTestAccount()
	acc.AutoPause5hThreshold = 0.95
	acc.UsagePercent5h = 95
	acc.UsagePercent5hValid = true
	acc.Reset5hAt = time.Now().Add(time.Hour)

	if acc.IsAvailable() {
		t.Fatal("IsAvailable() = true, want false after 5h auto-pause threshold is reached")
	}
	if got := acc.RuntimeStatus(); got != "active" {
		t.Fatalf("RuntimeStatus() = %q, want active because auto-pause is scheduling-only", got)
	}
	_, _, _, _, available := acc.fastSchedulerSnapshot(4, time.Now())
	if available {
		t.Fatal("fastSchedulerSnapshot available = true, want false")
	}
}

func TestQuotaAutoPauseIgnoresBelowThresholdAndDisabledWindow(t *testing.T) {
	acc := newQuotaAutoPauseTestAccount()
	acc.AutoPause5hThreshold = 0.95
	acc.UsagePercent5h = 94.9
	acc.UsagePercent5hValid = true
	acc.Reset5hAt = time.Now().Add(time.Hour)

	if !acc.IsAvailable() {
		t.Fatal("IsAvailable() = false, want true below threshold")
	}

	acc.UsagePercent5h = 99
	acc.AutoPause5hDisabled = true
	if !acc.IsAvailable() {
		t.Fatal("IsAvailable() = false, want true when 5h auto-pause is disabled")
	}
}

func TestQuotaAutoPauseStopsAfterResetTime(t *testing.T) {
	acc := newQuotaAutoPauseTestAccount()
	acc.AutoPause5hThreshold = 0.95
	acc.UsagePercent5h = 99
	acc.UsagePercent5hValid = true
	acc.Reset5hAt = time.Now().Add(-time.Minute)

	if !acc.IsAvailable() {
		t.Fatal("IsAvailable() = false, want true after reset time has passed")
	}
}

func TestQuotaAutoPause7dThresholdFencesAccount(t *testing.T) {
	acc := newQuotaAutoPauseTestAccount()
	acc.AutoPause7dThreshold = 0.9
	acc.UsagePercent7d = 91
	acc.UsagePercent7dValid = true

	if acc.IsAvailable() {
		t.Fatal("IsAvailable() = true, want false after 7d auto-pause threshold is reached")
	}
}
