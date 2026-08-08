package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestPromptConversationLockLifecycleAndDecisionReplay(t *testing.T) {
	db := newPromptPolicySQLiteTestDB(t)
	ctx := context.Background()
	input := PromptConversationLockInput{
		LockKey:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Platform: "newapi", NewAPIUserID: "42",
		SessionFingerprint: "0123456789abcdef0123456789abcdef",
		SessionHash:        "session-hash", IncidentID: "incident-1", DecisionID: "decision-1",
		RequestID: "request-1", ReasonCode: "upstream_cyber_policy",
		Endpoint: "/v1/responses", Model: "gpt-5.6-sol", LockedAt: time.Now().UTC(),
	}

	locked, changed, err := db.LockPromptConversation(ctx, input)
	if err != nil || !changed || locked.Status != PromptConversationLockStatusActive || locked.TriggerCount != 1 {
		t.Fatalf("first lock = %#v changed=%t err=%v", locked, changed, err)
	}
	replay, changed, err := db.LockPromptConversation(ctx, input)
	if err != nil || changed || replay.TriggerCount != 1 {
		t.Fatalf("decision replay = %#v changed=%t err=%v", replay, changed, err)
	}

	unlocked, err := db.UnlockPromptConversation(ctx, input.LockKey, "confirmed false positive")
	if err != nil || unlocked.Status != PromptConversationLockStatusUnlocked || unlocked.UnlockCount != 1 || unlocked.UnlockedAt == nil {
		t.Fatalf("unlock = %#v err=%v", unlocked, err)
	}
	if _, err := db.GetActivePromptConversationLock(ctx, input.LockKey); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("active lookup after unlock err=%v, want sql.ErrNoRows", err)
	}

	// Replaying the original decision after a manual unlock must stay unlocked.
	replay, changed, err = db.LockPromptConversation(ctx, input)
	if err != nil || changed || replay.Status != PromptConversationLockStatusUnlocked {
		t.Fatalf("post-unlock replay = %#v changed=%t err=%v", replay, changed, err)
	}
	input.DecisionID = "decision-2"
	input.IncidentID = "incident-2"
	relocked, changed, err := db.LockPromptConversation(ctx, input)
	if err != nil || !changed || relocked.Status != PromptConversationLockStatusActive || relocked.TriggerCount != 2 {
		t.Fatalf("new CYB relock = %#v changed=%t err=%v", relocked, changed, err)
	}
}
