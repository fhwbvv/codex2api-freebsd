package auth

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestNextForSessionPrefersBoundAccountAndProxy(t *testing.T) {
	store := &Store{
		accounts: []*Account{
			{DBID: 1, AccessToken: "tok-1"},
			{DBID: 2, AccessToken: "tok-2"},
		},
		maxConcurrency: 2,
	}
	store.bindSessionAffinity("session-1", store.accounts[1], "http://proxy-2")

	acc, proxyURL := store.NextForSession("session-1", 0, nil)
	if acc == nil {
		t.Fatal("expected account")
	}
	if acc.DBID != 2 {
		t.Fatalf("account DBID = %d, want %d", acc.DBID, 2)
	}
	if proxyURL != "http://proxy-2" {
		t.Fatalf("proxyURL = %q, want %q", proxyURL, "http://proxy-2")
	}
}

func TestBindSessionAffinityUsesConfigurableTTL(t *testing.T) {
	t.Setenv("CODEX_SESSION_AFFINITY_TTL", "2h")
	store := &Store{}
	account := &Account{DBID: 1, AccessToken: "tok-1"}

	before := time.Now()
	store.bindSessionAffinity("session-ttl", account, "http://proxy-1")

	store.sessionMu.RLock()
	binding, ok := store.sessionBindings["session-ttl"]
	store.sessionMu.RUnlock()
	if !ok {
		t.Fatal("expected session binding")
	}
	if binding.expiresAt.Before(before.Add(2*time.Hour - time.Second)) {
		t.Fatalf("expiresAt too early: got %s want about 2h from now", binding.expiresAt)
	}
}

func TestNextForSessionFallsBackWhenBoundAccountExcluded(t *testing.T) {
	store := &Store{
		accounts: []*Account{
			{DBID: 1, AccessToken: "tok-1"},
			{DBID: 2, AccessToken: "tok-2"},
		},
		maxConcurrency: 2,
	}
	store.bindSessionAffinity("session-1", store.accounts[1], "http://proxy-2")

	acc, proxyURL := store.NextForSession("session-1", 0, map[int64]bool{2: true})
	if acc == nil {
		t.Fatal("expected fallback account")
	}
	if acc.DBID != 1 {
		t.Fatalf("account DBID = %d, want %d", acc.DBID, 1)
	}
	if proxyURL != "" {
		t.Fatalf("proxyURL = %q, want empty fallback proxy", proxyURL)
	}
}

func TestNextForSessionWithFilterFallsBackWhenBoundAccountRejected(t *testing.T) {
	store := &Store{
		accounts: []*Account{
			{DBID: 1, AccessToken: "tok-1", PlanType: "pro"},
			{DBID: 2, AccessToken: "tok-2", PlanType: "plus"},
		},
		maxConcurrency: 2,
	}
	store.bindSessionAffinity("session-1", store.accounts[1], "http://proxy-2")

	acc, proxyURL := store.NextForSessionWithFilter("session-1", 0, nil, func(acc *Account) bool {
		return acc.GetPlanType() == "pro"
	})
	if acc == nil {
		t.Fatal("expected fallback account")
	}
	if acc.DBID != 1 {
		t.Fatalf("account DBID = %d, want %d", acc.DBID, 1)
	}
	if proxyURL != "" {
		t.Fatalf("proxyURL = %q, want empty fallback proxy", proxyURL)
	}
}

func TestNextForSessionFallsBackWhenBoundAccountIsError(t *testing.T) {
	store := &Store{
		accounts: []*Account{
			{DBID: 1, AccessToken: "tok-1"},
			{DBID: 2, AccessToken: "tok-2", Status: StatusError, ErrorMsg: "deactivated_workspace"},
		},
		maxConcurrency: 2,
	}
	store.bindSessionAffinity("session-1", store.accounts[1], "http://proxy-2")

	acc, proxyURL := store.NextForSession("session-1", 0, nil)
	if acc == nil {
		t.Fatal("expected fallback account")
	}
	if acc.DBID != 1 {
		t.Fatalf("account DBID = %d, want %d", acc.DBID, 1)
	}
	if proxyURL != "" {
		t.Fatalf("proxyURL = %q, want empty fallback proxy", proxyURL)
	}
	if store.accounts[1].IsAvailable() {
		t.Fatal("error account should not be available for scheduling")
	}
}

func TestWaitForSessionAvailableReturnsBoundAccount(t *testing.T) {
	store := &Store{
		accounts: []*Account{
			{DBID: 1, AccessToken: "tok-1"},
			{DBID: 2, AccessToken: "tok-2"},
		},
		maxConcurrency: 1,
	}
	store.bindSessionAffinity("session-1", store.accounts[1], "http://proxy-2")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	acc, proxyURL := store.WaitForSessionAvailable(ctx, "session-1", 50*time.Millisecond, 0, nil)
	if acc == nil {
		t.Fatal("expected bound account")
	}
	if acc.DBID != 2 {
		t.Fatalf("account DBID = %d, want %d", acc.DBID, 2)
	}
	if proxyURL != "http://proxy-2" {
		t.Fatalf("proxyURL = %q, want %q", proxyURL, "http://proxy-2")
	}
}

