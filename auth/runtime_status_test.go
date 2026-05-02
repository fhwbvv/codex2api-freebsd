package auth

import "testing"

func TestRuntimeStatusShowsRefreshingForRTWithoutAccessToken(t *testing.T) {
	acc := &Account{
		RefreshToken: "rt-test",
		Status:       StatusReady,
	}

	if got := acc.RuntimeStatus(); got != "refreshing" {
		t.Fatalf("RuntimeStatus() = %q, want refreshing", got)
	}
}

func TestRuntimeStatusKeepsErrorForFailedRTAccount(t *testing.T) {
	acc := &Account{
		RefreshToken: "rt-test",
		Status:       StatusError,
		ErrorMsg:     "invalid_grant",
	}

	if got := acc.RuntimeStatus(); got != "error" {
		t.Fatalf("RuntimeStatus() = %q, want error", got)
	}
}

func TestMarkErrorAndClearCooldownRoundTrip(t *testing.T) {
	store := NewStore(nil, nil, nil)
	acc := &Account{
		DBID:        1,
		AccessToken: "at-test",
		Status:      StatusReady,
	}

	store.MarkError(acc, "batch test failed")
	if got := acc.RuntimeStatus(); got != "error" {
		t.Fatalf("RuntimeStatus() after MarkError = %q, want error", got)
	}

	store.ClearCooldown(acc)
	if got := acc.RuntimeStatus(); got != "active" {
		t.Fatalf("RuntimeStatus() after ClearCooldown = %q, want active", got)
	}
}
