package proxy

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/codex2api/database"
	"github.com/codex2api/security"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// GenerateImageOnceForAdmin executes the existing Images API handler in-process.
// It keeps model aliasing, account dispatch, usage logging, and image parsing in one code path.
func (h *Handler) GenerateImageOnceForAdmin(ctx context.Context, rawBody []byte, apiKey *database.APIKeyRow) ([]byte, int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if h == nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("image proxy handler is not initialized")
	}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(rawBody)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	if apiKey != nil && strings.TrimSpace(apiKey.Key) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey.Key)
		ginCtx.Set(contextAPIKeyID, apiKey.ID)
		ginCtx.Set(contextAPIKeyName, strings.TrimSpace(apiKey.Name))
		ginCtx.Set(contextAPIKeyMasked, security.MaskAPIKey(apiKey.Key))
	}
	ginCtx.Request = req

	// 这条 in-process 路径刻意不放 contextAPIKeyRow（否则并发槽会被重复占用），
	// 因此 Key 级限额由调用方在外层请求上校验。scope 预算（issue #439）是调度层面的
	// 过滤，只有挂在这个 gin context 上才生效，所以在这里单独算一次。
	if status, msg := h.applyAdminScopeBudget(ctx, ginCtx, apiKey); status != 0 {
		return nil, status, fmt.Errorf("%s", msg)
	}

	h.ImagesGenerations(ginCtx)

	body := recorder.Body.Bytes()
	if recorder.Code < 200 || recorder.Code >= 300 {
		msg := strings.TrimSpace(extractAdminImageErrorMessage(body))
		if msg == "" {
			msg = fmt.Sprintf("image generation failed with HTTP %d", recorder.Code)
		}
		return body, recorder.Code, fmt.Errorf("%s", msg)
	}
	return body, recorder.Code, nil
}

func extractAdminImageErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	message := strings.TrimSpace(gjsonGetString(body, "error.message"))
	if message != "" {
		return message
	}
	message = strings.TrimSpace(gjsonGetString(body, "error"))
	if message != "" {
		return message
	}
	return strings.TrimSpace(string(body))
}

// GenerateImageEditForAdmin executes the ImagesEdits handler in-process for
// image-to-image (edit) jobs from the admin image studio.
func (h *Handler) GenerateImageEditForAdmin(ctx context.Context, rawBody []byte, apiKey *database.APIKeyRow) ([]byte, int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if h == nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("image proxy handler is not initialized")
	}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(rawBody)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	if apiKey != nil && strings.TrimSpace(apiKey.Key) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey.Key)
		ginCtx.Set(contextAPIKeyID, apiKey.ID)
		ginCtx.Set(contextAPIKeyName, strings.TrimSpace(apiKey.Name))
		ginCtx.Set(contextAPIKeyMasked, security.MaskAPIKey(apiKey.Key))
	}
	ginCtx.Request = req

	if status, msg := h.applyAdminScopeBudget(ctx, ginCtx, apiKey); status != 0 {
		return nil, status, fmt.Errorf("%s", msg)
	}

	h.ImagesEdits(ginCtx)

	body := recorder.Body.Bytes()
	if recorder.Code < 200 || recorder.Code >= 300 {
		msg := strings.TrimSpace(extractAdminImageErrorMessage(body))
		if msg == "" {
			msg = fmt.Sprintf("image edit failed with HTTP %d", recorder.Code)
		}
		return body, recorder.Code, fmt.Errorf("%s", msg)
	}
	return body, recorder.Code, nil
}

func gjsonGetString(body []byte, path string) string {
	return gjson.GetBytes(body, path).String()
}

// applyAdminScopeBudget 为 in-process 的管理端生图任务计算 scope 预算闸门：
// reject 类超额直接返回 429，skip 类挂到该 gin context 上供账号过滤链剔除候选。
func (h *Handler) applyAdminScopeBudget(ctx context.Context, ginCtx *gin.Context, apiKey *database.APIKeyRow) (int, string) {
	if apiKey == nil || len(apiKey.Limits.ScopeLimits) == 0 {
		return 0, ""
	}
	gate, rejectMsg := h.evaluateAPIKeyScopeBudgets(ctx, apiKey)
	if rejectMsg != "" {
		return http.StatusTooManyRequests, rejectMsg
	}
	if gate != nil {
		ginCtx.Set(contextScopeBudgetGate, gate)
	}
	return 0, ""
}
