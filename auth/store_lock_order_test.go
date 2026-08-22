package auth

import (
	"testing"
	"time"

	"github.com/codex2api/database"
)

func newStoreLockOrderFixture(t *testing.T) (*Store, *FastScheduler, *Account) {
	t.Helper()

	store := NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency:       2,
		FastSchedulerEnabled: true,
	})
	account := &Account{
		DBID:        1,
		AccessToken: "token",
		Status:      StatusReady,
		PlanType:    "plus",
	}
	store.AddAccount(account)

	scheduler := store.getFastScheduler()
	if scheduler == nil {
		t.Fatal("fast scheduler is not enabled")
	}
	return store, scheduler, account
}

func runStoreSchedulerLockOrderRace(
	t *testing.T,
	store *Store,
	scheduler *FastScheduler,
	mutate func(),
) {
	t.Helper()

	filterEntered := make(chan struct{})
	allowStoreRead := make(chan struct{})
	acquireDone := make(chan struct{})
	go func() {
		defer close(acquireDone)
		scheduler.AcquireExcludingWithFilter(0, nil, func(*Account) bool {
			close(filterEntered)
			<-allowStoreRead
			store.mu.RLock()
			store.mu.RUnlock()
			return false
		})
	}()

	select {
	case <-filterEntered:
	case <-time.After(time.Second):
		t.Fatal("scheduler filter did not start")
	}

	// Hold Store.mu while the mutation queues for it. Once the filter is
	// released, the queued writer gets Store.mu first. Account mutations must
	// release that lock before waiting for FastScheduler.mu, otherwise the two
	// goroutines form the production deadlock.
	store.mu.Lock()
	mutationStarted := make(chan struct{})
	mutationDone := make(chan struct{})
	go func() {
		close(mutationStarted)
		mutate()
		close(mutationDone)
	}()
	<-mutationStarted
	time.Sleep(20 * time.Millisecond)
	close(allowStoreRead)
	store.mu.Unlock()

	select {
	case <-mutationDone:
	case <-time.After(time.Second):
		t.Fatal("account mutation deadlocked with scheduler acquisition")
	}
	select {
	case <-acquireDone:
	case <-time.After(time.Second):
		t.Fatal("scheduler acquisition did not finish")
	}
}

func TestAddAccountsDoesNotInvertStoreAndSchedulerLocks(t *testing.T) {
	store, scheduler, _ := newStoreLockOrderFixture(t)
	added := &Account{
		DBID:        2,
		AccessToken: "token-2",
		Status:      StatusReady,
		PlanType:    "plus",
	}

	runStoreSchedulerLockOrderRace(t, store, scheduler, func() {
		store.AddAccounts([]*Account{added})
	})

	if got := store.FindByID(added.DBID); got != added {
		t.Fatalf("added account = %p, want %p", got, added)
	}
}

func TestRemoveAccountDoesNotInvertStoreAndSchedulerLocks(t *testing.T) {
	store, scheduler, account := newStoreLockOrderFixture(t)

	runStoreSchedulerLockOrderRace(t, store, scheduler, func() {
		store.RemoveAccount(account.DBID)
	})

	if got := store.FindByID(account.DBID); got != nil {
		t.Fatalf("removed account still present: %p", got)
	}
}

func TestRemoveAccountsDoesNotInvertStoreAndSchedulerLocks(t *testing.T) {
	store, scheduler, account := newStoreLockOrderFixture(t)

	runStoreSchedulerLockOrderRace(t, store, scheduler, func() {
		store.RemoveAccounts([]int64{account.DBID})
	})

	if got := store.FindByID(account.DBID); got != nil {
		t.Fatalf("removed account still present: %p", got)
	}
}

func newStoreRecursiveReadFixture(t *testing.T) *Store {
	t.Helper()

	store := NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 2})
	store.AddAccount(&Account{
		DBID:        1,
		AccessToken: "token",
		Status:      StatusReady,
		PlanType:    "plus",
	})
	return store
}

func runStoreRecursiveReadLockRace(t *testing.T, store *Store, call func(AccountFilter)) {
	t.Helper()

	filterEntered := make(chan struct{})
	allowNestedRead := make(chan struct{})
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		call(func(*Account) bool {
			close(filterEntered)
			<-allowNestedRead
			_ = store.GetProxyURL()
			return false
		})
	}()

	select {
	case <-filterEntered:
	case <-time.After(time.Second):
		t.Fatal("account filter did not start")
	}

	writerStarted := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		close(writerStarted)
		store.SetProxyURL("http://writer.invalid")
		close(writerDone)
	}()
	<-writerStarted
	time.Sleep(20 * time.Millisecond)
	close(allowNestedRead)

	select {
	case <-callDone:
	case <-time.After(time.Second):
		t.Fatal("account filter deadlocked on a recursive Store read")
	}
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("queued Store writer did not finish")
	}
}

func TestAccountFiltersRunOutsideStoreReadLock(t *testing.T) {
	tests := []struct {
		name string
		call func(*Store, AccountFilter)
	}{
		{
			name: "candidate check",
			call: func(store *Store, filter AccountFilter) {
				store.hasDispatchCandidateWithFilter(0, nil, filter)
			},
		},
		{
			name: "fallback scheduler",
			call: func(store *Store, filter AccountFilter) {
				store.NextExcludingWithFilter(0, nil, filter)
			},
		},
		{
			name: "lazy scheduler",
			call: func(store *Store, filter AccountFilter) {
				store.nextExcludingWithFilterLazy(0, nil, filter, DispatchPolicyStandard)
			},
		},
		{
			name: "affinity spread",
			call: func(store *Store, filter AccountFilter) {
				store.affinitySpreadEnabled.Store(true)
				store.nextAccountForFreshAffinity("session", 0, nil, filter)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newStoreRecursiveReadFixture(t)
			runStoreRecursiveReadLockRace(t, store, func(filter AccountFilter) {
				tc.call(store, filter)
			})
		})
	}
}
