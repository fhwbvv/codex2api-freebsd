package proxy

import (
	"testing"

	"github.com/tidwall/gjson"
)

// issue #450：Codex 压缩轮发 tools:[] + tool_choice:"auto"，Grok 上游 400
// "A tool_choice was set on the request but no tools were specified."
// 净化必须在没有工具声明时撤掉 tool_choice，有工具时一字不动。
func TestDropGrokToolChoiceWithoutTools(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantChoice bool
	}{
		{"空 tools 数组 + auto", `{"model":"grok-4.5","tools":[],"tool_choice":"auto"}`, false},
		{"无 tools 字段 + auto", `{"model":"grok-4.5","tool_choice":"auto"}`, false},
		{"空 tools + none", `{"model":"grok-4.5","tools":[],"tool_choice":"none"}`, false},
		{"空 tools + 指定函数", `{"model":"grok-4.5","tools":[],"tool_choice":{"type":"function","name":"shell"}}`, false},
		{"非空 tools + auto 保留", `{"model":"grok-4.5","tools":[{"type":"function","name":"shell"}],"tool_choice":"auto"}`, true},
		{"非空 tools + 指定函数保留", `{"model":"grok-4.5","tools":[{"type":"function","name":"shell"}],"tool_choice":{"type":"function","name":"shell"}}`, true},
		// 畸形形态（tools 不是数组）不属于本修复范围，保持原样交给上游报错。
		{"tools 非数组不动", `{"model":"grok-4.5","tools":{"type":"function"},"tool_choice":"auto"}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := dropGrokToolChoiceWithoutTools([]byte(c.in))
			if got := gjson.GetBytes(out, "tool_choice").Exists(); got != c.wantChoice {
				t.Fatalf("tool_choice 存在性 = %v, want %v (out=%s)", got, c.wantChoice, out)
			}
			// 不得顺手改动其他字段。
			if gjson.GetBytes(out, "model").String() != "grok-4.5" {
				t.Fatalf("model 被改动: %s", out)
			}
		})
	}

	// 无 tool_choice 的请求原样返回（零开销快速路径，且不得凭空注入）。
	body := []byte(`{"model":"grok-4.5","tools":[]}`)
	if out := dropGrokToolChoiceWithoutTools(body); string(out) != string(body) {
		t.Fatalf("无 tool_choice 时应原样返回, got %s", out)
	}
}

// 主入口也要带上这步，且不影响既有的字段剥离/降级。
func TestSanitizeGrokRequestBodyDropsOrphanToolChoice(t *testing.T) {
	out := sanitizeGrokRequestBody([]byte(`{"model":"grok-4.5","tools":[],"tool_choice":"auto","service_tier":"priority","reasoning":{"effort":"xhigh"}}`))
	if gjson.GetBytes(out, "tool_choice").Exists() {
		t.Fatalf("sanitize 应撤掉无工具时的 tool_choice: %s", out)
	}
	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("sanitize 应仍剥离 service_tier: %s", out)
	}
	if got := gjson.GetBytes(out, "reasoning.effort").String(); got != "high" {
		t.Fatalf("reasoning.effort = %q, want high", got)
	}
}

// 归一化把唯一的 web_search 工具整体丢弃后（external_web_access:false），
// 残留的 tool_choice:"auto" 也必须被撤掉——否则网关自己就造出同一个 400。
func TestGrokNormalizeThenSanitizeDropsChoiceAfterToolsEmptied(t *testing.T) {
	body := []byte(`{"model":"grok-4.5","tools":[{"type":"web_search","external_web_access":false}],"tool_choice":"auto"}`)
	normalized, _ := normalizeGrokUpstreamTools(body)
	if tools := gjson.GetBytes(normalized, "tools"); tools.IsArray() && len(tools.Array()) != 0 {
		t.Fatalf("web_search 应被整体丢弃: %s", normalized)
	}
	out := sanitizeGrokRequestBody(normalized)
	if gjson.GetBytes(out, "tool_choice").Exists() {
		t.Fatalf("工具被剥空后 tool_choice 应撤掉: %s", out)
	}
}
