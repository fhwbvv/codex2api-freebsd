package proxy

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/codex2api/auth"
)

func TestReadSSEStream_MergesMultilineData(t *testing.T) {
	input := strings.NewReader("data: {\"type\":\"response.output_text.delta\",\n" +
		"data: \"delta\":\"hello\"}\n\n" +
		"data: [DONE]\n\n")

	var events []string
	err := ReadSSEStream(input, func(data []byte) bool {
		events = append(events, string(data))
		return true
	})
	if err != nil {
		t.Fatalf("ReadSSEStream returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	want := "{\"type\":\"response.output_text.delta\",\n\"delta\":\"hello\"}"
	if events[0] != want {
		t.Fatalf("unexpected merged event: got %q want %q", events[0], want)
	}
}

func TestClassifyStreamOutcome(t *testing.T) {
	tests := []struct {
		name         string
		ctxErr       error
		readErr      error
		writeErr     error
		gotTerminal  bool
		wantStatus   int
		wantKind     string
		wantPenalize bool
	}{
		{
			name:        "terminal success",
			gotTerminal: true,
			wantStatus:  200,
		},
		{
			name:         "client canceled",
			ctxErr:       context.Canceled,
			wantStatus:   logStatusClientClosed,
			wantPenalize: false,
		},
		{
			name:         "upstream timeout",
			readErr:      errors.New("read timeout"),
			wantStatus:   logStatusUpstreamStreamBreak,
			wantKind:     "timeout",
			wantPenalize: true,
		},
		{
			name:         "upstream early eof",
			wantStatus:   logStatusUpstreamStreamBreak,
			wantKind:     "transport",
			wantPenalize: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			outcome := classifyStreamOutcome(tc.ctxErr, tc.readErr, tc.writeErr, tc.gotTerminal)
			if outcome.logStatusCode != tc.wantStatus {
				t.Fatalf("status mismatch: got %d want %d", outcome.logStatusCode, tc.wantStatus)
			}
			if outcome.failureKind != tc.wantKind {
				t.Fatalf("failure kind mismatch: got %q want %q", outcome.failureKind, tc.wantKind)
			}
			if outcome.penalize != tc.wantPenalize {
				t.Fatalf("penalize mismatch: got %v want %v", outcome.penalize, tc.wantPenalize)
			}
		})
	}
}

func TestShouldRecyclePooledClient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "connection shutting down",
			err:  errors.New("http2: client connection is shutting down"),
			want: true,
		},
		{
			name: "connection reset",
			err:  errors.New("read tcp: connection reset by peer"),
			want: true,
		},
		{
			name: "broken pipe",
			err:  errors.New("write: broken pipe"),
			want: true,
		},
		{
			name: "plain timeout",
			err:  errors.New("read timeout"),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRecyclePooledClient(tc.err); got != tc.want {
				t.Fatalf("shouldRecyclePooledClient() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShouldTransparentRetryStream(t *testing.T) {
	retryable := streamOutcome{
		logStatusCode:  logStatusUpstreamStreamBreak,
		failureKind:    "transport",
		failureMessage: "upstream failed before first byte",
		penalize:       true,
	}

	if !shouldTransparentRetryStream(retryable, 0, 2, false, nil, nil) {
		t.Fatal("expected early upstream failure to be transparently retried")
	}
	if shouldTransparentRetryStream(retryable, 2, 2, false, nil, nil) {
		t.Fatal("expected retry to stop at maxRetries")
	}
	if shouldTransparentRetryStream(retryable, 0, 2, true, nil, nil) {
		t.Fatal("expected retry to stop after downstream already received bytes")
	}
	if shouldTransparentRetryStream(retryable, 0, 2, false, context.Canceled, nil) {
		t.Fatal("expected retry to stop when downstream context is canceled")
	}
}

func TestApplyCodexRequestHeadersUsesSessionIDWithoutConversationID(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}

	acc := &auth.Account{
		DBID:      42,
		AccountID: "acct-42",
	}
	cfg := &DeviceProfileConfig{
		UserAgent:              "codex_cli_rs/0.120.0 (Mac OS 15.5.0; arm64) Apple_Terminal/464",
		PackageVersion:         "0.120.0",
		RuntimeVersion:         "0.120.0",
		OS:                     "MacOS",
		Arch:                   "arm64",
		StabilizeDeviceProfile: true,
	}
	downstreamHeaders := http.Header{
		"Originator": []string{"custom-originator"},
	}

	applyCodexRequestHeaders(req, acc, "token-123", "cache-key-1", "api-key-1", cfg, downstreamHeaders)

	if got := req.Header.Get("Authorization"); got != "Bearer token-123" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get("Session_id"); got != "cache-key-1" {
		t.Fatalf("Session_id = %q", got)
	}
	if got := req.Header.Get("Conversation_id"); got != "" {
		t.Fatalf("Conversation_id = %q, want empty", got)
	}
	if got := req.Header.Get("User-Agent"); got != cfg.UserAgent {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := req.Header.Get("Version"); got != "0.120.0" {
		t.Fatalf("Version = %q", got)
	}
	if got := req.Header.Get("Originator"); got != Originator {
		t.Fatalf("Originator = %q, want fallback %q", got, Originator)
	}
	if got := req.Header.Get("Chatgpt-Account-Id"); got != "acct-42" {
		t.Fatalf("Chatgpt-Account-Id = %q", got)
	}
	for _, name := range []string{"X-Stainless-Package-Version", "X-Stainless-Runtime-Version", "X-Stainless-Os", "X-Stainless-Arch"} {
		if got := req.Header.Get(name); got != "" {
			t.Fatalf("%s = %q, want empty", name, got)
		}
	}
}

func TestApplyCodexRequestHeadersUsesMinimalFallbackByDefault(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}

	acc := &auth.Account{
		DBID:      42,
		AccountID: "acct-42",
	}

	applyCodexRequestHeaders(req, acc, "token-123", "", "api-key-1", nil, http.Header{})

	if got := req.Header.Get("User-Agent"); got != latestCodexCLIUserAgentPrefix {
		t.Fatalf("User-Agent = %q, want minimal Codex CLI %q", got, latestCodexCLIUserAgentPrefix)
	}
	if got := req.Header.Get("Version"); got != latestCodexCLIVersion {
		t.Fatalf("Version = %q, want %q", got, latestCodexCLIVersion)
	}
}

func TestApplyCodexRequestHeadersPreservesOfficialClientHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	acc := &auth.Account{DBID: 42, AccountID: "acct-42"}
	downstreamHeaders := http.Header{
		"User-Agent":            []string{"codex_vscode/1.2.3"},
		"Originator":            []string{"codex_vscode"},
		"Version":               []string{"1.2.3"},
		"X-Codex-Turn-State":    []string{"turn-state"},
		"X-Codex-Turn-Metadata": []string{"turn-metadata"},
		"X-Client-Request-Id":   []string{"req-123"},
	}

	applyCodexRequestHeaders(req, acc, "token-123", "cache-key-1", "api-key-1", nil, downstreamHeaders)

	if got := req.Header.Get("User-Agent"); got != "codex_vscode/1.2.3" {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := req.Header.Get("Originator"); got != "codex_vscode" {
		t.Fatalf("Originator = %q", got)
	}
	if got := req.Header.Get("Version"); got != "1.2.3" {
		t.Fatalf("Version = %q", got)
	}
	for _, name := range []string{"X-Codex-Turn-State", "X-Codex-Turn-Metadata", "X-Client-Request-Id"} {
		if got := req.Header.Get(name); got != downstreamHeaders.Get(name) {
			t.Fatalf("%s = %q, want %q", name, got, downstreamHeaders.Get(name))
		}
	}
}

func TestApplyCodexRequestHeadersFallsBackForNonOfficialClient(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	acc := &auth.Account{DBID: 42}
	downstreamHeaders := http.Header{
		"User-Agent": []string{"curl/8.0"},
		"Originator": []string{"opencode"},
	}

	applyCodexRequestHeaders(req, acc, "token-123", "", "api-key-1", nil, downstreamHeaders)

	if got := req.Header.Get("User-Agent"); got != latestCodexCLIUserAgentPrefix {
		t.Fatalf("User-Agent = %q, want %q", got, latestCodexCLIUserAgentPrefix)
	}
	if got := req.Header.Get("Originator"); got != Originator {
		t.Fatalf("Originator = %q, want %q", got, Originator)
	}
	if got := req.Header.Get("Version"); got != latestCodexCLIVersion {
		t.Fatalf("Version = %q, want %q", got, latestCodexCLIVersion)
	}
}

func TestCodexTransportModeDefaultsToStandard(t *testing.T) {
	t.Setenv("CODEX_TRANSPORT_MODE", "")
	if _, ok := newCodexTransport("").(*http.Transport); !ok {
		t.Fatalf("newCodexTransport default = %T, want *http.Transport", newCodexTransport(""))
	}
}

func TestCodexTransportModeCanUseUTLSChrome(t *testing.T) {
	t.Setenv("CODEX_TRANSPORT_MODE", "utls_chrome")
	if _, ok := newCodexTransport("").(*utlsRoundTripper); !ok {
		t.Fatalf("newCodexTransport utls_chrome = %T, want *utlsRoundTripper", newCodexTransport(""))
	}
}

func TestClientPoolKeyIncludesTransportMode(t *testing.T) {
	acc := &auth.Account{DBID: 42}
	standard := clientPoolKey(acc, "http://proxy", codexTransportModeStandard)
	utlsChrome := clientPoolKey(acc, "http://proxy", codexTransportModeUTLSChrome)
	if standard == utlsChrome {
		t.Fatalf("clientPoolKey should include transport mode, got %q", standard)
	}
}

func TestIsolateCodexSessionIDUsesAPIKeyScope(t *testing.T) {
	raw := "session-1"
	if got := IsolateCodexSessionID(0, raw); got != raw {
		t.Fatalf("IsolateCodexSessionID without api key = %q, want %q", got, raw)
	}
	first := IsolateCodexSessionID(1, raw)
	second := IsolateCodexSessionID(2, raw)
	if first == raw || second == raw || first == second {
		t.Fatalf("expected distinct isolated session ids, got first=%q second=%q raw=%q", first, second, raw)
	}
}

func TestResolveSessionIDPrefersContinuityHeaders(t *testing.T) {
	headers := http.Header{
		"Session_id":      []string{"session-from-header"},
		"Conversation_id": []string{"conversation-from-header"},
		"Authorization":   []string{"Bearer sk-test-123"},
	}

	if got := ResolveSessionID(headers, []byte(`{"prompt_cache_key":"body-key"}`)); got != "session-from-header" {
		t.Fatalf("ResolveSessionID() = %q, want %q", got, "session-from-header")
	}

	headers.Del("Session_id")
	if got := ResolveSessionID(headers, []byte(`{"prompt_cache_key":"body-key"}`)); got != "conversation-from-header" {
		t.Fatalf("ResolveSessionID() = %q, want %q", got, "conversation-from-header")
	}

	headers.Del("Conversation_id")
	headers.Set("Idempotency-Key", "idempotency-key-1")
	if got := ResolveSessionID(headers, []byte(`{"prompt_cache_key":"body-key"}`)); got != "idempotency-key-1" {
		t.Fatalf("ResolveSessionID() = %q, want %q", got, "idempotency-key-1")
	}
}
