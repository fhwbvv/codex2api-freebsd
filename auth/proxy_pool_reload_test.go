package auth

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codex2api/database"
)

func newProxyPoolReloadTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "codex2api.db"))
	if err != nil {
		t.Fatalf("database.New(sqlite) returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestReloadProxyPoolLoadsCurrentEnabledRows(t *testing.T) {
	db := newProxyPoolReloadTestDB(t)
	ctx := context.Background()
	const proxyURL = "http://proxy.example:8080"
	id, err := db.InsertProxy(ctx, proxyURL, "")
	if err != nil {
		t.Fatalf("InsertProxy returned error: %v", err)
	}

	store := NewStore(db, nil, nil)
	store.SetProxyPoolEnabled(true)
	t.Cleanup(store.Stop)
	if err := store.ReloadProxyPool(); err != nil {
		t.Fatalf("ReloadProxyPool returned error: %v", err)
	}
	if got := store.NextProxy(); got != proxyURL {
		t.Fatalf("NextProxy after reload = %q, want %q", got, proxyURL)
	}
	store.mu.RLock()
	_, indexed := store.proxyPoolSet[proxyURL]
	store.mu.RUnlock()
	if !indexed {
		t.Fatalf("reloaded proxy %q is missing from the membership index", proxyURL)
	}

	if err := db.UpdateProxyTestResult(ctx, id, proxyURL, database.ProxyTestStatusError, "", "", 0); err != nil {
		t.Fatalf("UpdateProxyTestResult(error) returned error: %v", err)
	}
	if err := store.ReloadProxyPool(); err != nil {
		t.Fatalf("ReloadProxyPool after error returned error: %v", err)
	}
	if got := store.NextProxy(); got != "" {
		t.Fatalf("NextProxy after error reload = %q, want empty", got)
	}
}

func TestReloadProxyPoolSerializesSnapshotLoadAndPublish(t *testing.T) {
	const (
		oldURL = "http://old.example:8080"
		newURL = "http://new.example:8080"
	)
	var calls int32
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	allowFirstReturn := make(chan struct{})
	allowSecondReturn := make(chan struct{})

	store := &Store{
		proxyPoolEnabled: true,
		proxyPoolLoader: func(context.Context) ([]*database.ProxyRow, error) {
			switch atomic.AddInt32(&calls, 1) {
			case 1:
				close(firstStarted)
				<-allowFirstReturn
				return []*database.ProxyRow{{URL: oldURL}}, nil
			case 2:
				close(secondStarted)
				<-allowSecondReturn
				return []*database.ProxyRow{{URL: newURL}}, nil
			default:
				t.Fatalf("proxy pool loader called more than twice")
				return nil, nil
			}
		},
	}

	firstDone := make(chan error, 1)
	go func() { firstDone <- store.ReloadProxyPool() }()
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first reload did not start")
	}

	secondDone := make(chan error, 1)
	go func() { secondDone <- store.ReloadProxyPool() }()

	select {
	case <-secondStarted:
		close(allowSecondReturn)
		if err := <-secondDone; err != nil {
			t.Fatalf("second ReloadProxyPool returned error: %v", err)
		}
		close(allowFirstReturn)
		if err := <-firstDone; err != nil {
			t.Fatalf("first ReloadProxyPool returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		close(allowFirstReturn)
		if err := <-firstDone; err != nil {
			t.Fatalf("first ReloadProxyPool returned error: %v", err)
		}
		select {
		case <-secondStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("second reload did not start after first completed")
		}
		close(allowSecondReturn)
		if err := <-secondDone; err != nil {
			t.Fatalf("second ReloadProxyPool returned error: %v", err)
		}
	}

	if got := store.NextProxy(); got != newURL {
		t.Fatalf("NextProxy after overlapping reloads = %q, want newest snapshot %q", got, newURL)
	}
}

func TestRemoveProxyURLsUpdatesPoolMembershipAndLocalAffinity(t *testing.T) {
	const (
		badURL  = "http://bad.example:8080"
		goodURL = "http://good.example:8080"
	)
	store := &Store{
		proxyPoolEnabled: true,
		proxyPool:        []string{badURL, goodURL},
		proxyPoolSet:     buildProxyPoolSet([]string{badURL, goodURL}),
		sessionBindings: map[string]sessionAffinity{
			"bad":  {accountID: 1, proxyURL: badURL},
			"good": {accountID: 2, proxyURL: goodURL},
		},
	}

	store.RemoveProxyURLs([]string{"  " + badURL + "  "})

	store.mu.RLock()
	if len(store.proxyPool) != 1 || store.proxyPool[0] != goodURL {
		t.Fatalf("proxyPool = %v, want only %q", store.proxyPool, goodURL)
	}
	if _, ok := store.proxyPoolSet[badURL]; ok {
		t.Fatalf("removed proxy %q remains in membership index", badURL)
	}
	if _, ok := store.proxyPoolSet[goodURL]; !ok {
		t.Fatalf("retained proxy %q missing from membership index", goodURL)
	}
	store.mu.RUnlock()

	store.sessionMu.RLock()
	_, badBindingExists := store.sessionBindings["bad"]
	_, goodBindingExists := store.sessionBindings["good"]
	store.sessionMu.RUnlock()
	if badBindingExists {
		t.Fatal("local affinity for removed proxy was not cleared")
	}
	if !goodBindingExists {
		t.Fatal("local affinity for retained proxy was cleared")
	}
}

func TestRemoveProxyURLsRunsAfterOlderReloadSnapshotPublishes(t *testing.T) {
	const (
		badURL  = "http://bad.example:8080"
		goodURL = "http://good.example:8080"
	)
	loadStarted := make(chan struct{})
	releaseLoad := make(chan struct{})
	store := &Store{
		proxyPoolEnabled: true,
		proxyPoolLoader: func(context.Context) ([]*database.ProxyRow, error) {
			close(loadStarted)
			<-releaseLoad
			return []*database.ProxyRow{{URL: badURL}, {URL: goodURL}}, nil
		},
	}

	reloadDone := make(chan error, 1)
	go func() { reloadDone <- store.ReloadProxyPool() }()
	select {
	case <-loadStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("proxy pool reload did not start")
	}

	removeDone := make(chan struct{})
	go func() {
		store.RemoveProxyURLs([]string{badURL})
		close(removeDone)
	}()
	select {
	case <-removeDone:
		t.Fatal("RemoveProxyURLs bypassed the in-flight reload")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseLoad)
	if err := <-reloadDone; err != nil {
		t.Fatalf("ReloadProxyPool returned error: %v", err)
	}
	select {
	case <-removeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("RemoveProxyURLs did not finish after reload")
	}

	store.mu.RLock()
	defer store.mu.RUnlock()
	if len(store.proxyPool) != 1 || store.proxyPool[0] != goodURL {
		t.Fatalf("proxyPool = %v, want only %q", store.proxyPool, goodURL)
	}
	if _, ok := store.proxyPoolSet[badURL]; ok {
		t.Fatalf("older reload reintroduced removed proxy %q", badURL)
	}
}

func TestClearAccountProxyURLIfMatchesDoesNotOverwriteConcurrentRebind(t *testing.T) {
	const (
		accountID = int64(42)
		oldURL    = "http://old.example:8080"
		newURL    = "http://new.example:8080"
	)
	account := &Account{DBID: accountID, ProxyURL: oldURL}
	store := &Store{
		accounts:     []*Account{account},
		accountsByID: map[int64]*Account{accountID: account},
	}

	if !store.ClearAccountProxyURLIfMatches(accountID, []string{oldURL}) {
		t.Fatal("expected matching proxy URL to be cleared")
	}
	if got := account.GetProxyURL(); got != "" {
		t.Fatalf("proxy URL after matching clear = %q, want empty", got)
	}

	store.ApplyAccountProxyURL(accountID, newURL)
	if store.ClearAccountProxyURLIfMatches(accountID, []string{oldURL}) {
		t.Fatal("stale clear unexpectedly matched a concurrently rebound proxy URL")
	}
	if got := account.GetProxyURL(); got != newURL {
		t.Fatalf("proxy URL after stale clear = %q, want %q", got, newURL)
	}
}
