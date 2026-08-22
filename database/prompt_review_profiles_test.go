package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPromptReviewProfilesPersistMetadataAndSecretsServerSide(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatalf("New sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	profile := PromptReviewProfile{
		ID: "profile-openai", Name: "OpenAI moderation", BaseURL: "https://api.openai.com",
		Model: "omni-moderation-latest", RequestMode: "moderations", AdapterJSON: `{"request_mode":"moderations"}`,
		APIKeys: "key-a\nkey-b", TimeoutSecond: 15,
	}
	if err := db.UpsertPromptReviewProfile(ctx, profile); err != nil {
		t.Fatalf("UpsertPromptReviewProfile: %v", err)
	}
	got, err := db.GetPromptReviewProfile(ctx, profile.ID)
	if err != nil || got == nil {
		t.Fatalf("GetPromptReviewProfile: got=%+v err=%v", got, err)
	}
	if got.APIKeys != profile.APIKeys || got.BaseURL != profile.BaseURL {
		t.Fatalf("profile round trip lost fields: got=%+v", got)
	}
	if err := db.SetPromptReviewProfileActive(ctx, profile.ID); err != nil {
		t.Fatalf("SetPromptReviewProfileActive: %v", err)
	}
	if err := db.SetPromptReviewProfileActive(ctx, "missing-profile"); err == nil {
		t.Fatal("SetPromptReviewProfileActive should reject a missing profile")
	}
	items, err := db.ListPromptReviewProfiles(ctx)
	if err != nil || len(items) != 1 || !items[0].Active {
		t.Fatalf("failed activation should preserve the active profile: items=%+v err=%v", items, err)
	}
	if err := db.DeletePromptReviewProfile(ctx, profile.ID); err != nil {
		t.Fatalf("DeletePromptReviewProfile: %v", err)
	}
	deleted, err := db.GetPromptReviewProfile(ctx, profile.ID)
	if err != nil || deleted != nil {
		t.Fatalf("profile still exists after delete: profile=%+v err=%v", deleted, err)
	}
}
