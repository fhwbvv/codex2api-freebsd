package proxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// 未转换的握手工作区停用错误必须以 503 池级错误返回,绝不能 500 + 原始握手细节
// 漏给下游。
func TestErrorToGinResponseHandshakeDeactivatedBecomes503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ErrorToGinResponse(c, errors.New(`websocket handshake failed: websocket: bad handshake (HTTP 402 Payment Required); Cf-Ray=x: {"detail":{"code":"deactivated_workspace"}}`))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Retry-After missing")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "account_pool_deactivated") {
		t.Fatalf("body = %s", body)
	}
	// 文案须附上游原始错误体,但握手内部细节(Cf-Ray 等)不外漏。
	if !strings.Contains(body, "deactivated_workspace") {
		t.Fatalf("upstream detail missing: %s", body)
	}
	if strings.Contains(body, "websocket handshake failed") || strings.Contains(body, "Cf-Ray") {
		t.Fatalf("raw handshake detail leaked: %s", body)
	}

	// 普通错误仍走 500 fallback
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	ErrorToGinResponse(c, errors.New("dial tcp timeout"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("generic error status = %d, want 500", rec.Code)
	}
}
