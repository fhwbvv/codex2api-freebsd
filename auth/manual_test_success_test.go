package auth

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestRecordManualTestSuccessRecoversReadyBannedAccount(t *testing.T) {
	store := NewStore(nil, nil, nil)
	acc := &Account{
		DBID:               1,
		AccessToken:        "at-test",
		Status:             StatusReady,
		HealthTier:         HealthTierBanned,
		FailureStreak:      4,
		LastFailureAt:      time.Now().Add(-time.Minute),
		LastUnauthorizedAt: time.Now().Add(-time.Minute),
	}
	atomic.StoreInt32(&acc.Disabled, 1)
	store.AddAccount(acc)

	store.RecordManualTestSuccess(acc, 123*time.Millisecond)

	acc.mu.RLock()
	status := acc.Status
	healthTier := acc.HealthTier
	failureStreak := acc.FailureStreak
	successStreak := acc.SuccessStreak
	lastSuccessAt := acc.LastSuccessAt
	acc.mu.RUnlock()

	if atomic.LoadInt32(&acc.Disabled) != 0 {
		t.Fatal("Disabled flag should be cleared")
	}
	if status != StatusReady {
		t.Fatalf("Status = %v, want ready", status)
	}
	if healthTier == HealthTierBanned {
		t.Fatal("HealthTier should recover from banned")
	}
	if failureStreak != 0 {
		t.Fatalf("FailureStreak = %d, want 0", failureStreak)
	}
	if successStreak == 0 || lastSuccessAt.IsZero() {
		t.Fatal("manual test success should be recorded")
	}
	if !acc.IsAvailable() {
		t.Fatal("account should be available after successful manual test")
	}
}
