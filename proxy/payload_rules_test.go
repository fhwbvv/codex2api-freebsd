package proxy

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func mustParseRules(t *testing.T, raw string) *PayloadRuleSet {
	t.Helper()
	rs, err := ParsePayloadRulesJSON(raw)
	if err != nil {
		t.Fatalf("ParsePayloadRulesJSON: %v", err)
	}
	return rs
}

func withPayloadRules(t *testing.T, raw string) {
	t.Helper()
	prev := CurrentPayloadRules()
	if err := SetPayloadRulesJSON(raw); err != nil {
		t.Fatalf("SetPayloadRulesJSON: %v", err)
	}
	t.Cleanup(func() {
		if prev == nil {
			prev = &PayloadRuleSet{}
		}
		currentPayloadRuleSet.Store(prev)
	})
}

const payloadTestBody = `{"model":"gpt-5.6-sol","stream":true,"instructions":"official prompt","reasoning":{"effort":"medium"},"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`

func TestPayloadRulesOverrideInstructions(t *testing.T) {
	withPayloadRules(t, `{"override":[{"models":["gpt-*"],"params":{"instructions":"my custom prompt"}}]}`)
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, nil)
	if got := gjson.GetBytes(out, "instructions").String(); got != "my custom prompt" {
		t.Fatalf("instructions = %q, want my custom prompt", got)
	}
}

func TestPayloadRulesAppendInstructions(t *testing.T) {
	withPayloadRules(t, `{"append":[{"params":{"instructions":"extra guard text"}}]}`)
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, nil)
	want := "official prompt\n\nextra guard text"
	if got := gjson.GetBytes(out, "instructions").String(); got != want {
		t.Fatalf("instructions = %q, want %q", got, want)
	}
	// 缺失时直接写入
	out = ApplyPayloadRulesToBody([]byte(`{"model":"gpt-5.6-sol"}`), "gpt-5.6-sol", nil, nil)
	if got := gjson.GetBytes(out, "instructions").String(); got != "extra guard text" {
		t.Fatalf("instructions(missing) = %q, want extra guard text", got)
	}
}

func TestPayloadRulesConditionalEffortMapping(t *testing.T) {
	withPayloadRules(t, `{"override":[{"models":["gpt-*"],"match":{"reasoning.effort":"medium"},"params":{"reasoning.effort":"high"}}]}`)
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, nil)
	if got := gjson.GetBytes(out, "reasoning.effort").String(); got != "high" {
		t.Fatalf("reasoning.effort = %q, want high", got)
	}
	// 不满足 match 门时不改写
	low := []byte(strings.Replace(payloadTestBody, `"medium"`, `"low"`, 1))
	out = ApplyPayloadRulesToBody(low, "gpt-5.6-sol", nil, nil)
	if got := gjson.GetBytes(out, "reasoning.effort").String(); got != "low" {
		t.Fatalf("reasoning.effort = %q, want low（match 门不满足）", got)
	}
}

func TestPayloadRulesServiceTierOverride(t *testing.T) {
	withPayloadRules(t, `{"override":[{"params":{"service_tier":"priority"}}]}`)
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, nil)
	if got := gjson.GetBytes(out, "service_tier").String(); got != "priority" {
		t.Fatalf("service_tier = %q, want priority", got)
	}
}

func TestPayloadRulesServiceTierSanitizedAfterRewrite(t *testing.T) {
	// 规则注入的 flex/auto 等上游不接受的层级须在发出前被净化剔除（issue #395），
	// 否则会绕过 handler 层的净化直达上游触发 400。fast 则应映射为 priority。
	for tier, want := range map[string]string{"flex": "", "auto": "", "scale": "", "default": "", "fast": "priority", "priority": "priority"} {
		withPayloadRules(t, `{"override":[{"params":{"service_tier":"`+tier+`"}}]}`)
		out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, nil)
		out = sanitizeServiceTierForUpstream(out)
		if got := gjson.GetBytes(out, "service_tier").String(); got != want {
			t.Fatalf("tier %s: service_tier = %q, want %q", tier, got, want)
		}
	}
}

