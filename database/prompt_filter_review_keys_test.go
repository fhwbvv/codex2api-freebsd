package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCompareAndSwapPromptFilterReviewAPIKeysRejectsStaleSnapshot(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "review-keys.db"))
	if err != nil {
		t.Fatalf("New(sqlite): %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.UpdateSystemSettings(ctx, &SystemSettings{PromptFilterReviewAPIKey: "key-one\nkey-two"}); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	swapped, err := db.CompareAndSwapPromptFilterReviewAPIKeys(ctx, "key-one\nkey-two", "key-two")
	if err != nil || !swapped {
		t.Fatalf("first swap = %t, %v; want true, nil", swapped, err)
	}
	swapped, err = db.CompareAndSwapPromptFilterReviewAPIKeys(ctx, "key-one\nkey-two", "key-one")
	if err != nil || swapped {
		t.Fatalf("stale swap = %t, %v; want false, nil", swapped, err)
	}
	settings, err := db.GetSystemSettings(ctx)
	if err != nil || settings.PromptFilterReviewAPIKey != "key-two" {
		t.Fatalf("stored keys = %q, %v; want key-two", settings.PromptFilterReviewAPIKey, err)
	}
}

// SQL TRIM 只去空格,存量值带换行/制表符时曾导致 CAS 永远匹配不上;
// 现按原值精确比较,调用方传入的 expected 必须是未 trim 的存储原值。
func TestCompareAndSwapPromptFilterReviewAPIKeysMatchesPaddedStoredValue(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "review-keys.db"))
	if err != nil {
		t.Fatalf("New(sqlite): %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	stored := "\tkey-one\nkey-two\n"
	if err := db.UpdateSystemSettings(ctx, &SystemSettings{PromptFilterReviewAPIKey: stored}); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	swapped, err := db.CompareAndSwapPromptFilterReviewAPIKeys(ctx, stored, "key-two")
	if err != nil || !swapped {
		t.Fatalf("padded swap = %t, %v; want true, nil", swapped, err)
	}
	settings, err := db.GetSystemSettings(ctx)
	if err != nil || settings.PromptFilterReviewAPIKey != "key-two" {
		t.Fatalf("stored keys = %q, %v; want key-two", settings.PromptFilterReviewAPIKey, err)
	}
}

// 设置保存未携带审查 Key 字段时必须保留数据库现值,防止别的实例用
// 过期内存快照把已删除的 Key 写回(PreservePromptFilterReviewAPIKey)。
func TestUpdateSystemSettingsPreservesReviewAPIKeyWhenFlagged(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "review-keys.db"))
	if err != nil {
		t.Fatalf("New(sqlite): %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.UpdateSystemSettings(ctx, &SystemSettings{PromptFilterReviewAPIKey: "key-two"}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	stale := &SystemSettings{PromptFilterReviewAPIKey: "key-one\nkey-two", PreservePromptFilterReviewAPIKey: true}
	if err := db.UpdateSystemSettings(ctx, stale); err != nil {
		t.Fatalf("preserving update: %v", err)
	}
	settings, err := db.GetSystemSettings(ctx)
	if err != nil || settings.PromptFilterReviewAPIKey != "key-two" {
		t.Fatalf("preserved keys = %q, %v; want key-two", settings.PromptFilterReviewAPIKey, err)
	}
	overwrite := &SystemSettings{PromptFilterReviewAPIKey: "key-three"}
	if err := db.UpdateSystemSettings(ctx, overwrite); err != nil {
		t.Fatalf("overwriting update: %v", err)
	}
	settings, err = db.GetSystemSettings(ctx)
	if err != nil || settings.PromptFilterReviewAPIKey != "key-three" {
		t.Fatalf("overwritten keys = %q, %v; want key-three", settings.PromptFilterReviewAPIKey, err)
	}
}
