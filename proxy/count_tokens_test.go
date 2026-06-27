package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// TestLocalCountTokensEndpointsReturnInputTokens 覆盖 issue #238：本地 token 计数
// 兼容端点对合法请求返回 200 + 正数 input_tokens，对非法 JSON 返回 400。
func TestLocalCountTokensEndpointsReturnInputTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}

	cases := []struct {
		name    string
		handler gin.HandlerFunc
		body    string
	}{
		{
			name:    "messages/count_tokens",
			handler: h.CountTokens,
			body:    `{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hello world"}]}`,
		},
		{
			name:    "responses/input_tokens",
			handler: h.ResponsesInputTokens,
			body:    `{"model":"gpt-5.5","input":"hello world","instructions":"be brief"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			ctx.Request.Header.Set("Content-Type", "application/json")

			tc.handler(ctx)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			got := gjson.Get(rec.Body.String(), "input_tokens")
			if !got.Exists() || got.Int() <= 0 {
				t.Fatalf("input_tokens = %q, want positive; body=%s", got.Raw, rec.Body.String())
			}
		})
	}

	// 非法 JSON → 400
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not json"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	h.CountTokens(ctx)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid body status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