func TestPayloadRulesDefaultOnlyWhenMissing(t *testing.T) {
	withPayloadRules(t, `{"default":[{"params":{"text.verbosity":"low","instructions":"default prompt"}}]}`)
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, nil)
	if got := gjson.GetBytes(out, "text.verbosity").String(); got != "low" {
		t.Fatalf("text.verbosity = %q, want low（缺失时写入）", got)
	}
	if got := gjson.GetBytes(out, "instructions").String(); got != "official prompt" {
		t.Fatalf("instructions = %q, want official prompt（已存在不覆盖）", got)
	}
}

func TestPayloadRulesFilterRemovesField(t *testing.T) {
	withPayloadRules(t, `{"filter":[{"params":["reasoning.effort"]}]}`)
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, nil)
	if gjson.GetBytes(out, "reasoning.effort").Exists() {
		t.Fatalf("reasoning.effort 应被删除")
	}
}

func TestPayloadRulesOverrideRaw(t *testing.T) {
	withPayloadRules(t, `{"override_raw":[{"params":{"text":"{\"verbosity\":\"high\"}"}}]}`)
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, nil)
	if got := gjson.GetBytes(out, "text.verbosity").String(); got != "high" {
		t.Fatalf("text.verbosity = %q, want high", got)
	}
}

func TestPayloadRulesHeaderGate(t *testing.T) {
	withPayloadRules(t, `{"override":[{"headers":{"Originator":"codex_cli*"},"params":{"service_tier":"priority"}}]}`)
	h := http.Header{}
	h.Set("Originator", "codex_cli_rs")
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", h, nil)
	if got := gjson.GetBytes(out, "service_tier").String(); got != "priority" {
		t.Fatalf("service_tier = %q, want priority（头匹配）", got)
	}
	out = ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", http.Header{}, nil)
	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("头不匹配时不应改写")
	}
}

func TestPayloadRulesModelGate(t *testing.T) {
	withPayloadRules(t, `{"override":[{"models":["gpt-5.5*"],"params":{"service_tier":"priority"}}]}`)
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, nil)
	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("模型不匹配时不应改写")
	}
}

func TestPayloadRulesExistNotExistGates(t *testing.T) {
	withPayloadRules(t, `{"override":[{"exist":["reasoning.effort"],"not_exist":["metadata.skip"],"params":{"service_tier":"flex"}}]}`)
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, nil)
	if got := gjson.GetBytes(out, "service_tier").String(); got != "flex" {
		t.Fatalf("service_tier = %q, want flex", got)
	}
	skip := []byte(strings.Replace(payloadTestBody, `"stream":true`, `"stream":true,"metadata":{"skip":1}`, 1))
	out = ApplyPayloadRulesToBody(skip, "gpt-5.6-sol", nil, nil)
	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("not_exist 门命中时不应改写")
	}
}

func TestPayloadRulesProtectedPathsRejected(t *testing.T) {
	for _, raw := range []string{
		`{"override":[{"params":{"model":"gpt-4"}}]}`,
		`{"override":[{"params":{"stream":false}}]}`,
		`{"override":[{"params":{"input.0.content":"x"}}]}`,
		`{"filter":[{"params":["prompt_cache_key"]}]}`,
		`{"append":[{"params":{"store":"x"}}]}`,
	} {
		if _, err := ParsePayloadRulesJSON(raw); err == nil {
			t.Fatalf("保护字段应被拒绝: %s", raw)
		}
	}
}

func TestPayloadRulesInvalidJSONRejected(t *testing.T) {
	for _, raw := range []string{
		`{"override":[{"params":{}}]}`,
		`{"override_raw":[{"params":{"text":"not-json{"}}]}`,
		`{"filter":[{"params":["  "]}]}`,
		`{"unknown_group":[]}`,
		`not json`,
	} {
		if _, err := ParsePayloadRulesJSON(raw); err == nil {
			t.Fatalf("非法配置应被拒绝: %s", raw)
		}
	}
	// 空配置合法
	if rs := mustParseRules(t, ""); !rs.IsEmpty() {
		t.Fatalf("空串应解析为空规则集")
	}
	if rs := mustParseRules(t, "{}"); !rs.IsEmpty() {
		t.Fatalf("{} 应解析为空规则集")
	}
}

