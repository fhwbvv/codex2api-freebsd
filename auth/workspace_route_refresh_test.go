package auth

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/codex2api/cache"
	"github.com/codex2api/database"
)

type failingSharedLeaseCache struct {
	cache.TokenCache
}

func (f failingSharedLeaseCache) SharedAcrossInstances() bool { return true }

func (f failingSharedLeaseCache) AcquireLease(context.Context, string, string, string, time.Duration) (bool, error) {
	return false, errors.New("shared lease unavailable")
}

func TestPropagateSharedOAuthCredentialsPreservesWorkspaceRoutes(t *testing.T) {
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "codex2api.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	personalID, err := db.InsertAccountWithCredentials(ctx, "personal", map[string]interface{}{
		"refresh_token": "shared-old-rt",
		"session_token": "shared-old-st",
		"access_token":  "old-at",
		"email":         "user@example.com",
		"workspace_id":  "personal-workspace",
	}, "")
	if err != nil {
		t.Fatalf("insert personal: %v", err)
	}
	teamID, err := db.InsertAccountWithCredentials(ctx, "team", map[string]interface{}{
		"refresh_token": "shared-old-rt",
		"session_token": "shared-old-st",
		"access_token":  "old-at",
		"email":         "user@example.com",
		"workspace_id":  "personal-workspace",
		"custom_headers": map[string]string{
			"Chatgpt-Account-Id": "team-workspace",
		},
	}, "")
	if err != nil {
		t.Fatalf("insert team: %v", err)
	}

	tokenCache := cache.NewMemory(16)
	store := NewStore(db, tokenCache, &database.SystemSettings{MaxConcurrency: 2})
	defer store.Stop()
	if err := store.LoadAccountByID(ctx, personalID); err != nil {
		t.Fatalf("load personal: %v", err)
	}
	if err := store.LoadAccountByID(ctx, teamID); err != nil {
		t.Fatalf("load team: %v", err)
	}

	source := store.FindByID(personalID)
	source.mu.Lock()
	source.RefreshToken = "shared-new-rt"
	source.AccessToken = "new-at"
	source.ExpiresAt = time.Now().Add(time.Hour)
	source.AccountID = "personal-workspace"
	source.Email = "user@example.com"
	source.PlanType = "team"
	source.mu.Unlock()

	td := &TokenData{
		RefreshToken: "shared-new-rt",
		AccessToken:  "new-at",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	credentials := map[string]interface{}{
		"refresh_token": "shared-new-rt",
		"session_token": "shared-new-st",
		"access_token":  "new-at",
		"expires_at":    td.ExpiresAt.Format(time.RFC3339),
		"email":         "user@example.com",
		"workspace_id":  "personal-workspace",
		"plan_type":     "team",
	}
	store.propagateSharedOAuthCredentials(ctx, source, "shared-old-rt", td, credentials, 55*time.Minute)

	team := store.FindByID(teamID)
	team.mu.RLock()
	gotRefreshToken := team.RefreshToken
	gotAccessToken := team.AccessToken
	gotSessionToken := team.SessionToken
	gotWorkspaceRoute := team.CustomHeaders["Chatgpt-Account-Id"]
	team.mu.RUnlock()
	if gotRefreshToken != "shared-new-rt" || gotAccessToken != "new-at" {
		t.Fatalf("team runtime tokens = rt:%q at:%q", gotRefreshToken, gotAccessToken)
	}
	if gotSessionToken != "shared-new-st" {
		t.Fatalf("team runtime session token = %q, want shared-new-st", gotSessionToken)
	}
	if gotWorkspaceRoute != "team-workspace" {
		t.Fatalf("team runtime route = %q, want team-workspace", gotWorkspaceRoute)
	}

	teamRow, err := db.GetAccountByID(ctx, teamID)
	if err != nil {
		t.Fatalf("get team row: %v", err)
	}
	if got := teamRow.GetCredential("refresh_token"); got != "shared-new-rt" {
		t.Fatalf("team stored refresh_token = %q", got)
	}
	if got := teamRow.GetCredential("session_token"); got != "shared-new-st" {
		t.Fatalf("team stored session_token = %q", got)
	}
	if got := teamRow.GetCredentialStringMap("custom_headers")["Chatgpt-Account-Id"]; got != "team-workspace" {
		t.Fatalf("team stored route = %q, want team-workspace", got)
	}
	if got, err := tokenCache.GetAccessToken(ctx, teamID); err != nil || got != "new-at" {
		t.Fatalf("team cached access token = %q err=%v", got, err)
	}
}

func TestOAuthRefreshLocalLockHonorsContext(t *testing.T) {
	store := NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 2})
	defer store.Stop()

	firstLease, err := store.acquireOAuthRefreshLease(context.Background(), "shared-rt")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := store.acquireOAuthRefreshLease(ctx, "shared-rt"); err == nil {
		t.Fatal("second acquire should stop when its context expires")
	}
	firstLease.Release()

	store.oauthRefreshLocksMu.Lock()
	defer store.oauthRefreshLocksMu.Unlock()
	if len(store.oauthRefreshLocks) != 0 {
		t.Fatalf("retained %d OAuth refresh locks after cancellation and release", len(store.oauthRefreshLocks))
	}
}

