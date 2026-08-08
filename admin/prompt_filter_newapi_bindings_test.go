package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/database"
	"github.com/gin-gonic/gin"
)

func TestPromptFilterNewAPIBindingAdminLifecycleMasksSecretsAndReloadsStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	apiKeyID := insertTestAPIKey(t, db, "Fanren Binding")
	tokenCache := cache.NewMemory(4)
	t.Cleanup(func() { _ = tokenCache.Close() })
	store := auth.NewStore(db, tokenCache, nil)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tokenCache, nil, "admin-secret")
	router := gin.New()
	handler.RegisterRoutes(router)

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-Admin-Key", "admin-secret")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		router.ServeHTTP(recorder, req)
		return recorder
	}

	invalidAPIKeyID := insertTestAPIKey(t, db, "Invalid Platform Binding")
	invalidCode := strings.Repeat("a", 33)
	invalid := do(http.MethodPost, "/api/admin/prompt-filter/newapi-bindings", `{"api_key_id":`+jsonNumber(invalidAPIKeyID)+`,"platform_code":"`+invalidCode+`"}`)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "最长 32 字符") {
		t.Fatalf("overlong platform code response=%d body=%s", invalid.Code, invalid.Body.String())
	}

	createBody := `{"api_key_id":` + jsonNumber(apiKeyID) + `,"platform_code":"gateway-a","platform_name":"示例平台 NewAPI","prompt_filter_scope":"local_only"}`
	created := do(http.MethodPost, "/api/admin/prompt-filter/newapi-bindings", createBody)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var createResponse promptFilterNewAPIBindingResponse
	if err := json.Unmarshal(created.Body.Bytes(), &createResponse); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if len(createResponse.Secret) != 64 || createResponse.SecretMasked == "" {
		t.Fatalf("create secret response = %#v", createResponse)
	}
	if createResponse.RequireSignedIdentity {
		t.Fatal("create default require_signed_identity = true, want safe migration default false")
	}
	if createResponse.PromptFilterScope != database.PromptFilterScopeLocalOnly {
		t.Fatalf("create prompt filter scope = %q", createResponse.PromptFilterScope)
	}
	runtimeBinding, ok := store.GetPromptFilterNewAPIBinding(apiKeyID)
	if !ok || runtimeBinding.Secret != createResponse.Secret {
		t.Fatalf("runtime create binding=%#v ok=%v", runtimeBinding, ok)
	}

	listed := do(http.MethodGet, "/api/admin/prompt-filter/newapi-bindings", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	if strings.Contains(listed.Body.String(), `"secret":"`) || strings.Contains(listed.Body.String(), createResponse.Secret) {
		t.Fatalf("list leaked secret: %s", listed.Body.String())
	}

	invalidScope := do(http.MethodPatch, "/api/admin/prompt-filter/newapi-bindings/"+jsonNumber(apiKeyID), `{"prompt_filter_scope":"remote_only"}`)
	if invalidScope.Code != http.StatusBadRequest {
		t.Fatalf("invalid scope status=%d body=%s", invalidScope.Code, invalidScope.Body.String())
	}

	patched := do(http.MethodPatch, "/api/admin/prompt-filter/newapi-bindings/"+jsonNumber(apiKeyID), `{"platform_name":"示例平台生产站","enabled":false,"prompt_filter_scope":"off"}`)
	if patched.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", patched.Code, patched.Body.String())
	}
	runtimeBinding, _ = store.GetPromptFilterNewAPIBinding(apiKeyID)
	if runtimeBinding.PlatformName != "示例平台生产站" || runtimeBinding.Enabled || runtimeBinding.PromptFilterScope != database.PromptFilterScopeOff {
		t.Fatalf("runtime patch binding=%#v", runtimeBinding)
	}
	if strings.Contains(patched.Body.String(), "policy_mode") || strings.Contains(patched.Body.String(), "policy_profile") {
		t.Fatalf("binding response exposed retired policy controls: %s", patched.Body.String())
	}

	unsafeGenerated := do(http.MethodPost, "/api/admin/prompt-filter/newapi-bindings/"+jsonNumber(apiKeyID)+"/secret/generate", `{"grace_seconds":0}`)
	if unsafeGenerated.Code != http.StatusBadRequest || !strings.Contains(unsafeGenerated.Body.String(), "60 到 86400") {
		t.Fatalf("unsafe generated-secret rotation status=%d body=%s", unsafeGenerated.Code, unsafeGenerated.Body.String())
	}
	unchangedBinding, _ := store.GetPromptFilterNewAPIBinding(apiKeyID)
	if unchangedBinding.Secret != createResponse.Secret {
		t.Fatalf("rejected generated-secret rotation changed runtime secret: %#v", unchangedBinding)
	}

	generated := do(http.MethodPost, "/api/admin/prompt-filter/newapi-bindings/"+jsonNumber(apiKeyID)+"/secret/generate", `{"grace_seconds":300}`)
	if generated.Code != http.StatusOK {
		t.Fatalf("generate status=%d body=%s", generated.Code, generated.Body.String())
	}
	var generatedResponse promptFilterNewAPIBindingResponse
	if err := json.Unmarshal(generated.Body.Bytes(), &generatedResponse); err != nil {
		t.Fatalf("decode generated: %v", err)
	}
	if len(generatedResponse.Secret) != 64 || generatedResponse.Secret == createResponse.Secret || !generatedResponse.PreviousSecretActive {
		t.Fatalf("generated response=%#v", generatedResponse)
	}
	runtimeBinding, _ = store.GetPromptFilterNewAPIBinding(apiKeyID)
	if runtimeBinding.Secret != generatedResponse.Secret || runtimeBinding.PreviousSecret != createResponse.Secret {
		t.Fatalf("runtime rotated binding=%#v", runtimeBinding)
	}

	replacementSecret := "replacement-secret-0123456789-ABCDEFG"
	replaced := do(http.MethodPut, "/api/admin/prompt-filter/newapi-bindings/"+jsonNumber(apiKeyID)+"/secret", `{"secret":"`+replacementSecret+`","grace_seconds":0}`)
	if replaced.Code != http.StatusOK {
		t.Fatalf("replace status=%d body=%s", replaced.Code, replaced.Body.String())
	}
	var replacedResponse promptFilterNewAPIBindingResponse
	if err := json.Unmarshal(replaced.Body.Bytes(), &replacedResponse); err != nil {
		t.Fatalf("decode replaced: %v", err)
	}
	if replacedResponse.Secret != replacementSecret || replacedResponse.PreviousSecretActive {
		t.Fatalf("replaced response=%#v", replacedResponse)
	}
	runtimeBinding, _ = store.GetPromptFilterNewAPIBinding(apiKeyID)
	if runtimeBinding.Secret != replacementSecret || runtimeBinding.PreviousSecret != "" || runtimeBinding.PreviousSecretExpiresAt != nil {
		t.Fatalf("runtime no-grace replacement=%#v", runtimeBinding)
	}

	deleted := do(http.MethodDelete, "/api/admin/prompt-filter/newapi-bindings/"+jsonNumber(apiKeyID), "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if _, ok := store.GetPromptFilterNewAPIBinding(apiKeyID); ok {
		t.Fatal("runtime binding remained after delete")
	}
}

