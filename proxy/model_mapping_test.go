package proxy

import (
	"testing"

	"github.com/codex2api/auth"
	"github.com/tidwall/gjson"
)

func TestResolveConfiguredModelMappingExactAndWildcard(t *testing.T) {
	mapping := `{
		"gpt-*": "gpt-5.5",
		"gpt-5.4": "gpt-5.2",
		"*-mini": "gpt-5.4-mini",
		"*codex*": "gpt-5.3-codex"
	}`
	supported := []string{"gpt-5.5", "gpt-5.4-mini", "gpt-5.3-codex", "gpt-5.2"}

	tests := []struct {
		name  string
		model string
		want  string
	}{
		{name: "exact beats wildcard", model: "gpt-5.4", want: "gpt-5.2"},
		{name: "prefix wildcard", model: "gpt-5.1", want: "gpt-5.5"},
		{name: "suffix wildcard", model: "custom-mini", want: "gpt-5.4-mini"},
		{name: "substring wildcard", model: "my-codex-alias", want: "gpt-5.3-codex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolveConfiguredModelMapping(tt.model, mapping, supported)
			if !ok {
				t.Fatalf("expected mapping for %q", tt.model)
			}
			if got != tt.want {
				t.Fatalf("resolveConfiguredModelMapping(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestResolveConfiguredModelMappingSpecificWildcardWins(t *testing.T) {
	mapping := `{
		"gpt-*": "gpt-5.5",
		"gpt-5.4-*": "gpt-5.4-mini"
	}`
	got, ok := resolveConfiguredModelMapping("gpt-5.4-preview", mapping, []string{"gpt-5.5", "gpt-5.4-mini"})
	if !ok {
		t.Fatal("expected wildcard mapping")
	}
	if got != "gpt-5.4-mini" {
		t.Fatalf("mapped model = %q, want gpt-5.4-mini", got)
	}
}

func TestResolveConfiguredModelMappingCanonicalizesTargetAlias(t *testing.T) {
	got, ok := resolveConfiguredModelMapping("legacy-model", `{"legacy-model":"gpt5.5"}`, []string{"gpt-5.5"})
	if !ok {
		t.Fatal("expected exact mapping")
	}
	if got != "gpt-5.5" {
		t.Fatalf("mapped model = %q, want gpt-5.5", got)
	}

	got, ok = resolveConfiguredModelMapping("legacy-model", `{"legacy-model":"GPT-5.5"}`, []string{"gpt-5.5"})
	if !ok {
		t.Fatal("expected exact mapping")
	}
	if got != "gpt-5.5" {
		t.Fatalf("case-normalized mapped model = %q, want gpt-5.5", got)
	}
}

func TestResolveConfiguredModelMappingIgnoresInvalidJSON(t *testing.T) {
	got, ok := resolveConfiguredModelMapping("gpt-5.4", `{bad json`, []string{"gpt-5.5"})
	if ok {
		t.Fatal("invalid JSON should not map")
	}
	if got != "gpt-5.4" {
		t.Fatalf("model = %q, want original", got)
	}
}

func TestResolveAnthropicModelUsesWildcardBeforeDefaultFallback(t *testing.T) {
	got := resolveAnthropicModel("claude-opus-4-7", `{"claude-opus-*":"gpt-5.5"}`, []string{"gpt-5.5", "gpt-5.4"})
	if got != "gpt-5.5" {
		t.Fatalf("resolveAnthropicModel wildcard = %q, want gpt-5.5", got)
	}

	got = resolveAnthropicModel("claude-haiku-4-5", `{}`, []string{"gpt-5.4-mini"})
	if got != "gpt-5.4-mini" {
		t.Fatalf("resolveAnthropicModel default = %q, want gpt-5.4-mini", got)
	}
}

func TestApplyConfiguredModelMappingToBodyRewritesBeforeValidation(t *testing.T) {
	store := auth.NewStore(nil, nil, nil)
	store.SetCodexModelMapping(`{"gpt-legacy-*":"gpt-5.5"}`)
	handler := NewHandler(store, nil, nil, nil)

	body, original, effective, mapped := handler.applyConfiguredModelMappingToBody(
		[]byte(`{"model":"gpt-legacy-1","input":"hello"}`),
		[]string{"gpt-5.5"},
	)
	if !mapped {
		t.Fatal("expected body model to be mapped")
	}
	if original != "gpt-legacy-1" || effective != "gpt-5.5" {
		t.Fatalf("original/effective = %q/%q, want gpt-legacy-1/gpt-5.5", original, effective)
	}
	if got := gjson.GetBytes(body, "model").String(); got != "gpt-5.5" {
		t.Fatalf("body model = %q, want gpt-5.5; body=%s", got, body)
	}
}

func TestApplyReasoningEffortModelAliasToBody(t *testing.T) {
	store := auth.NewStore(nil, nil, nil)
	store.SetReasoningEffortModels(`[{"model":"gpt-5.5","effort":"xhigh"}]`)
	handler := NewHandler(store, nil, nil, nil)

	body, original, effective, mapped := handler.applyConfiguredModelMappingToBody(
		[]byte(`{"model":"gpt-5.5(xhigh)","input":"hello"}`),
		[]string{"gpt-5.5", "gpt-5.5(xhigh)"},
	)
	if !mapped {
		t.Fatal("expected alias to be resolved")
	}
	if original != "gpt-5.5(xhigh)" || effective != "gpt-5.5" {
		t.Fatalf("original/effective = %q/%q, want gpt-5.5(xhigh)/gpt-5.5", original, effective)
	}
	if got := gjson.GetBytes(body, "model").String(); got != "gpt-5.5" {
		t.Fatalf("body model = %q, want gpt-5.5; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "reasoning_effort").String(); got != "xhigh" {
		t.Fatalf("reasoning_effort = %q, want xhigh; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "reasoning.effort").String(); got != "xhigh" {
		t.Fatalf("reasoning.effort = %q, want xhigh; body=%s", got, body)
	}
}

func TestApplyReasoningEffortModelAliasBeforeCodexMapping(t *testing.T) {
	store := auth.NewStore(nil, nil, nil)
	store.SetReasoningEffortModels(`[{"model":"gpt-5.5","effort":"xhigh"}]`)
	store.SetCodexModelMapping(`{"gpt-5.5":"gpt-5.4"}`)
	handler := NewHandler(store, nil, nil, nil)

	body, original, effective, mapped := handler.applyConfiguredModelMappingToBody(
		[]byte(`{"model":"gpt-5.5(xhigh)","input":"hello"}`),
		[]string{"gpt-5.5", "gpt-5.4", "gpt-5.5(xhigh)"},
	)
	if !mapped {
		t.Fatal("expected alias and mapping to be applied")
	}
	if original != "gpt-5.5(xhigh)" || effective != "gpt-5.4" {
		t.Fatalf("original/effective = %q/%q, want gpt-5.5(xhigh)/gpt-5.4", original, effective)
	}
	if got := gjson.GetBytes(body, "model").String(); got != "gpt-5.4" {
		t.Fatalf("body model = %q, want gpt-5.4; body=%s", got, body)
	}
	if got := gjson.GetBytes(body, "reasoning.effort").String(); got != "xhigh" {
		t.Fatalf("reasoning.effort = %q, want xhigh; body=%s", got, body)
	}
}

func TestApplyConfiguredModelMappingToBodyIgnoresClaudeMappingSetting(t *testing.T) {
	store := auth.NewStore(nil, nil, nil)
	store.SetModelMapping(`{"gpt-5.2":"gpt-5.5"}`)
	handler := NewHandler(store, nil, nil, nil)

	body, original, effective, mapped := handler.applyConfiguredModelMappingToBody(
		[]byte(`{"model":"gpt-5.2","input":"hello"}`),
		[]string{"gpt-5.5"},
	)
	if mapped {
		t.Fatal("Claude model_mapping should not rewrite Codex/OpenAI requests")
	}
	if original != "gpt-5.2" || effective != "gpt-5.2" {
		t.Fatalf("original/effective = %q/%q, want gpt-5.2/gpt-5.2", original, effective)
	}
	if got := gjson.GetBytes(body, "model").String(); got != "gpt-5.2" {
		t.Fatalf("body model = %q, want gpt-5.2; body=%s", got, body)
	}
}
