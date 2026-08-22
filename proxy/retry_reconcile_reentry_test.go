package proxy

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
)

func newReconcileReentryStore(t *testing.T, dbName string) (*auth.Store, *database.DB) {
	t.Helper()
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), dbName))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := auth.NewStore(db, nil, &database.SystemSettings{
		MaxConcurrency:       1,
		FastSchedulerEnabled: true,
	})
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("Store.Init: %v", err)
	}
	return store, db
}

// A scheduler miss must ride the shared background reconciliation back into
// the full selection loop, so an account that only exists in the database
// (added by another process) is picked up by the missed request itself.
func TestNextRetryAccountReentersLoopAfterReconcile(t *testing.T) {
	ctx := context.Background()
	store, db := newReconcileReentryStore(t, "retry-reconcile-reentry.db")

	accountID, err := db.InsertOpenAIResponsesAccount(ctx, "reentry-endpoint", map[string]interface{}{
		"upstream_type": auth.UpstreamOpenAIResponses,
		"base_url":      "https://healthy.example",
		"api_key":       "sk-reentry",
		"models":        []string{"gpt-5.6"},
	}, "")
	if err != nil {
		t.Fatalf("InsertOpenAIResponsesAccount: %v", err)
	}

	handler := &Handler{store: store}
	deadlineCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	account, _ := handler.nextRetryAccountForSession(deadlineCtx, "reentry-session", 0, newRetryAccountExclusions(), nil)
	if account == nil {
		t.Fatal("nextRetryAccount returned nil; expected the reconcile re-entry to pick up the database-only account")
	}
	defer store.Release(account)
	if account.ID() != accountID {
		t.Fatalf("nextRetryAccount = account %d, want database-only account %d", account.ID(), accountID)
	}
}

// A canceled request must exit the retry loop promptly instead of running
// another scheduler round or waiting out the reconcile grace period.
func TestNextRetryAccountReturnsPromptlyOnCanceledContext(t *testing.T) {
	store, _ := newReconcileReentryStore(t, "retry-reconcile-canceled.db")

	handler := &Handler{store: store}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	account, _ := handler.nextRetryAccountForSession(canceledCtx, "canceled-session", 0, newRetryAccountExclusions(), nil)
	if account != nil {
		store.Release(account)
		t.Fatal("nextRetryAccount returned an account for a canceled context")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("nextRetryAccount took %s on a canceled context, want a prompt return", elapsed)
	}
}
