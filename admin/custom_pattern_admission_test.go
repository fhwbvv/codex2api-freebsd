package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/proxy"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

func TestUpdateSettingsRejectsBroadCustomRuleWithoutChangingPersistenceOrStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	settings.PromptFilterCustomPatterns = `[{
		"name":"existing_safe_rule",
		"pattern":"terminal-safe-marker",
		"weight":60,
		"category":"custom"
	}]`
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")

	before := promptfilter.MarshalCustomPatterns(store.GetPromptFilterConfig().CustomPatterns)
	submitted := []promptfilter.PatternConfig{{Name: "all", Pattern: `(?i)\ball\b`, Weight: 100, Strict: true}}
	submittedJSON, _ := json.Marshal(submitted)
	body, _ := json.Marshal(map[string]string{
		"prompt_filter_custom_patterns":          string(submittedJSON),
		"prompt_filter_custom_patterns_expected": settings.PromptFilterCustomPatterns,
	})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 body=%s", recorder.Code, recorder.Body.String())
	}
	if got := promptfilter.MarshalCustomPatterns(store.GetPromptFilterConfig().CustomPatterns); got != before {
		t.Fatalf("runtime rules changed\nbefore=%s\nafter=%s", before, got)
	}
	persisted, err := db.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.PromptFilterCustomPatterns != settings.PromptFilterCustomPatterns {
		t.Fatalf("persisted rules changed\nbefore=%s\nafter=%s", settings.PromptFilterCustomPatterns, persisted.PromptFilterCustomPatterns)
	}
}

func TestUpdateSettingsAllowsExplicitlyDisabledBroadLegacyRule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")

	disabled := false
	submitted := []promptfilter.PatternConfig{{Name: "all", Pattern: `(?i)\ball\b`, Weight: 100, Strict: true, Enabled: &disabled}}
	submittedJSON, _ := json.Marshal(submitted)
	body, _ := json.Marshal(map[string]string{
		"prompt_filter_custom_patterns":          string(submittedJSON),
		"prompt_filter_custom_patterns_expected": settings.PromptFilterCustomPatterns,
	})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 body=%s", recorder.Code, recorder.Body.String())
	}
	got := store.GetPromptFilterConfig().CustomPatterns
	if len(got) != 1 || got[0].Enabled == nil || *got[0].Enabled {
		t.Fatalf("disabled legacy rule was not retained: %#v", got)
	}
}

func TestUpdateSettingsDeletesQuarantinedLegacyRuleFromRuntimeSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	settings.PromptFilterCustomPatterns = `[
		{"name":"legacy_broad_rule","pattern":"(?i)\\ball\\b","weight":100,"category":"custom","strict":true},
		{"name":"existing_safe_rule","pattern":"terminal-safe-marker","weight":60,"category":"custom"}
	]`
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")

	runtimeBefore := store.GetPromptFilterConfig().CustomPatterns
	if len(runtimeBefore) != 2 || runtimeBefore[0].Enabled == nil || *runtimeBefore[0].Enabled {
		t.Fatalf("legacy rule was not quarantined in runtime snapshot: %#v", runtimeBefore)
	}
	expected := promptfilter.MarshalCustomPatterns(runtimeBefore)
	replacement := []promptfilter.PatternConfig{runtimeBefore[1]}
	replacementJSON := promptfilter.MarshalCustomPatterns(replacement)
	body, _ := json.Marshal(map[string]string{
		"prompt_filter_custom_patterns":          replacementJSON,
		"prompt_filter_custom_patterns_expected": expected,
	})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 body=%s", recorder.Code, recorder.Body.String())
	}
	persisted, err := db.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.PromptFilterCustomPatterns != replacementJSON {
		t.Fatalf("quarantined rule was not deleted from persistence: %s", persisted.PromptFilterCustomPatterns)
	}
	runtimeAfter := store.GetPromptFilterConfig().CustomPatterns
	if len(runtimeAfter) != 1 || runtimeAfter[0].Name != "existing_safe_rule" {
		t.Fatalf("runtime rules after delete = %#v", runtimeAfter)
	}
}

func TestUpdateSettingsRejectsStaleCustomRuleSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	settings.PromptFilterCustomPatterns = `[{"name":"first_rule","pattern":"first-marker","weight":60,"category":"custom"}]`
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")

	concurrent := `[{"name":"second_rule","pattern":"second-marker","weight":60,"category":"custom"}]`
	if err := db.ReplacePromptFilterCustomPatterns(context.Background(), concurrent); err != nil {
		t.Fatalf("publish concurrent rules: %v", err)
	}
	submitted := []promptfilter.PatternConfig{{Name: "third_rule", Pattern: `third-marker`, Weight: 60, Category: "custom"}}
	submittedJSON, _ := json.Marshal(submitted)
	body, _ := json.Marshal(map[string]string{
		"prompt_filter_custom_patterns":          string(submittedJSON),
		"prompt_filter_custom_patterns_expected": settings.PromptFilterCustomPatterns,
	})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409 body=%s", recorder.Code, recorder.Body.String())
	}
	persisted, err := db.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.PromptFilterCustomPatterns != concurrent {
		t.Fatalf("stale request overwrote concurrent rules: %s", persisted.PromptFilterCustomPatterns)
	}
	runtime := store.GetPromptFilterConfig().CustomPatterns
	if len(runtime) != 1 || runtime[0].Name != "second_rule" {
		t.Fatalf("conflict did not refresh authoritative runtime rules: %#v", runtime)
	}
}

func TestUpdateSettingsRequiresCustomRuleSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")

	submitted := []promptfilter.PatternConfig{{Name: "safe_rule", Pattern: `safe-marker`, Weight: 60, Category: "custom"}}
	submittedJSON, _ := json.Marshal(submitted)
	body, _ := json.Marshal(map[string]string{"prompt_filter_custom_patterns": string(submittedJSON)})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409 body=%s", recorder.Code, recorder.Body.String())
	}
	persisted, err := db.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.PromptFilterCustomPatterns != settings.PromptFilterCustomPatterns {
		t.Fatalf("snapshot-less request changed rules: %s", persisted.PromptFilterCustomPatterns)
	}
}

func TestUpdateSettingsRejectsMixedCustomRuleAndSettingsRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")

	submitted := []promptfilter.PatternConfig{{Name: "safe_rule", Pattern: `safe-marker`, Weight: 60, Category: "custom"}}
	submittedJSON, _ := json.Marshal(submitted)
	body, _ := json.Marshal(map[string]any{
		"prompt_filter_custom_patterns":          string(submittedJSON),
		"prompt_filter_custom_patterns_expected": settings.PromptFilterCustomPatterns,
		"prompt_filter_enabled":                  !settings.PromptFilterEnabled,
	})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 body=%s", recorder.Code, recorder.Body.String())
	}
	persisted, err := db.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.PromptFilterCustomPatterns != settings.PromptFilterCustomPatterns || persisted.PromptFilterEnabled != settings.PromptFilterEnabled {
		t.Fatalf("mixed request changed settings: %#v", persisted)
	}
	if store.GetPromptFilterConfig().Enabled != settings.PromptFilterEnabled {
		t.Fatal("mixed request changed runtime Prompt configuration")
	}
}

func TestUpdateSettingsAcceptsSemanticallyEqualFormattedRuleSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	settings.PromptFilterCustomPatterns = `[
		{"name":"existing_rule","pattern":"existing-marker","weight":60,"category":"custom"}
	]`
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")

	expected := promptfilter.MarshalCustomPatterns(store.GetPromptFilterConfig().CustomPatterns)
	submitted := []promptfilter.PatternConfig{{Name: "replacement_rule", Pattern: `replacement-marker`, Weight: 60, Category: "custom"}}
	submittedJSON, _ := json.Marshal(submitted)
	body, _ := json.Marshal(map[string]string{
		"prompt_filter_custom_patterns":          string(submittedJSON),
		"prompt_filter_custom_patterns_expected": expected,
	})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 body=%s", recorder.Code, recorder.Body.String())
	}
	persisted, err := db.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.PromptFilterCustomPatterns != promptfilter.MarshalCustomPatterns(submitted) {
		t.Fatalf("formatted equivalent snapshot was not accepted: %s", persisted.PromptFilterCustomPatterns)
	}
}

func TestUpdateSettingsCustomRuleSaveDoesNotOverwriteUnrelatedConcurrentSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	settings.SiteName = "before concurrent update"
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")

	concurrent, err := db.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	concurrent.SiteName = "preserve concurrent update"
	concurrent.GlobalRPM = settings.GlobalRPM + 77
	if err := db.UpdateSystemSettings(context.Background(), concurrent); err != nil {
		t.Fatalf("save concurrent unrelated settings: %v", err)
	}
	submitted := []promptfilter.PatternConfig{{Name: "new_rule", Pattern: `new-marker`, Weight: 60, Category: "custom"}}
	submittedJSON, _ := json.Marshal(submitted)
	body, _ := json.Marshal(map[string]string{
		"prompt_filter_custom_patterns":          string(submittedJSON),
		"prompt_filter_custom_patterns_expected": settings.PromptFilterCustomPatterns,
	})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 body=%s", recorder.Code, recorder.Body.String())
	}
	persisted, err := db.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.SiteName != concurrent.SiteName || persisted.GlobalRPM != concurrent.GlobalRPM {
		t.Fatalf("rule-only save overwrote unrelated settings: site=%q rpm=%d", persisted.SiteName, persisted.GlobalRPM)
	}
	if persisted.PromptFilterCustomPatterns != promptfilter.MarshalCustomPatterns(submitted) {
		t.Fatalf("rule-only save did not update rules: %s", persisted.PromptFilterCustomPatterns)
	}
}

func TestUpdateSettingsCustomRuleSnapshotPreservesUnknownFutureFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	settings.PromptFilterCustomPatterns = `[{"name":"future_rule","pattern":"future-marker","weight":60,"category":"custom","future_mode":"v2"}]`
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")

	expectedWithoutFutureField := promptfilter.MarshalCustomPatterns(store.GetPromptFilterConfig().CustomPatterns)
	submitted := []promptfilter.PatternConfig{{Name: "replacement_rule", Pattern: `replacement-marker`, Weight: 60, Category: "custom"}}
	submittedJSON, _ := json.Marshal(submitted)
	body, _ := json.Marshal(map[string]string{
		"prompt_filter_custom_patterns":          string(submittedJSON),
		"prompt_filter_custom_patterns_expected": expectedWithoutFutureField,
	})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateSettings(ctx)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409 body=%s", recorder.Code, recorder.Body.String())
	}
	persisted, err := db.GetSystemSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.PromptFilterCustomPatterns != settings.PromptFilterCustomPatterns {
		t.Fatalf("unknown future rule field was lost: %s", persisted.PromptFilterCustomPatterns)
	}
}
