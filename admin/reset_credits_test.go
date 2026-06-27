package admin

import (
	"net/http"
	"strings"
	"testing"
)

func TestUpstreamResetErrorMessage_CreditsOnlyMapsToChineseAndKeepsRaw(t *testing.T) {
	body := []byte(`{"detail":{"code":"rate_limit_not_resettable","reason":"credits_only"}}`)
	msg := upstreamResetErrorMessage(http.StatusBadRequest, body)

	if !strings.Contains(msg, "额度（credits）计费") {
		t.Errorf("message missing Chinese explanation: %q", msg)
	}
	// 必须保留上游原文，便于排查。
	if !strings.Contains(msg, "rate_limit_not_resettable") || !strings.Contains(msg, "credits_only") {
		t.Errorf("message must retain raw upstream body: %q", msg)
	}
}

func TestUpstreamResetErrorMessage_KnownCodeWithoutReason(t *testing.T) {
	body := []byte(`{"detail":{"code":"rate_limit_not_resettable"}}`)
	msg := upstreamResetErrorMessage(http.StatusBadRequest, body)
	if !strings.Contains(msg, "不支持主动重置") {
		t.Errorf("expected generic not-resettable Chinese message, got %q", msg)
	}
	if !strings.Contains(msg, "rate_limit_not_resettable") {
		t.Errorf("expected raw body retained, got %q", msg)
	}
}

func TestUpstreamResetErrorMessage_UnknownCodeFallsBackToRaw(t *testing.T) {
	body := []byte(`{"detail":{"code":"something_new"}}`)
	msg := upstreamResetErrorMessage(http.StatusBadRequest, body)
	if !strings.Contains(msg, "something_new") {
		t.Errorf("unknown code should fall back to raw body, got %q", msg)
	}
	// 未识别 code 时不应硬塞中文说明。
	if strings.Contains(msg, "（上游：") {
		t.Errorf("unknown code should not be wrapped with Chinese prefix, got %q", msg)
	}
}

func TestUpstreamResetErrorMessage_EmptyBodyUsesStatus(t *testing.T) {
	msg := upstreamResetErrorMessage(http.StatusBadGateway, nil)
	if !strings.Contains(msg, "502") {
		t.Errorf("empty body should report status code, got %q", msg)
	}
}
