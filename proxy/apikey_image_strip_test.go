package proxy

import (
	"net/http"
	"testing"

	"github.com/codex2api/database"
	"github.com/tidwall/gjson"
)

// TestResolveImageGenerationPolicy 校验新旧配置的归一优先级。
func TestResolveImageGenerationPolicy(t *testing.T) {
	cases := []struct {
		name   string
		limits database.APIKeyLimits
		want   string
	}{
		{"empty", database.APIKeyLimits{}, database.ImageGenerationPolicyAllow},
		{"legacy bool → block", database.APIKeyLimits{DisableImageGeneration: true}, database.ImageGenerationPolicyBlock},
		{"explicit strip", database.APIKeyLimits{ImageGenerationPolicy: "strip"}, database.ImageGenerationPolicyStrip},
		{"explicit block", database.APIKeyLimits{ImageGenerationPolicy: "block"}, database.ImageGenerationPolicyBlock},
		{"explicit allow overrides legacy bool", database.APIKeyLimits{ImageGenerationPolicy: "allow", DisableImageGeneration: true}, database.ImageGenerationPolicyAllow},
		{"strip overrides legacy bool", database.APIKeyLimits{ImageGenerationPolicy: "strip", DisableImageGeneration: true}, database.ImageGenerationPolicyStrip},
		{"case insensitive", database.APIKeyLimits{ImageGenerationPolicy: "  STRIP "}, database.ImageGenerationPolicyStrip},
		{"unknown value falls back to legacy bool", database.APIKeyLimits{ImageGenerationPolicy: "nonsense", DisableImageGeneration: true}, database.ImageGenerationPolicyBlock},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.limits.ResolveImageGenerationPolicy(); got != tc.want {
				t.Fatalf("policy = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStripResponsesImageGenerationCapabilities 覆盖 issue #411 的剥离要求。
func TestStripResponsesImageGenerationCapabilities(t *testing.T) {
	t.Run("removes flat image_generation tool, keeps others", func(t *testing.T) {
		body := `{"model":"gpt-5.4","tools":[{"type":"image_generation"},{"type":"function","name":"foo"},{"type":"web_search"}]}`
		out := stripResponsesImageGenerationCapabilities([]byte(body))
		tools := gjson.GetBytes(out, "tools").Array()
		if len(tools) != 2 {
			t.Fatalf("expected 2 tools kept, got %d: %s", len(tools), out)
		}
		for _, tool := range tools {
			if tool.Get("type").String() == "image_generation" {
				t.Fatalf("image_generation not removed: %s", out)
			}
		}
	})

	t.Run("removes namespace image_gen tool", func(t *testing.T) {
		body := `{"model":"gpt-5.4","tools":[{"type":"namespace","name":"image_gen"},{"type":"namespace","name":"shell"}]}`
		out := stripResponsesImageGenerationCapabilities([]byte(body))
		tools := gjson.GetBytes(out, "tools").Array()
		if len(tools) != 1 || tools[0].Get("name").String() != "shell" {
			t.Fatalf("expected only shell namespace kept, got: %s", out)
		}
	})

	t.Run("removes tools key entirely when only image tool present", func(t *testing.T) {
		body := `{"model":"gpt-5.4","tools":[{"type":"image_generation"}]}`
		out := stripResponsesImageGenerationCapabilities([]byte(body))
		if gjson.GetBytes(out, "tools").Exists() {
			t.Fatalf("tools should be removed entirely, got: %s", out)
		}
	})

	t.Run("filters Responses Lite additional_tools", func(t *testing.T) {
		body := `{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":"hi"},{"type":"additional_tools","tools":[{"type":"image_generation"},{"type":"function","name":"foo"}]}]}`
		out := stripResponsesImageGenerationCapabilities([]byte(body))
		input := gjson.GetBytes(out, "input").Array()
		if len(input) != 2 {
			t.Fatalf("expected additional_tools carrier retained (non-empty), got %d items: %s", len(input), out)
		}
		nested := input[1].Get("tools").Array()
		if len(nested) != 1 || nested[0].Get("name").String() != "foo" {
			t.Fatalf("expected only function tool kept in carrier, got: %s", out)
		}
	})

	t.Run("removes empty Responses Lite additional_tools carrier", func(t *testing.T) {
		body := `{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":"hi"},{"type":"additional_tools","tools":[{"type":"namespace","name":"image_gen"}]}]}`
		out := stripResponsesImageGenerationCapabilities([]byte(body))
		input := gjson.GetBytes(out, "input").Array()
		if len(input) != 1 {
			t.Fatalf("expected empty carrier removed, got %d items: %s", len(input), out)
		}
		if input[0].Get("type").String() != "message" {
			t.Fatalf("expected message item retained, got: %s", out)
		}
	})

	t.Run("deletes tool_choice pointing at image tool", func(t *testing.T) {
		for _, body := range []string{
			`{"model":"gpt-5.4","tool_choice":{"type":"image_generation"}}`,
			`{"model":"gpt-5.4","tool_choice":"image_generation"}`,
			`{"model":"gpt-5.4","tool_choice":{"type":"namespace","name":"image_gen"}}`,
		} {
			out := stripResponsesImageGenerationCapabilities([]byte(body))
			if gjson.GetBytes(out, "tool_choice").Exists() {
				t.Fatalf("tool_choice should be removed for %s, got: %s", body, out)
			}
		}
	})

	t.Run("preserves tool_choice auto and other selections", func(t *testing.T) {
		for _, body := range []string{
			`{"model":"gpt-5.4","tool_choice":"auto"}`,
			`{"model":"gpt-5.4","tool_choice":"required"}`,
			`{"model":"gpt-5.4","tool_choice":{"type":"function","name":"foo"}}`,
		} {
			out := stripResponsesImageGenerationCapabilities([]byte(body))
			if !gjson.GetBytes(out, "tool_choice").Exists() {
				t.Fatalf("tool_choice should be preserved for %s, got: %s", body, out)
			}
		}
	})

	t.Run("no-op when no image capability present", func(t *testing.T) {
		body := `{"model":"gpt-5.4","input":"hello","tools":[{"type":"function","name":"foo"}],"tool_choice":"auto"}`
		out := stripResponsesImageGenerationCapabilities([]byte(body))
		if gjson.GetBytes(out, "tools").Array()[0].Get("name").String() != "foo" {
			t.Fatalf("function tool should survive untouched, got: %s", out)
		}
	})
}

// TestEnforceAPIKeyLimits_ImagePolicyBlock 校验 explicit block 策略仍触发 403，
// 而 strip 策略不在 enforce 层短路（放行，由转发前改写处理）。
func TestEnforceAPIKeyLimits_ImagePolicyBlockAndStrip(t *testing.T) {
	h := &Handler{}
	body := `{"model":"gpt-5.4","input":"hi","tools":[{"type":"image_generation"}]}`

	block := database.APIKeyLimits{ImageGenerationPolicy: "block"}
	c := newImageLimitCtx(t, "/v1/responses", body, block)
	if status, _ := h.enforceAPIKeyLimits(c, "gpt-5.4"); status != http.StatusForbidden {
		t.Fatalf("block policy should 403, got %d", status)
	}

	strip := database.APIKeyLimits{ImageGenerationPolicy: "strip"}
	c = newImageLimitCtx(t, "/v1/responses", body, strip)
	if status, msg := h.enforceAPIKeyLimits(c, "gpt-5.4"); status != 0 {
		t.Fatalf("strip policy should pass enforce (rewrite happens later), got %d (%q)", status, msg)
	}
}

// TestApplyImageGenerationStripPolicy 校验按 Key 策略选择性改写。
func TestApplyImageGenerationStripPolicy(t *testing.T) {
	body := `{"model":"gpt-5.4","tools":[{"type":"image_generation"},{"type":"function","name":"foo"}]}`

	t.Run("strip rewrites body", func(t *testing.T) {
		c := newImageLimitCtx(t, "/v1/responses", body, database.APIKeyLimits{ImageGenerationPolicy: "strip"})
		out := applyImageGenerationStripPolicy(c, []byte(body))
		if len(gjson.GetBytes(out, "tools").Array()) != 1 {
			t.Fatalf("strip should remove image tool, got: %s", out)
		}
	})

	t.Run("allow leaves body untouched", func(t *testing.T) {
		c := newImageLimitCtx(t, "/v1/responses", body, database.APIKeyLimits{})
		out := applyImageGenerationStripPolicy(c, []byte(body))
		if len(gjson.GetBytes(out, "tools").Array()) != 2 {
			t.Fatalf("allow should keep both tools, got: %s", out)
		}
	})

	t.Run("block leaves body untouched (handled by 403 earlier)", func(t *testing.T) {
		c := newImageLimitCtx(t, "/v1/responses", body, database.APIKeyLimits{ImageGenerationPolicy: "block"})
		out := applyImageGenerationStripPolicy(c, []byte(body))
		if len(gjson.GetBytes(out, "tools").Array()) != 2 {
			t.Fatalf("block should not strip, got: %s", out)
		}
	})
}
