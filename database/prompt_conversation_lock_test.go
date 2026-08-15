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

func TestPromptConversationLockExpiresAfterTTL(t *testing.T) {
	db := newPromptPolicySQLiteTestDB(t)
	ctx := context.Background()
	input := PromptConversationLockInput{
		LockKey:  "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		Platform: "newapi", NewAPIUserID: "42",
		SessionFingerprint: "abcdef0123456789abcdef0123456789", SessionHash: "expired-session",
		IncidentID: "incident-expired", DecisionID: "decision-expired", ReasonCode: "upstream_cyber_policy",
		LockedAt: time.Now().UTC().Add(-2 * time.Hour),
	}
	if _, _, err := db.LockPromptConversation(ctx, input); err != nil {
		t.Fatalf("LockPromptConversation: %v", err)
	}
	if _, err := db.GetActivePromptConversationLockWithTTL(ctx, input.LockKey, time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expired lock lookup err=%v, want sql.ErrNoRows", err)
	}
	stored, err := db.GetPromptConversationLock(ctx, input.LockKey)
	if err != nil || stored.Status != PromptConversationLockStatusUnlocked || stored.UnlockReason != "automatic expiry after conversation-lock TTL" {
		t.Fatalf("expired stored lock = %#v err=%v", stored, err)
	}
}

func TestPromptConversationRestrictionUsesBoundedUserCooldownAcrossSessions(t *testing.T) {
	db := newPromptPolicySQLiteTestDB(t)
	ctx := context.Background()
	input := PromptConversationLockInput{
		LockKey:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Platform: "newapi", NewAPIUserID: "42",
		SessionFingerprint: "0123456789abcdef0123456789abcdef", SessionHash: "session-a",
		IncidentID: "incident-cooldown", DecisionID: "decision-cooldown", ReasonCode: "upstream_cyber_policy",
		LockedAt: time.Now().UTC(),
	}
	if _, _, err := db.LockPromptConversation(ctx, input); err != nil {
		t.Fatalf("LockPromptConversation: %v", err)
	}

	otherLockKey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	item, exact, err := db.GetActivePromptConversationRestriction(ctx, otherLockKey, "newapi", "42", 24*time.Hour, 30*time.Minute)
	if err != nil || exact || item.DecisionID != input.DecisionID {
		t.Fatalf("user cooldown restriction = %#v exact=%t err=%v", item, exact, err)
	}

	if _, _, err := db.GetActivePromptConversationRestriction(ctx, otherLockKey, "newapi", "43", 24*time.Hour, 30*time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("different user inherited cooldown: %v", err)
	}
	if _, _, err := db.GetActivePromptConversationRestriction(ctx, otherLockKey, "other-platform", "42", 24*time.Hour, 30*time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("different platform inherited cooldown: %v", err)
	}

	if _, _, err := db.GetActivePromptConversationRestriction(ctx, otherLockKey, "newapi", "42", 24*time.Hour, -time.Second); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("disabled cooldown returned restriction: %v", err)
	}
}

func TestPromptConversationRestrictionSupportsSessionlessUserCooldown(t *testing.T) {
	db := newPromptPolicySQLiteTestDB(t)
	ctx := context.Background()
	input := PromptConversationLockInput{
		LockKey:  "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
		Platform: "newapi", NewAPIUserID: "42",
		IncidentID: "incident-sessionless", DecisionID: "decision-sessionless", ReasonCode: "upstream_cyber_policy",
		LockedAt: time.Now().UTC(),
	}
	stored, changed, err := db.LockPromptConversation(ctx, input)
	if err != nil || !changed || stored.SessionFingerprint != "" || stored.SessionHash != "" {
		t.Fatalf("sessionless cooldown lock = %#v changed=%t err=%v", stored, changed, err)
	}

	item, exact, err := db.GetActivePromptConversationRestriction(ctx, "", "newapi", "42", 24*time.Hour, 30*time.Minute)
	if err != nil || exact || item.DecisionID != input.DecisionID {
		t.Fatalf("sessionless user cooldown restriction = %#v exact=%t err=%v", item, exact, err)
	}
	otherSessionKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	item, exact, err = db.GetActivePromptConversationRestriction(ctx, otherSessionKey, "newapi", "42", 24*time.Hour, 30*time.Minute)
	if err != nil || exact || item.DecisionID != input.DecisionID {
		t.Fatalf("sessionless cooldown did not cover later session = %#v exact=%t err=%v", item, exact, err)
	}
}

func TestPromptConversationLockCreatesUserCooldownIndex(t *testing.T) {
	db := newPromptPolicySQLiteTestDB(t)
	if err := db.ensurePromptConversationLocksTable(t.Context()); err != nil {
		t.Fatal(err)
	}
	var name string
	err := db.conn.QueryRowContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='index' AND name=$1`, "idx_prompt_conversation_locks_user_cooldown").Scan(&name)
	if err != nil || name != "idx_prompt_conversation_locks_user_cooldown" {
		t.Fatalf("user cooldown index = %q err=%v", name, err)
	}
}

func TestUnlockPromptConversationUserCooldownClearsEveryActiveLockForVerifiedUser(t *testing.T) {
	db := newPromptPolicySQLiteTestDB(t)
	ctx := t.Context()
	locks := []PromptConversationLockInput{
		{
			LockKey:  "1111111111111111111111111111111111111111111111111111111111111111",
			Platform: "newapi", NewAPIUserID: "42", SessionFingerprint: "11111111111111111111111111111111",
			SessionHash: "session-a", DecisionID: "decision-a", ReasonCode: "upstream_cyber_policy",
		},
		{
			LockKey:  "2222222222222222222222222222222222222222222222222222222222222222",
			Platform: "newapi", NewAPIUserID: "42", SessionFingerprint: "22222222222222222222222222222222",
			SessionHash: "session-b", DecisionID: "decision-b", ReasonCode: "upstream_cyber_policy",
		},
		{
			LockKey:  "3333333333333333333333333333333333333333333333333333333333333333",
			Platform: "newapi", NewAPIUserID: "43", SessionFingerprint: "33333333333333333333333333333333",
			SessionHash: "session-c", DecisionID: "decision-c", ReasonCode: "upstream_cyber_policy",
		},
	}
	for _, input := range locks {
		if _, _, err := db.LockPromptConversation(ctx, input); err != nil {
			t.Fatalf("LockPromptConversation(%s): %v", input.DecisionID, err)
		}
	}

	keys, err := db.UnlockPromptConversationUserCooldown(ctx, "newapi", "42", "confirmed false positive")
	if err != nil || len(keys) != 2 {
		t.Fatalf("user cooldown unlock keys=%v err=%v", keys, err)
	}
	for _, input := range locks[:2] {
		if _, err := db.GetActivePromptConversationLock(ctx, input.LockKey); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("same-user lock %s remained active: %v", input.LockKey, err)
		}
		stored, err := db.GetPromptConversationLock(ctx, input.LockKey)
		if err != nil || stored.UnlockReason != "confirmed false positive" || stored.UnlockCount != 1 {
			t.Fatalf("unlocked row=%#v err=%v", stored, err)
		}
	}
	if _, err := db.GetActivePromptConversationLock(ctx, locks[2].LockKey); err != nil {
		t.Fatalf("different user lock was cleared: %v", err)
	}
}
