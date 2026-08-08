package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/codex2api/database"
)

func TestStoreInitLoadsAndHotReplacesPromptFilterNewAPIBinding(t *testing.T) {
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "store-binding.sqlite"))
	if err != nil {
		t.Fatalf("New sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	apiKeyID, err := db.InsertAPIKey(ctx, "gateway-a", "sk-gateway-a-store-binding")
	if err != nil {
		t.Fatalf("InsertAPIKey: %v", err)
	}
	binding := &database.PromptFilterNewAPIBinding{
		APIKeyID: apiKeyID, PlatformCode: "gateway-a", PlatformName: "示例平台", Secret: "01234567890123456789012345678901",
		Enabled: true, RequireSignedIdentity: true, PolicyMode: "enforce", PolicyProfile: "balanced",
	}
	if err := db.CreatePromptFilterNewAPIBinding(ctx, binding); err != nil {
		t.Fatalf("Create binding: %v", err)
	}
	store := NewStore(db, nil, nil)
	defer store.Stop()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Store.Init: %v", err)
	}
	got, ok := store.GetPromptFilterNewAPIBinding(apiKeyID)
	if !ok || got.Secret != binding.Secret || got.PlatformCode != "gateway-a" {
		t.Fatalf("runtime binding = %#v ok=%v", got, ok)
	}
	if !store.HasPromptFilterNewAPIBindings() {
		t.Fatal("runtime did not report active per-key binding mode")
	}
	got.Secret = "mutated-copy"
	again, _ := store.GetPromptFilterNewAPIBinding(apiKeyID)
	if again.Secret != binding.Secret {
		t.Fatalf("getter leaked mutable state: %q", again.Secret)
	}
	binding.PlatformCode = "gateway-a-prod"
	if err := db.UpdatePromptFilterNewAPIBinding(ctx, binding); err != nil {
		t.Fatalf("Update binding: %v", err)
	}
	if err := store.LoadPromptFilterNewAPIBindings(ctx); err != nil {
		t.Fatalf("hot reload: %v", err)
	}
	updated, ok := store.GetPromptFilterNewAPIBinding(apiKeyID)
	if !ok || updated.PlatformCode != "gateway-a-prod" {
		t.Fatalf("updated runtime binding = %#v ok=%v", updated, ok)
	}
	expiresAt := time.Now().UTC().Add(time.Minute)
	direct := *binding
	direct.PlatformCode = "gateway-a-direct"
	direct.PreviousSecretExpiresAt = &expiresAt
	store.UpsertPromptFilterNewAPIBinding(direct)
	expiresAt = expiresAt.Add(time.Hour)
	direct.PreviousSecretExpiresAt = nil
	direct.Secret = "mutated-after-upsert"
	directGot, ok := store.GetPromptFilterNewAPIBinding(apiKeyID)
	if !ok || directGot.PlatformCode != "gateway-a-direct" || directGot.Secret != binding.Secret || directGot.PreviousSecretExpiresAt == nil || !directGot.PreviousSecretExpiresAt.Before(expiresAt) {
		t.Fatalf("direct runtime upsert did not publish an immutable copy: %#v ok=%v", directGot, ok)
	}
	store.RemovePromptFilterNewAPIBinding(apiKeyID)
	if _, ok := store.GetPromptFilterNewAPIBinding(apiKeyID); ok || store.HasPromptFilterNewAPIBindings() {
		t.Fatal("direct runtime remove left the binding active")
	}
	store.ReplacePromptFilterNewAPIBindings(nil)
	if store.HasPromptFilterNewAPIBindings() {
		t.Fatal("empty runtime snapshot still reported per-key binding mode")
	}
}
