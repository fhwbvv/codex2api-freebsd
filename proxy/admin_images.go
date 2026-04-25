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

func gjsonGetString(body []byte, path string) string {
	return gjson.GetBytes(body, path).String()
}
