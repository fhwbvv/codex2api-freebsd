package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/database"
	"github.com/gin-gonic/gin"
)

func TestAdminCanUnlockPromptConversationAndInvalidateCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "admin-conversation-lock.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	lockKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	item, _, err := db.LockPromptConversation(t.Context(), database.PromptConversationLockInput{
		LockKey: lockKey, Platform: "newapi", NewAPIUserID: "42",
		SessionFingerprint: "0123456789abcdef0123456789abcdef", SessionHash: "session-hash",
		DecisionID: "decision-1", ReasonCode: "upstream_cyber_policy",
	})
	if err != nil {
		t.Fatalf("LockPromptConversation: %v", err)
	}
	tokenCache := cache.NewMemory(1)
	raw, _ := json.Marshal(item)
	if err := tokenCache.SetRuntime(t.Context(), database.PromptConversationLockCacheNamespace, lockKey, raw, time.Minute); err != nil {
		t.Fatalf("SetRuntime: %v", err)
	}
	store := auth.NewStore(db, tokenCache, &database.SystemSettings{MaxConcurrency: 2, TestConcurrency: 1})
	handler := NewHandler(store, db, tokenCache, nil, "")
	router := gin.New()
	router.POST("/api/admin/prompt-policy/conversation-locks/:lock_key/unlock", handler.UnlockPromptConversation)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/prompt-policy/conversation-locks/"+lockKey+"/unlock", bytes.NewBufferString(`{"reason":"confirmed false positive"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unlock status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stored, err := db.GetPromptConversationLock(t.Context(), lockKey)
	if err != nil || stored.Status != database.PromptConversationLockStatusUnlocked || stored.UnlockReason != "confirmed false positive" {
		t.Fatalf("stored lock=%#v err=%v", stored, err)
	}
	if _, found, err := tokenCache.GetRuntime(t.Context(), database.PromptConversationLockCacheNamespace, lockKey); err != nil || found {
		t.Fatalf("cache after unlock found=%t err=%v", found, err)
	}
}

func TestAdminCanReleaseVerifiedUserCooldownAndInvalidateAllSessionCaches(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "admin-user-cooldown.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	lockKeys := []string{
		"1111111111111111111111111111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222222222222222222222222222",
	}
	tokenCache := cache.NewMemory(1)
	for index, lockKey := range lockKeys {
		item, _, err := db.LockPromptConversation(t.Context(), database.PromptConversationLockInput{
			LockKey: lockKey, Platform: "newapi", NewAPIUserID: "42",
			SessionFingerprint: []string{"11111111111111111111111111111111", "22222222222222222222222222222222"}[index],
			SessionHash:        []string{"session-a", "session-b"}[index], DecisionID: "decision-" + string(rune('a'+index)),
			ReasonCode: "upstream_cyber_policy",
		})
		if err != nil {
			t.Fatalf("LockPromptConversation: %v", err)
		}
		raw, _ := json.Marshal(item)
		if err := tokenCache.SetRuntime(t.Context(), database.PromptConversationLockCacheNamespace, lockKey, raw, time.Minute); err != nil {
			t.Fatalf("SetRuntime: %v", err)
		}
	}
	store := auth.NewStore(db, tokenCache, &database.SystemSettings{MaxConcurrency: 2, TestConcurrency: 1})
	handler := NewHandler(store, db, tokenCache, nil, "")
	router := gin.New()
	router.POST("/api/admin/prompt-policy/conversation-locks/:lock_key/unlock", handler.UnlockPromptConversation)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/prompt-policy/conversation-locks/"+lockKeys[0]+"/unlock", bytes.NewBufferString(`{"reason":"reviewed","scope":"user_cooldown"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unlock status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Scope         string `json:"scope"`
		UnlockedCount int    `json:"unlocked_count"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Scope != "user_cooldown" || response.UnlockedCount != 2 {
		t.Fatalf("unlock response=%+v err=%v body=%s", response, err, recorder.Body.String())
	}
	for _, lockKey := range lockKeys {
		if _, found, err := tokenCache.GetRuntime(t.Context(), database.PromptConversationLockCacheNamespace, lockKey); err != nil || found {
			t.Fatalf("cache %s after user cooldown release found=%t err=%v", lockKey, found, err)
		}
	}
}

func TestRiskProfileShowsUserCooldownWithManualReleaseMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "admin-user-cooldown-profile.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, _, err = db.LockPromptConversation(t.Context(), database.PromptConversationLockInput{
		LockKey:  "3333333333333333333333333333333333333333333333333333333333333333",
		Platform: "newapi", NewAPIUserID: "42", DecisionID: "decision-profile",
		ReasonCode: "upstream_cyber_policy", LockedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("LockPromptConversation: %v", err)
	}
	tokenCache := cache.NewMemory(1)
	store := auth.NewStore(db, tokenCache, &database.SystemSettings{MaxConcurrency: 2, TestConcurrency: 1})
	handler := NewHandler(store, db, tokenCache, nil, "")
	profiles := []*database.PromptRiskProfile{{
		SubjectType: database.PromptRiskSubjectNewAPIUser, Platform: "newapi", NewAPIUserID: "42",
	}}
	handler.attachPromptConversationLocks(t.Context(), profiles)
	lock := profiles[0].ConversationLock
	if lock == nil || lock.RestrictionScope != database.PromptConversationRestrictionScopeUserCooldown || lock.ExpiresAt == nil || lock.RemainingSeconds <= 0 {
		t.Fatalf("user cooldown profile lock=%#v", lock)
	}
}
