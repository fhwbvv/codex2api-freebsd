package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenAIResponsesIdentityChangeClearsPersistedFailureState(t *testing.T) {
	ctx := context.Background()
	db, err := New("sqlite", filepath.Join(t.TempDir(), "responses-identity.db"))
	if err != nil {
		t.Fatalf("New(sqlite): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	accountID, err := db.InsertOpenAIResponsesAccount(ctx, "relay", map[string]interface{}{
		"upstream_type": "openai_responses",
		"base_url":      "https://relay.example",
		"api_key":       "sk-old",
		"models":        []string{"gpt-5.6"},
	}, "")
	if err != nil {
		t.Fatalf("InsertOpenAIResponsesAccount: %v", err)
	}
	if err := db.SetError(ctx, accountID, "old credential rejected"); err != nil {
		t.Fatalf("SetError: %v", err)
	}
	if err := db.SetCooldownWithError(ctx, accountID, "unauthorized", time.Now().Add(time.Hour), "old credential rejected"); err != nil {
		t.Fatalf("SetCooldownWithError: %v", err)
	}
	if err := db.SetModelCooldown(ctx, accountID, "gpt-5.6", "rate_limited", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SetModelCooldown: %v", err)
	}

	before, err := db.GetAccountByID(ctx, accountID)
	if err != nil {
		t.Fatalf("GetAccountByID(before): %v", err)
	}
	if err := db.UpdateOpenAIResponsesAccount(ctx, accountID, "relay", map[string]interface{}{
		"models": []string{"gpt-5.6", "gpt-5.6-mini"},
	}, ""); err != nil {
		t.Fatalf("config-only UpdateOpenAIResponsesAccount: %v", err)
	}
	configOnly, err := db.GetAccountByID(ctx, accountID)
	if err != nil {
		t.Fatalf("GetAccountByID(config-only): %v", err)
	}
	if configOnly.CredentialGeneration != before.CredentialGeneration || configOnly.Status != "error" || !configOnly.CooldownUntil.Valid {
		t.Fatalf("config-only update cleared identity state: generation=%d status=%q cooldown=%v", configOnly.CredentialGeneration, configOnly.Status, configOnly.CooldownUntil.Valid)
	}

	if err := db.UpdateOpenAIResponsesAccount(ctx, accountID, "relay", map[string]interface{}{
		"api_key": "sk-new",
	}, ""); err != nil {
		t.Fatalf("identity UpdateOpenAIResponsesAccount: %v", err)
	}
	after, err := db.GetAccountByID(ctx, accountID)
	if err != nil {
		t.Fatalf("GetAccountByID(after): %v", err)
	}
	if after.CredentialGeneration != before.CredentialGeneration+1 {
		t.Fatalf("credential generation = %d, want %d", after.CredentialGeneration, before.CredentialGeneration+1)
	}
	if after.Status != "active" || after.ErrorMessage != "" || after.CooldownReason != "" || after.CooldownUntil.Valid {
		t.Fatalf("identity failure state was not cleared: status=%q error=%q reason=%q cooldown=%v", after.Status, after.ErrorMessage, after.CooldownReason, after.CooldownUntil.Valid)
	}
	if got := after.GetCredential("api_key"); got != "sk-new" {
		t.Fatalf("api_key = %q, want sk-new", got)
	}
	cooldowns, err := db.ListActiveModelCooldowns(ctx)
	if err != nil {
		t.Fatalf("ListActiveModelCooldowns: %v", err)
	}
	if len(cooldowns) != 0 {
		t.Fatalf("model cooldowns after identity change = %#v, want none", cooldowns)
	}
}
