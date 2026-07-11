package wsrelay

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestFormatDialHandshakeErrorIncludesStatusAndJSONBody(t *testing.T) {
	body := `{"error":{"message":"Your account has been deactivated","type":"invalid_request_error","code":"account_deactivated"}}`
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Header: http.Header{
			"Cf-Ray":       []string{"abc123-SJC"},
			"X-Request-Id": []string{"req_test_1"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}

	err := formatDialHandshakeError(websocket.ErrBadHandshake, resp)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{
		"websocket handshake failed",
		"bad handshake",
		"HTTP 403",
		"Forbidden",
		"Cf-Ray=abc123-SJC",
		"X-Request-Id=req_test_1",
		`"message":"Your account has been deactivated"`,
		`"type":"invalid_request_error"`,
		`"code":"account_deactivated"`,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
	// 前缀与 JSON body 分行，方便前端 pretty-print。
	if !strings.Contains(msg, ":\n{") {
		t.Fatalf("expected newline before JSON body, got %q", msg)
	}

	// body 应可再次读取（回填 NopCloser）。
	again, _ := io.ReadAll(resp.Body)
	if string(again) != body {
		t.Fatalf("body not restored: %q", again)
	}
}

func TestFormatDialHandshakeErrorTokenExpiredJSON(t *testing.T) {
	body := `{"error":{"message":"Provided authentication token is expired. Please try signing in again.","type":"invalid_request_error","param":null,"code":"token_expired"}}`
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     http.Header{"Cf-Ray": []string{"a193c1ed3ac42eae-LAX"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	err := formatDialHandshakeError(websocket.ErrBadHandshake, resp)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "HTTP 401") {
		t.Fatalf("missing status: %q", msg)
	}
	if !strings.Contains(msg, `"code":"token_expired"`) {
		t.Fatalf("missing raw json field: %q", msg)
	}
	if !strings.Contains(msg, `"message":"Provided authentication token is expired. Please try signing in again."`) {
		t.Fatalf("missing message field: %q", msg)
	}
}

func TestFormatDialHandshakeErrorWithoutResponse(t *testing.T) {
	err := formatDialHandshakeError(errors.New("dial tcp timeout"), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "websocket handshake failed") || !strings.Contains(msg, "dial tcp timeout") {
		t.Fatalf("unexpected: %q", msg)
	}
	if strings.Contains(msg, "HTTP") {
		t.Fatalf("should not invent HTTP status: %q", msg)
	}
}

func TestFormatFailedHandshakeHTTPBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"X-Error-Message": []string{"rate limited by upstream"}},
		Body:       io.NopCloser(strings.NewReader(`{"message":"Too Many Requests"}`)),
	}
	msg := formatFailedHandshakeHTTPBody(http.StatusTooManyRequests, resp)
	for _, want := range []string{
		"HTTP 429",
		"Too Many Requests",
		"X-Error-Message=rate limited by upstream",
		`"message":"Too Many Requests"`,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("msg %q missing %q", msg, want)
		}
	}
}

func TestReadHTTPErrorBodyPreservesJSON(t *testing.T) {
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(`{"error": {"code": "token_expired", "message": "expired"}}`)),
	}
	got := readHTTPErrorBody(resp)
	if !strings.Contains(got, `"code":"token_expired"`) || !strings.Contains(got, `"message":"expired"`) {
		t.Fatalf("unexpected body: %q", got)
	}
}
