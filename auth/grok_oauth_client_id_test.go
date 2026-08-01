package auth

import "testing"

// EffectiveGrokOAuthClientID 默认回退到 GrokDefaultOAuthClientID，设 env 后覆盖。
// 这些用例直接钉住 PR 卖点：默认行为零变化 + env 非空即覆盖。
func TestEffectiveGrokOAuthClientID_DefaultIsUnchanged(t *testing.T) {
	t.Setenv(EnvGrokOAuthClientID, "")
	if got := EffectiveGrokOAuthClientID(); got != GrokDefaultOAuthClientID {
		t.Fatalf("default client_id drift: got %q, want %q", got, GrokDefaultOAuthClientID)
	}
}

func TestEffectiveGrokOAuthClientID_EnvOverridesDefault(t *testing.T) {
	t.Setenv(EnvGrokOAuthClientID, "test-client-id-1234")
	if got := EffectiveGrokOAuthClientID(); got != "test-client-id-1234" {
		t.Fatalf("env client_id not honored: got %q, want %q", got, "test-client-id-1234")
	}
}

// env 为纯空白时同样回退默认，保持“非空即用”的契约。
func TestEffectiveGrokOAuthClientID_WhitespaceFallsBack(t *testing.T) {
	t.Setenv(EnvGrokOAuthClientID, "   \t  ")
	if got := EffectiveGrokOAuthClientID(); got != GrokDefaultOAuthClientID {
		t.Fatalf("whitespace-only client_id should fall back: got %q, want %q", got, GrokDefaultOAuthClientID)
	}
}
