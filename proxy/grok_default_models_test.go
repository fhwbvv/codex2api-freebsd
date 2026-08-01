package proxy

import (
	"testing"

	"github.com/codex2api/auth"
)

// TestDefaultGrokModelIDsForAccountByAuthKind 守护两条通道目录不同这一实测事实：
// OAuth 走 cli-chat-proxy，实测 supergrok_heavy 与 free 两种套餐的 /v1/models
// 都只返回 grok-4.5；API Key 走 xAI 公开 API，目录更宽。
func TestDefaultGrokModelIDsForAccountByAuthKind(t *testing.T) {
	oauth := &auth.Account{UpstreamType: auth.UpstreamGrok, RefreshToken: "rt"}
	if got := DefaultGrokModelIDsForAccount(oauth); len(got) != 1 || got[0] != "grok-4.5" {
		t.Fatalf("OAuth 默认集 = %v, want [grok-4.5]", got)
	}

	apiKey := &auth.Account{UpstreamType: auth.UpstreamGrok, APIKey: "xai-key"}
	if apiKey.GrokAuthKind() != auth.GrokAuthKindAPIKey {
		t.Fatalf("构造的账号应被识别为 API Key，实际 %q", apiKey.GrokAuthKind())
	}
	got := DefaultGrokModelIDsForAccount(apiKey)
	if len(got) <= 1 {
		t.Fatalf("API Key 默认集应比 OAuth 宽, got %v", got)
	}
	if !modelIDInList("grok-3", got) {
		t.Fatalf("API Key 默认集应含 grok-3, got %v", got)
	}

	// 空账号按 OAuth 处理：CLI 通道是更保守的一侧，宁可少放行也不要advertise 不存在的模型。
	if nilGot := DefaultGrokModelIDsForAccount(nil); len(nilGot) != 1 {
		t.Fatalf("空账号应回落到最保守的 OAuth 默认集, got %v", nilGot)
	}
}

// TestRelayAccountSupportsModelHonoursAuthKind OAuth 账号不再被放行到 CLI 通道
// 不存在的模型（grok-3 等），避免调度到必然失败的账号上。
func TestRelayAccountSupportsModelHonoursAuthKind(t *testing.T) {
	oauth := &auth.Account{UpstreamType: auth.UpstreamGrok, RefreshToken: "rt"}
	if !relayAccountSupportsModel(oauth, "grok-4.5") {
		t.Fatalf("OAuth 账号应支持 grok-4.5")
	}
	for _, model := range []string{"grok-3", "grok-2", "grok-3-fast"} {
		if relayAccountSupportsModel(oauth, model) {
			t.Errorf("OAuth 账号不应被放行到 %s（CLI 通道无此模型）", model)
		}
	}

	apiKey := &auth.Account{UpstreamType: auth.UpstreamGrok, APIKey: "xai-key"}
	if !relayAccountSupportsModel(apiKey, "grok-3") {
		t.Fatalf("API Key 账号应仍支持 grok-3（公开 API 目录）")
	}
}

// TestRelayAccountSupportsModelRespectsDeclaredWhitelist 账号显式声明 models 后
// 以白名单为准，默认集不再介入。
func TestRelayAccountSupportsModelRespectsDeclaredWhitelist(t *testing.T) {
	declared := &auth.Account{
		UpstreamType: auth.UpstreamGrok,
		RefreshToken: "rt",
		Models:       []string{"grok-3"},
	}
	if !relayAccountSupportsModel(declared, "grok-3") {
		t.Fatalf("声明了 grok-3 应被放行")
	}
	if relayAccountSupportsModel(declared, "grok-4.5") {
		t.Fatalf("声明白名单后不应再补默认集放行 grok-4.5")
	}
}
