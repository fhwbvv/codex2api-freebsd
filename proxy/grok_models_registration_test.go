package proxy

import (
	"context"
	"strings"
	"testing"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
)

// 未声明 models 白名单的 Grok 账号应把默认 Grok 模型集(含 grok-4.5)注册进 /v1/models。
func TestSupportedModelIDsIncludesDefaultGrok(t *testing.T) {
	store := auth.NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 2})
	store.AddAccount(&auth.Account{DBID: 1, APIKey: "xai-1", UpstreamType: auth.UpstreamGrok})
	h := NewHandler(store, nil, nil, nil)

	ids := h.supportedModelIDs(context.Background())
	if !containsFold(ids, "grok-4.5") {
		t.Fatalf("grok-4.5 应出现在 /v1/models，实际: %v", ids)
	}
}

// 显式声明 models 的 Grok 账号以白名单为准，不再补默认集。
func TestSupportedModelIDsRespectsDeclaredGrokModels(t *testing.T) {
	store := auth.NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 2})
	store.AddAccount(&auth.Account{DBID: 1, APIKey: "xai-1", UpstreamType: auth.UpstreamGrok, Models: []string{"grok-4"}})
	h := NewHandler(store, nil, nil, nil)

	ids := h.supportedModelIDs(context.Background())
	if !containsFold(ids, "grok-4") {
		t.Fatalf("声明的 grok-4 应在列表，实际: %v", ids)
	}
	if containsFold(ids, "grok-4.5") {
		t.Fatalf("已声明白名单不应再补默认集(grok-4.5)，实际: %v", ids)
	}
}

func containsFold(list []string, target string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), target) {
			return true
		}
	}
	return false
}

// 通用 Key(无渠道限定)应能把默认 Grok 模型调度到未声明白名单的 Grok 账号,
// 与 /v1/models 注册的默认集一致;非默认集模型(gpt-*)不得误入 Grok 账号。
func TestRelayAccountSupportsModel_GrokDefaultSet(t *testing.T) {
	undeclared := &auth.Account{DBID: 1, APIKey: "xai-1", UpstreamType: auth.UpstreamGrok}
	declared := &auth.Account{DBID: 2, APIKey: "xai-2", UpstreamType: auth.UpstreamGrok, Models: []string{"grok-4"}}
	relay := &auth.Account{DBID: 3, APIKey: "sk-relay", BaseURL: "https://relay.example.com", UpstreamType: auth.UpstreamOpenAIResponses}

	cases := []struct {
		name    string
		account *auth.Account
		model   string
		want    bool
	}{
		{"undeclared grok serves default-set model", undeclared, "grok-4.5", true},
		{"undeclared grok serves grok-4", undeclared, "grok-4", true},
		{"undeclared grok rejects non-grok model", undeclared, "gpt-5.5", false},
		{"declared grok honors whitelist hit", declared, "grok-4", true},
		{"declared grok rejects default-set model outside whitelist", declared, "grok-4.5", false},
		{"relay without whitelist stays unschedulable", relay, "grok-4.5", false},
		{"nil account", nil, "grok-4.5", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := relayAccountSupportsModel(tc.account, tc.model); got != tc.want {
				t.Fatalf("relayAccountSupportsModel(%s) = %t, want %t", tc.model, got, tc.want)
			}
		})
	}
}

// 端到端过滤器语义:通用 Key 的 responses 过滤器应选中未声明白名单的 Grok 账号。
func TestAccountFilterForResponsesModel_GenericKeyReachesUndeclaredGrok(t *testing.T) {
	account := &auth.Account{DBID: 1, APIKey: "xai-1", UpstreamType: auth.UpstreamGrok}
	filter := accountFilterForResponsesModel("grok-4.5", false)
	if !filter(account) {
		t.Fatal("generic-key filter should select undeclared grok account for grok-4.5")
	}
	if filter(&auth.Account{DBID: 2, APIKey: "xai-2", UpstreamType: auth.UpstreamGrok, Models: []string{"grok-4"}}) {
		t.Fatal("declared whitelist without grok-4.5 must not match")
	}
	gptFilter := accountFilterForResponsesModel("gpt-5.5", false)
	if gptFilter(account) {
		t.Fatal("gpt-5.5 must not route to grok accounts")
	}
}
