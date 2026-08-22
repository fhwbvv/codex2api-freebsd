package auth

import "testing"

func TestHealthCountsNonBlockingSkipsBusyStoreLock(t *testing.T) {
	store := &Store{}
	store.mu.Lock()
	defer store.mu.Unlock()

	available, total, complete := store.HealthCountsNonBlocking()
	if available != -1 || total != -1 || complete {
		t.Fatalf("HealthCountsNonBlocking() = (%d, %d, %v), want (-1, -1, false)", available, total, complete)
	}
}

func TestHealthCountsNonBlockingMarksBusyAccountIncomplete(t *testing.T) {
	account := &Account{AccessToken: "token"}
	store := &Store{accounts: []*Account{account}}
	account.mu.Lock()
	defer account.mu.Unlock()

	available, total, complete := store.HealthCountsNonBlocking()
	if available != 0 || total != 1 || complete {
		t.Fatalf("HealthCountsNonBlocking() = (%d, %d, %v), want (0, 1, false)", available, total, complete)
	}
}

func TestHealthCountsNonBlockingCountsAvailableAccounts(t *testing.T) {
	store := &Store{accounts: []*Account{
		{AccessToken: "available"},
		{AccessToken: "disabled", Disabled: 1},
	}}

	available, total, complete := store.HealthCountsNonBlocking()
	if available != 1 || total != 2 || !complete {
		t.Fatalf("HealthCountsNonBlocking() = (%d, %d, %v), want (1, 2, true)", available, total, complete)
	}
}

func TestHealthCountsNonBlockingSkipsDispatchPausedAccounts(t *testing.T) {
	store := &Store{accounts: []*Account{
		{AccessToken: "available"},
		{AccessToken: "paused", DispatchPaused: 1},
	}}

	available, total, complete := store.HealthCountsNonBlocking()
	if available != 1 || total != 2 || !complete {
		t.Fatalf("HealthCountsNonBlocking() = (%d, %d, %v), want (1, 2, true)", available, total, complete)
	}
}

func TestHealthCountsNonBlockingHonorsLazyMode(t *testing.T) {
	refreshOnly := &Account{RefreshToken: "rt-only"}
	store := &Store{accounts: []*Account{refreshOnly}}

	available, _, complete := store.HealthCountsNonBlocking()
	if available != 0 || !complete {
		t.Fatalf("non-lazy HealthCountsNonBlocking() = (%d, complete=%v), want refresh-only account unavailable", available, complete)
	}

	store.lazyMode.Store(true)
	available, total, complete := store.HealthCountsNonBlocking()
	if available != 1 || total != 1 || !complete {
		t.Fatalf("lazy HealthCountsNonBlocking() = (%d, %d, %v), want (1, 1, true)", available, total, complete)
	}
}
