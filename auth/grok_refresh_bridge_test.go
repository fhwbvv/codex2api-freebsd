package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codex2api/cache"
	"github.com/codex2api/database"
)

type observingGrokLegacyLockCache struct {
	cache.TokenCache
	attempts chan int64
}

type failingGrokLegacyLockCache struct {
	cache.TokenCache
}

type failingLegacyOnlyRefreshCache struct {
	cache.TokenCache
}

func (c *failingGrokLegacyLockCache) SharedAcrossInstances() bool { return true }

func (c *failingGrokLegacyLockCache) AcquireRefreshLock(context.Context, int64, time.Duration) (bool, error) {
	return false, errors.New("legacy lock backend unavailable")
}

func (c *failingLegacyOnlyRefreshCache) SharedAcrossInstances() bool { return true }

func (c *failingLegacyOnlyRefreshCache) AcquireRefreshLock(context.Context, int64, time.Duration) (bool, error) {
	return false, errors.New("legacy-only lock backend unavailable")
}

func (c *observingGrokLegacyLockCache) AcquireRefreshLock(ctx context.Context, accountID int64, ttl time.Duration) (bool, error) {
	select {
	case c.attempts <- accountID:
	default:
	}
	return c.TokenCache.AcquireRefreshLock(ctx, accountID, ttl)
}