func TestPayloadRulesNormalize(t *testing.T) {
	normalized, err := NormalizePayloadRulesJSON(` {"override":[{"models":["gpt-*"],"params":{"service_tier":"priority"}}]} `)
	if err != nil {
		t.Fatalf("NormalizePayloadRulesJSON: %v", err)
	}
	if !gjson.Valid(normalized) || !gjson.Get(normalized, "override.0.params.service_tier").Exists() {
		t.Fatalf("normalized = %q", normalized)
	}
	if got, _ := NormalizePayloadRulesJSON(""); got != "{}" {
		t.Fatalf("空配置应归一化为 {}, got %q", got)
	}
}

func TestMatchPayloadWildcard(t *testing.T) {
	cases := []struct {
		pattern, value string
		want           bool
	}{
		{"gpt-*", "gpt-5.6-sol", true},
		{"gpt-*", "GPT-5.6", true},
		{"*", "anything", true},
		{"gpt-5.6-sol", "gpt-5.6-sol", true},
		{"gpt-5.6-sol", "gpt-5.6", false},
		{"*-sol", "gpt-5.6-sol", true},
		{"*-sol", "gpt-5.6", false},
		{"gpt-*-sol", "gpt-5.6-sol", true},
		{"", "x", false},
	}
	for _, tc := range cases {
		if got := matchPayloadWildcard(tc.pattern, tc.value); got != tc.want {
			t.Errorf("matchPayloadWildcard(%q, %q) = %v, want %v", tc.pattern, tc.value, got, tc.want)
		}
	}
}

func TestPayloadRulesApplyOrder(t *testing.T) {
	// override 先覆盖，append 再基于覆盖后的值追加
	withPayloadRules(t, `{"override":[{"params":{"instructions":"base"}}],"append":[{"params":{"instructions":"tail"}}]}`)
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, nil)
	if got := gjson.GetBytes(out, "instructions").String(); got != "base\n\ntail" {
		t.Fatalf("instructions = %q, want base\\n\\ntail", got)
	}
}

func TestPayloadRulesAPIKeyGate(t *testing.T) {
	withPayloadRules(t, `{"override":[{"api_key_names":["fast*"],"params":{"service_tier":"priority"}}]}`)
	fast := &PayloadRuleIdentity{APIKeyID: 7, APIKeyName: "fast-team"}
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, fast)
	if got := gjson.GetBytes(out, "service_tier").String(); got != "priority" {
		t.Fatalf("service_tier = %q, want priority（key 名匹配）", got)
	}
	// 名不匹配 → 不改写
	slow := &PayloadRuleIdentity{APIKeyID: 8, APIKeyName: "slow-team"}
	out = ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, slow)
	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("key 名不匹配时不应改写")
	}
	// 无身份 → fail-closed，不改写
	out = ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, nil)
	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("无身份时带 key 门的规则应 fail-closed 不改写")
	}
}

func TestPayloadRulesAPIKeyIDGate(t *testing.T) {
	withPayloadRules(t, `{"override":[{"api_key_ids":["7","3*"],"params":{"service_tier":"priority"}}]}`)
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, &PayloadRuleIdentity{APIKeyID: 7})
	if got := gjson.GetBytes(out, "service_tier").String(); got != "priority" {
		t.Fatalf("service_tier = %q, want priority（key id 精确匹配）", got)
	}
	out = ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, &PayloadRuleIdentity{APIKeyID: 31})
	if got := gjson.GetBytes(out, "service_tier").String(); got != "priority" {
		t.Fatalf("service_tier = %q, want priority（key id 通配 3*）", got)
	}
	out = ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, &PayloadRuleIdentity{APIKeyID: 9})
	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("key id 不匹配时不应改写")
	}
}

func TestPayloadRulesGroupGate(t *testing.T) {
	withPayloadRules(t, `{"override":[{"group_names":["fast*"],"params":{"service_tier":"priority"}}]}`)
	// 组名任一命中即通过
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil,
		&PayloadRuleIdentity{APIKeyID: 1, GroupIDs: []int64{2, 5}, GroupNames: []string{"slow", "fast-pool"}})
	if got := gjson.GetBytes(out, "service_tier").String(); got != "priority" {
		t.Fatalf("service_tier = %q, want priority（组名之一匹配）", got)
	}
	// 无组名命中 → 不改写
	out = ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil,
		&PayloadRuleIdentity{APIKeyID: 1, GroupNames: []string{"slow", "bulk"}})
	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("组名不匹配时不应改写")
	}
}

