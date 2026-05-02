package wsrelay

import (
	"net/http"
	"testing"

	"github.com/codex2api/proxy"
)

func TestPrepareWebsocketHeadersUsesConfiguredDefaultsAndBetaFeatures(t *testing.T) {
	t.Setenv("CODEX_WS_SEND_USER_AGENT", "true")
	exec := NewExecutor()
	cfg := &proxy.DeviceProfileConfig{
		UserAgent:              "codex_cli_rs/0.120.0 (Mac OS 15.5.0; arm64) Apple_Terminal/464",
		PackageVersion:         "0.120.0",
		RuntimeVersion:         "0.120.0",
		OS:                     "MacOS",
		Arch:                   "arm64",
		StabilizeDeviceProfile: true,
		BetaFeatures:           "multi_agent",
	}
	ginHeaders := http.Header{
		"Originator": []string{"custom-originator"},
	}

	headers := exec.prepareWebsocketHeaders("token-123", "42", "session-123", "api-key-1", cfg, ginHeaders)

	if got := headers.Get("Authorization"); got != "Bearer token-123" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := headers.Get("OpenAI-Beta"); got != responsesWebsocketBetaHeader {
		t.Fatalf("OpenAI-Beta = %q", got)
	}
	if got := headers.Get("X-Codex-Beta-Features"); got != "multi_agent" {
		t.Fatalf("X-Codex-Beta-Features = %q", got)
	}
	if got := headers.Get("User-Agent"); got != cfg.UserAgent {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := headers.Get("Version"); got != "0.120.0" {
		t.Fatalf("Version = %q", got)
	}
	if got := headers.Get("Originator"); got != proxy.Originator {
		t.Fatalf("Originator = %q", got)
	}
	if got := headers.Get("Chatgpt-Account-Id"); got != "42" {
		t.Fatalf("Chatgpt-Account-Id = %q", got)
	}
	if got := headers.Get("Conversation_id"); got != "session-123" {
		t.Fatalf("Conversation_id = %q", got)
	}
	if got := headers.Get("Session_id"); got != "session-123" {
		t.Fatalf("Session_id = %q", got)
	}
}

func TestPrepareWebsocketHeadersOmitsUserAgentByDefault(t *testing.T) {
	exec := NewExecutor()
	ginHeaders := http.Header{
		"X-Codex-Turn-State":                    []string{"turn-state"},
		"X-Codex-Turn-Metadata":                 []string{"turn-metadata"},
		"X-Client-Request-Id":                   []string{"req-123"},
		"X-Responsesapi-Include-Timing-Metrics": []string{"true"},
	}

	headers := exec.prepareWebsocketHeaders("token-123", "42", "session-123", "api-key-1", nil, ginHeaders)

	if got := headers.Get("User-Agent"); got != "" {
		t.Fatalf("User-Agent = %q, want empty", got)
	}
	if got := headers.Get("Version"); got != "" {
		t.Fatalf("Version = %q, want empty", got)
	}
	if got := headers.Get("OpenAI-Beta"); got != responsesWebsocketBetaHeader {
		t.Fatalf("OpenAI-Beta = %q", got)
	}
	for _, name := range []string{"X-Codex-Turn-State", "X-Codex-Turn-Metadata", "X-Client-Request-Id", "X-Responsesapi-Include-Timing-Metrics"} {
		if got := headers.Get(name); got != ginHeaders.Get(name) {
			t.Fatalf("%s = %q, want %q", name, got, ginHeaders.Get(name))
		}
	}
	if got := headers.Get("Session_id"); got != "session-123" {
		t.Fatalf("Session_id = %q", got)
	}
	if got := headers.Get("Conversation_id"); got != "session-123" {
		t.Fatalf("Conversation_id = %q", got)
	}
}