func TestWaitForSessionAvailableFallsBackWhenBindingExpired(t *testing.T) {
	store := &Store{
		accounts: []*Account{
			{DBID: 1, AccessToken: "tok-1"},
		},
		maxConcurrency:  1,
		sessionBindings: map[string]sessionAffinity{"session-1": {accountID: 99, proxyURL: "http://stale", expiresAt: time.Now().Add(-time.Minute)}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	acc, proxyURL := store.WaitForSessionAvailable(ctx, "session-1", 50*time.Millisecond, 0, nil)
	if acc == nil {
		t.Fatal("expected fallback account")
	}
	if acc.DBID != 1 {
		t.Fatalf("account DBID = %d, want %d", acc.DBID, 1)
	}
	if proxyURL != "" {
		t.Fatalf("proxyURL = %q, want empty fallback proxy", proxyURL)
	}
}

func TestWaitForSessionAvailableRespectsExcludeSet(t *testing.T) {
	store := &Store{
		accounts: []*Account{
			{DBID: 1, AccessToken: "tok-1"},
			{DBID: 2, AccessToken: "tok-2"},
		},
		maxConcurrency: 1,
	}
	store.bindSessionAffinity("session-1", store.accounts[1], "http://proxy-2")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	acc, proxyURL := store.WaitForSessionAvailable(ctx, "session-1", 50*time.Millisecond, 0, map[int64]bool{2: true})
	if acc == nil {
		t.Fatal("expected fallback account")
	}
	if acc.DBID != 1 {
		t.Fatalf("account DBID = %d, want %d", acc.DBID, 1)
	}
	if proxyURL != "" {
		t.Fatalf("proxyURL = %q, want empty fallback proxy", proxyURL)
	}
}

func TestWaitForSessionAvailableReturnsImmediatelyWhenNoDispatchCandidate(t *testing.T) {
	store := &Store{
		accounts:       []*Account{},
		maxConcurrency: 1,
	}

	start := time.Now()
	acc, proxyURL := store.WaitForSessionAvailable(context.Background(), "", 2*time.Second, 0, nil)
	elapsed := time.Since(start)

	if acc != nil {
		t.Fatalf("account = %+v, want nil", acc)
	}
	if proxyURL != "" {
		t.Fatalf("proxyURL = %q, want empty", proxyURL)
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("WaitForSessionAvailable took %s with no dispatch candidates; want fast failure", elapsed)
	}
}

func TestWaitForSessionAvailableKeepsWaitingWhenCandidateIsBusy(t *testing.T) {
	account := &Account{DBID: 1, AccessToken: "tok-1"}
	store := &Store{
		accounts:       []*Account{account},
		maxConcurrency: 1,
	}
	atomic.StoreInt64(&account.ActiveRequests, 1)

	go func() {
		time.Sleep(75 * time.Millisecond)
		store.Release(account)
	}()

	acc, proxyURL := store.WaitForSessionAvailable(context.Background(), "", 500*time.Millisecond, 0, nil)
	if acc == nil {
		t.Fatal("expected busy candidate to become available")
	}
	if acc.DBID != 1 {
		t.Fatalf("account DBID = %d, want %d", acc.DBID, 1)
	}
	if proxyURL != "" {
		t.Fatalf("proxyURL = %q, want empty", proxyURL)
	}
}

func TestUnbindSessionAffinityRemovesMatchingBinding(t *testing.T) {
	store := &Store{
		accounts: []*Account{
			{DBID: 1, AccessToken: "tok-1"},
		},
		maxConcurrency: 1,
	}
	// 绑定一个不在 accounts 列表中的账号，unbind 后只能回退到 DBID=1
	store.bindSessionAffinity("session-1", &Account{DBID: 2, AccessToken: "tok-2"}, "http://proxy-2")

	store.UnbindSessionAffinity("session-1", 2)

	acc, proxyURL := store.NextForSession("session-1", 0, nil)
	if acc == nil {
		t.Fatal("expected fallback account")
	}
	if acc.DBID != 1 {
		t.Fatalf("account DBID = %d, want %d", acc.DBID, 1)
	}
	if proxyURL != "" {
		t.Fatalf("proxyURL = %q, want empty fallback proxy", proxyURL)
	}
}

func TestNextForSessionFallsBackWhenAPIKeyNotAllowed(t *testing.T) {
	store := &Store{
		accounts: []*Account{
			{DBID: 1, AccessToken: "tok-1"},
			{DBID: 2, AccessToken: "tok-2", AllowedAPIKeyIDs: []int64{2}, allowedAPIKeySet: map[int64]struct{}{2: {}}},
		},
		maxConcurrency: 2,
	}
	store.bindSessionAffinity("session-1", store.accounts[1], "http://proxy-2")

	acc, proxyURL := store.NextForSession("session-1", 1, nil)
	if acc == nil {
		t.Fatal("expected fallback account")
	}
	if acc.DBID != 1 {
		t.Fatalf("account DBID = %d, want %d", acc.DBID, 1)
	}
	if proxyURL != "" {
		t.Fatalf("proxyURL = %q, want empty fallback proxy", proxyURL)
	}
}