func TestPayloadRulesGroupIDGate(t *testing.T) {
	withPayloadRules(t, `{"override":[{"group_ids":["5"],"params":{"service_tier":"priority"}}]}`)
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil,
		&PayloadRuleIdentity{APIKeyID: 1, GroupIDs: []int64{2, 5}})
	if got := gjson.GetBytes(out, "service_tier").String(); got != "priority" {
		t.Fatalf("service_tier = %q, want priority（组 id 命中）", got)
	}
	out = ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil,
		&PayloadRuleIdentity{APIKeyID: 1, GroupIDs: []int64{2, 3}})
	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("组 id 不匹配时不应改写")
	}
}

func TestPayloadRulesIdentityAndModelGatesCombine(t *testing.T) {
	// 身份门与模型门 AND：两者都满足才改写
	withPayloadRules(t, `{"override":[{"models":["gpt-*"],"api_key_names":["fast*"],"params":{"service_tier":"priority"}}]}`)
	fast := &PayloadRuleIdentity{APIKeyName: "fast-1"}
	out := ApplyPayloadRulesToBody([]byte(payloadTestBody), "gpt-5.6-sol", nil, fast)
	if got := gjson.GetBytes(out, "service_tier").String(); got != "priority" {
		t.Fatalf("service_tier = %q, want priority（模型+key 都匹配）", got)
	}
	// 模型不匹配 → 不改写，即使 key 匹配
	out = ApplyPayloadRulesToBody([]byte(payloadTestBody), "claude-3", nil, fast)
	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("模型门不满足时不应改写")
	}
}

func TestPayloadRulesGatesParseRoundTrip(t *testing.T) {
	raw := `{"override":[{"api_key_ids":["1"],"api_key_names":["fast*"],"group_ids":["5"],"group_names":["fast"],"params":{"service_tier":"priority"}}]}`
	rs := mustParseRules(t, raw)
	if len(rs.Override) != 1 {
		t.Fatalf("override 规则数 = %d, want 1", len(rs.Override))
	}
	r := rs.Override[0]
	if len(r.APIKeyIDs) != 1 || len(r.APIKeyNames) != 1 || len(r.GroupIDs) != 1 || len(r.GroupNames) != 1 {
		t.Fatalf("身份门字段解析不全: %+v", r)
	}
}

func TestEffectiveRequestedServiceTier(t *testing.T) {
	withPayloadRules(t, `{"override":[{"api_key_names":["fast*"],"params":{"service_tier":"priority"}}]}`)
	body := []byte(`{"model":"gpt-5.6-sol","service_tier":"default","input":[]}`)
	// 命中 → 返回覆写后的值
	got := EffectiveRequestedServiceTier(body, "gpt-5.6-sol", nil, &PayloadRuleIdentity{APIKeyName: "fast-1"})
	if got != "priority" {
		t.Fatalf("EffectiveRequestedServiceTier = %q, want priority", got)
	}
	// 未命中 → 返回原值
	got = EffectiveRequestedServiceTier(body, "gpt-5.6-sol", nil, &PayloadRuleIdentity{APIKeyName: "slow-1"})
	if got != "default" {
		t.Fatalf("EffectiveRequestedServiceTier(未命中) = %q, want default", got)
	}
	// 无身份 fail-closed → 返回原值
	got = EffectiveRequestedServiceTier(body, "gpt-5.6-sol", nil, nil)
	if got != "default" {
		t.Fatalf("EffectiveRequestedServiceTier(无身份) = %q, want default", got)
	}
}

func TestWithPayloadRuleIdentityRoundTrip(t *testing.T) {
	id := &PayloadRuleIdentity{APIKeyID: 42, APIKeyName: "k", GroupIDs: []int64{1}, GroupNames: []string{"g"}}
	ctx := WithPayloadRuleIdentity(context.Background(), id)
	if got := PayloadRuleIdentityFromContext(ctx); got != id {
		t.Fatalf("从 context 取回身份不一致: %+v", got)
	}
	// nil 身份不写入
	ctx2 := WithPayloadRuleIdentity(context.Background(), nil)
	if PayloadRuleIdentityFromContext(ctx2) != nil {
		t.Fatalf("nil 身份不应写入 context")
	}
	// 空 context 返回 nil
	if PayloadRuleIdentityFromContext(context.Background()) != nil {
		t.Fatalf("无身份 context 应返回 nil")
	}
}