func TestGrokRefreshBridgeReloadsLegacyRotationWithoutGenerationAdvance(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "grok-refresh-bridge.db")
	db, err := database.New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer db.Close()

	var providerCalls atomic.Int64
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"unexpected-at","refresh_token":"unexpected-rt","expires_in":3600}`))
	}))
	defer provider.Close()
	providerURL, err := url.Parse(provider.URL)
	if err != nil {
		t.Fatalf("parse provider URL: %v", err)
	}
	t.Setenv(EnvGrokOAuthHostAllowlist, providerURL.Host)

	ctx := context.Background()
	accountID, err := db.InsertAccountWithUpstream(ctx, "grok-legacy-bridge", "xai", UpstreamGrok, map[string]interface{}{
		"upstream_type":       UpstreamGrok,
		"access_token":        "legacy-old-at",
		"refresh_token":       "legacy-old-rt",
		"expires_at":          time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
		"grok_client_id":      "legacy-client",
		"grok_token_endpoint": provider.URL + "/oauth2/token",
		"grok_oidc_issuer":    provider.URL,
		"grok_principal_type": "user",
		"grok_principal_id":   "principal-before",
		"account_id":          "account-before",
		"email":               "before@example.invalid",
	}, "")
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
	before, err := db.GetAccountByID(ctx, accountID)
	if err != nil {
		t.Fatalf("get account before rotation: %v", err)
	}

	baseCache := cache.NewMemory(16)
	observedCache := &observingGrokLegacyLockCache{TokenCache: baseCache, attempts: make(chan int64, 16)}
	store := NewStore(db, observedCache, &database.SystemSettings{MaxConcurrency: 2})
	defer store.Stop()
	if err := store.LoadAccountByID(ctx, accountID); err != nil {
		t.Fatalf("load account: %v", err)
	}

	// Simulate the lock understood by the pre-generation Grok refresher.
	locked, err := baseCache.AcquireRefreshLock(ctx, accountID, time.Minute)
	if err != nil || !locked {
		t.Fatalf("acquire simulated legacy lock = %v, %v", locked, err)
	}
	refreshDone := make(chan error, 1)
	go func() { refreshDone <- store.RefreshGrokAccountByID(ctx, accountID) }()
	select {
	case attemptedID := <-observedCache.attempts:
		if attemptedID != accountID {
			t.Fatalf("bridge attempted account %d, want %d", attemptedID, accountID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("new refresher did not wait on the legacy account-id lock")
	}

	// Write exactly like an old binary: replace credentials without touching the
	// credential_generation column. The new waiter must still detect and reload
	// every identity field after it acquires the bridge lock.
	rotated := make(map[string]interface{}, len(before.Credentials))
	for key, value := range before.Credentials {
		rotated[key] = value
	}
	rotated["access_token"] = "legacy-new-at"
	rotated["refresh_token"] = "legacy-new-rt"
	rotated["expires_at"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	rotated["grok_client_id"] = "legacy-client-rotated"
	rotated["grok_principal_id"] = "principal-after"
	rotated["account_id"] = "account-after"
	rotated["email"] = "after@example.invalid"
	encoded, err := json.Marshal(rotated)
	if err != nil {
		t.Fatalf("marshal legacy credentials: %v", err)
	}
	legacyConn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite connection: %v", err)
	}
	if _, err = legacyConn.ExecContext(ctx, `UPDATE accounts SET credentials=$1,updated_at=CURRENT_TIMESTAMP WHERE id=$2`, encoded, accountID); err != nil {
		_ = legacyConn.Close()
		t.Fatalf("legacy credential write: %v", err)
	}
	if err = legacyConn.Close(); err != nil {
		t.Fatalf("close legacy sqlite connection: %v", err)
	}
	afterLegacyWrite, err := db.GetAccountByID(ctx, accountID)
	if err != nil {
		t.Fatalf("get account after legacy write: %v", err)
	}
	if afterLegacyWrite.CredentialGeneration != before.CredentialGeneration {
		t.Fatalf("legacy write advanced generation from %d to %d", before.CredentialGeneration, afterLegacyWrite.CredentialGeneration)
	}
	if err := baseCache.ReleaseRefreshLock(ctx, accountID); err != nil {
		t.Fatalf("release simulated legacy lock: %v", err)
	}

	select {
	case err := <-refreshDone:
		if err != nil {
			t.Fatalf("bridged refresh: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("bridged refresh did not complete after the legacy lock released")
	}
	if got := providerCalls.Load(); got != 0 {
		t.Fatalf("provider refresh calls = %d, want 0 after reloading legacy rotation", got)
	}

	account := store.FindByID(accountID)
	account.mu.RLock()
	defer account.mu.RUnlock()
	if account.CredentialGeneration != before.CredentialGeneration ||
		account.AccessToken != "legacy-new-at" || account.RefreshToken != "legacy-new-rt" ||
		account.GrokClientID != "legacy-client-rotated" || account.GrokPrincipalID != "principal-after" ||
		account.AccountID != "account-after" || account.Email != "after@example.invalid" {
		t.Fatalf("runtime did not fully reload legacy rotation: generation=%d at=%q rt=%q client=%q principal=%q account=%q email=%q",
			account.CredentialGeneration, account.AccessToken, account.RefreshToken, account.GrokClientID,
			account.GrokPrincipalID, account.AccountID, account.Email)
	}
}

func TestGrokRefreshBridgeAcquiresFamilyBeforeLegacyLock(t *testing.T) {
	sharedCache := cache.NewMemory(16)
	observedCache := &observingGrokLegacyLockCache{TokenCache: sharedCache, attempts: make(chan int64, 16)}
	storeA := NewStore(nil, observedCache, &database.SystemSettings{MaxConcurrency: 2})
	storeB := NewStore(nil, observedCache, &database.SystemSettings{MaxConcurrency: 2})
	defer storeA.Stop()
	defer storeB.Stop()

	ctx := context.Background()
	first, err := storeA.acquireGrokOAuthRefreshLocks(ctx, 41, "stable-family")
	if err != nil {
		t.Fatalf("store A acquire bridge locks: %v", err)
	}
	// Drain store A's observed account-lock attempt.
	select {
	case <-observedCache.attempts:
	default:
		t.Fatal("store A did not acquire the legacy bridge lock first")
	}

	acquired := make(chan *grokOAuthRefreshLocks, 1)
	errs := make(chan error, 1)
	go func() {
		locks, acquireErr := storeB.acquireGrokOAuthRefreshLocks(ctx, 41, "stable-family")
		if acquireErr != nil {
			errs <- acquireErr
			return
		}
		acquired <- locks
	}()
	// Store B must remain at the family lease while store A owns it. Reaching
	// the legacy account lock here would prove the locks were taken in reverse
	// order and could deadlock with a family-first deployment.
	select {
	case attemptedID := <-observedCache.attempts:
		first.Release()
		t.Fatalf("store B attempted legacy account lock %d before acquiring family lease", attemptedID)
	case locks := <-acquired:
		locks.Release()
		first.Release()
		t.Fatal("store B bypassed store A's family lease")
	case err := <-errs:
		first.Release()
		t.Fatalf("store B failed while waiting: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	first.Release()
	select {
	case attemptedID := <-observedCache.attempts:
		if attemptedID != 41 {
			t.Fatalf("store B attempted account %d, want 41", attemptedID)
		}
	case err := <-errs:
		t.Fatalf("store B acquire after release: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("store B did not reach legacy lock after family lease released")
	}
	select {
	case second := <-acquired:
		second.Release()
	case err := <-errs:
		t.Fatalf("store B acquire after release: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("store B did not acquire bridge/family locks after store A released")
	}
}

func TestGrokRefreshBridgeReleasesFamilyLeaseWhenLegacyLockFails(t *testing.T) {
	baseCache := cache.NewMemory(16)
	sharedCache := &failingGrokLegacyLockCache{TokenCache: baseCache}
	store := NewStore(nil, sharedCache, &database.SystemSettings{MaxConcurrency: 2})
	defer store.Stop()

	ctx := context.Background()
	locks, err := store.acquireGrokOAuthRefreshLocks(ctx, 73, "stable-family")
	if err == nil || locks != nil {
		t.Fatalf("legacy lock failure = locks:%v err:%v, want nil/error", locks, err)
	}

	// Family is acquired first. A failure to obtain the legacy bridge lock must
	// release it immediately, otherwise every current instance in that token
	// family remains wedged until the distributed TTL expires.
	second, reacquireErr := store.acquireOAuthRefreshFamilyLease(ctx, "stable-family")
	if reacquireErr != nil || second == nil {
		t.Fatalf("family lease after legacy failure = %v, %v; want released", second, reacquireErr)
	}
	second.Release()

	store.oauthRefreshLocksMu.Lock()
	defer store.oauthRefreshLocksMu.Unlock()
	if len(store.oauthRefreshLocks) != 0 {
		t.Fatalf("family failure retained %d local refresh locks", len(store.oauthRefreshLocks))
	}
}

func TestGrokLegacyRefreshFallbackFailsClosedForSharedCache(t *testing.T) {
	baseCache := cache.NewMemory(16)
	sharedCache := &failingLegacyOnlyRefreshCache{TokenCache: baseCache}
	store := NewStore(nil, sharedCache, &database.SystemSettings{MaxConcurrency: 2})
	defer store.Stop()

	account := &Account{
		DBID: 91, UpstreamType: UpstreamGrok, RefreshToken: "rt",
		GrokTokenEndpoint:    "https://auth.x.ai/oauth2/token",
		CredentialGeneration: 1,
	}
	err := store.refreshGrokAccount(context.Background(), account, true)
	if err == nil || !strings.Contains(err.Error(), "跨实例刷新锁失败") {
		t.Fatalf("shared legacy fallback error = %v, want fail-closed lock error", err)
	}
}
