package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSendFinalUpstreamError_UsageLimitRewrites429(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	handler := &Handler{}
	body := []byte(`{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached","plan_type":"free","resets_at":1775317531,"resets_in_seconds":602705}}`)

	handler.sendFinalUpstreamError(ctx, http.StatusTooManyRequests, body)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if got := recorder.Header().Get("Retry-After"); got != "602705" {
		t.Fatalf("Retry-After = %q, want %q", got, "602705")
	}

	var payload struct {
		Error struct {
			Message         string `json:"message"`
			Type            string `json:"type"`
			Code            string `json:"code"`
			PlanType        string `json:"plan_type"`
			ResetsAt        int64  `json:"resets_at"`
			ResetsInSeconds int64  `json:"resets_in_seconds"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Error.Type != "server_error" {
		t.Fatalf("type = %q, want %q", payload.Error.Type, "server_error")
	}
	if payload.Error.Code != "account_pool_usage_limit_reached" {
		t.Fatalf("code = %q, want %q", payload.Error.Code, "account_pool_usage_limit_reached")
	}
	if payload.Error.PlanType != "free" {
		t.Fatalf("plan_type = %q, want %q", payload.Error.PlanType, "free")
	}
	if payload.Error.ResetsAt != 1775317531 {
		t.Fatalf("resets_at = %d, want %d", payload.Error.ResetsAt, 1775317531)
	}
	if payload.Error.ResetsInSeconds != 602705 {
		t.Fatalf("resets_in_seconds = %d, want %d", payload.Error.ResetsInSeconds, 602705)
	}
	if payload.Error.Message == "" {
		t.Fatal("expected non-empty aggregated error message")
	}
}

func TestSendFinalUpstreamError_FallsBackForNonUsageLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	handler := &Handler{}
	body := []byte(`{"error":{"type":"rate_limit_error","message":"Too many requests"}}`)

	handler.sendFinalUpstreamError(ctx, http.StatusTooManyRequests, body)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
	}
	if got := recorder.Header().Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After = %q, want empty", got)
	}
}

func TestSendFinalUpstreamError_UsageLimitMissingTimeFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	handler := &Handler{}
	// usage_limit_reached 但不含 resets_at / resets_in_seconds
	body := []byte(`{"error":{"type":"usage_limit_reached","message":"limit reached"}}`)

	handler.sendFinalUpstreamError(ctx, http.StatusTooManyRequests, body)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	// 无 resets_in_seconds 时不应设置 Retry-After
	if got := recorder.Header().Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After = %q, want empty (no resets_in_seconds)", got)
	}

	// 验证零值字段不出现在响应中
	var raw map[string]map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj := raw["error"]
	if _, exists := errObj["resets_at"]; exists {
		t.Fatal("resets_at should be omitted when 0")
	}
	if _, exists := errObj["resets_in_seconds"]; exists {
		t.Fatal("resets_in_seconds should be omitted when 0")
	}
	if _, exists := errObj["plan_type"]; exists {
		t.Fatal("plan_type should be omitted when empty")
	}
}

func TestSendFinalUpstreamError_Non429StatusPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	handler := &Handler{}
	body := []byte(`{"error":{"type":"server_error","message":"internal failure"}}`)

	handler.sendFinalUpstreamError(ctx, http.StatusInternalServerError, body)

	// 非 429 直接透传原状态码
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
}