func TestPromptFilterNewAPIBindingSecretRotationCompletesAfterClientCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	apiKeyID := insertTestAPIKey(t, db, "Canceled Binding Rotation")
	originalSecret := "01234567890123456789012345678901"
	if err := db.CreatePromptFilterNewAPIBinding(context.Background(), &database.PromptFilterNewAPIBinding{
		APIKeyID: apiKeyID, PlatformCode: "gateway-a-canceled", PlatformName: "示例平台取消测试", Secret: originalSecret,
		Enabled: true,
	}); err != nil {
		t.Fatalf("create binding: %v", err)
	}
	tokenCache := cache.NewMemory(4)
	t.Cleanup(func() { _ = tokenCache.Close() })
	store := auth.NewStore(db, tokenCache, nil)
	t.Cleanup(store.Stop)
	if err := store.LoadPromptFilterNewAPIBindings(context.Background()); err != nil {
		t.Fatalf("load bindings: %v", err)
	}
	handler := NewHandler(store, db, tokenCache, nil, "admin-secret")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext, cancelRequest := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPut, "/api/admin/prompt-filter/newapi-bindings/"+jsonNumber(apiKeyID)+"/secret", nil).WithContext(requestContext)
	c.Params = gin.Params{{Key: "api_key_id", Value: jsonNumber(apiKeyID)}}
	cancelRequest()
	replacementSecret := "replacement-after-cancel-0123456789"
	grace := int64(300)
	handler.replacePromptFilterNewAPIBindingSecret(c, replacementSecret, &grace)
	if recorder.Code != http.StatusOK {
		t.Fatalf("canceled request rotation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response promptFilterNewAPIBindingResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode rotation response: %v", err)
	}
	if response.Secret != replacementSecret {
		t.Fatalf("one-time replacement secret was lost: %#v", response)
	}
	persisted, err := db.GetPromptFilterNewAPIBinding(context.Background(), apiKeyID)
	if err != nil || persisted.Secret != replacementSecret || persisted.PreviousSecret != originalSecret {
		t.Fatalf("persisted binding=%#v err=%v", persisted, err)
	}
	runtimeBinding, ok := store.GetPromptFilterNewAPIBinding(apiKeyID)
	if !ok || runtimeBinding.Secret != persisted.Secret || runtimeBinding.PreviousSecret != persisted.PreviousSecret || runtimeBinding.PreviousSecretExpiresAt == nil || persisted.PreviousSecretExpiresAt == nil || !runtimeBinding.PreviousSecretExpiresAt.Equal(*persisted.PreviousSecretExpiresAt) {
		t.Fatalf("runtime binding diverged after canceled request: runtime=%#v persisted=%#v ok=%v", runtimeBinding, persisted, ok)
	}
}

func jsonNumber(value int64) string {
	return strconv.FormatInt(value, 10)
}