func TestOAuthRefreshLeaseHoldDeadlinePrecedesTTL(t *testing.T) {
	store := NewStore(nil, cache.NewMemory(1), &database.SystemSettings{MaxConcurrency: 2})
	defer store.Stop()

	lease, err := store.acquireOAuthRefreshLease(context.Background(), "shared-rt")
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	defer lease.Release()

	deadline, ok := lease.Context().Deadline()
	if !ok {
		t.Fatal("lease context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining >= oauthRefreshLeaseTTL {
		t.Fatalf("lease hold deadline remaining = %v, want between zero and TTL %v", remaining, oauthRefreshLeaseTTL)
	}
}

func TestOAuthRefreshLeaseFailsClosedWhenSharedCacheIsUnavailable(t *testing.T) {
	base := cache.NewMemory(1)
	store := NewStore(nil, failingSharedLeaseCache{TokenCache: base}, &database.SystemSettings{MaxConcurrency: 2})
	defer store.Stop()

	if lease, err := store.acquireOAuthRefreshLease(context.Background(), "shared-rt"); err == nil || lease != nil {
		t.Fatalf("shared lease failure must fail closed, lease=%v err=%v", lease, err)
	}
	store.oauthRefreshLocksMu.Lock()
	defer store.oauthRefreshLocksMu.Unlock()
	if len(store.oauthRefreshLocks) != 0 {
		t.Fatalf("failed shared lease retained %d local locks", len(store.oauthRefreshLocks))
	}
}

func TestOAuthRefreshLeaseSerializesStoresAndReloadsRotatedCredentials(t *testing.T) {
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "codex2api.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	accountID, err := db.InsertAccountWithCredentials(ctx, "team", map[string]interface{}{
		"refresh_token": "shared-old-rt",
		"session_token": "shared-old-st",
		"access_token":  "old-at",
		"expires_at":    time.Now().Add(time.Minute).Format(time.RFC3339),
	}, "")
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}

	sharedCache := cache.NewMemory(16)
	storeA := NewStore(db, sharedCache, &database.SystemSettings{MaxConcurrency: 2})
	storeB := NewStore(db, sharedCache, &database.SystemSettings{MaxConcurrency: 2})
	defer storeA.Stop()
	defer storeB.Stop()
	if err := storeB.LoadAccountByID(ctx, accountID); err != nil {
		t.Fatalf("load account in store B: %v", err)
	}

	firstLease, err := storeA.acquireOAuthRefreshLease(ctx, "shared-old-rt")
	if err != nil {
		t.Fatalf("store A acquire lease: %v", err)
	}

	var wg sync.WaitGroup
	acquired := make(chan *oauthRefreshLease, 1)
	errs := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		lease, acquireErr := storeB.acquireOAuthRefreshLease(ctx, "shared-old-rt")
		if acquireErr != nil {
			errs <- acquireErr
			return
		}
		acquired <- lease
	}()

	select {
	case lease := <-acquired:
		lease.Release()
		t.Fatal("store B acquired the shared RT lease before store A released it")
	case err := <-errs:
		t.Fatalf("store B acquire lease: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	expiresAt := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := db.UpdateCredentials(ctx, accountID, map[string]interface{}{
		"refresh_token": "shared-new-rt",
		"session_token": "shared-new-st",
		"access_token":  "new-at",
		"expires_at":    expiresAt.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("rotate stored credentials: %v", err)
	}
	firstLease.Release()

	var secondLease *oauthRefreshLease
	select {
	case secondLease = <-acquired:
	case err := <-errs:
		t.Fatalf("store B acquire lease after release: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("store B did not acquire the shared RT lease after release")
	}
	defer secondLease.Release()
	wg.Wait()

	account := storeB.FindByID(accountID)
	changed, usable, err := storeB.reloadOAuthCredentialsAfterLock(ctx, account, "shared-old-rt", "old-at")
	if err != nil {
		t.Fatalf("reload credentials: %v", err)
	}
	if !changed || !usable {
		t.Fatalf("reload result = changed:%t usable:%t, want true/true", changed, usable)
	}
	account.mu.RLock()
	defer account.mu.RUnlock()
	if account.RefreshToken != "shared-new-rt" ||
		account.SessionToken != "shared-new-st" ||
		account.AccessToken != "new-at" {
		t.Fatalf(
			"reloaded runtime credentials = rt:%q st:%q at:%q",
			account.RefreshToken,
			account.SessionToken,
			account.AccessToken,
		)
	}

	secondLease.Release()
	storeB.oauthRefreshLocksMu.Lock()
	defer storeB.oauthRefreshLocksMu.Unlock()
	if len(storeB.oauthRefreshLocks) != 0 {
		t.Fatalf("store B retained %d OAuth refresh locks after release", len(storeB.oauthRefreshLocks))
	}
}
