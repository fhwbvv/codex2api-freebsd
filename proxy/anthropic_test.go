package proxy

import (
	"encoding/json"
	"testing"

	"github.com/tidwall/gjson"
)

func TestTranslateAnthropicToCodex_OutputConfigEffortTakesPrecedence(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}],
		"thinking":{"type":"enabled","budget_tokens":512},
		"output_config":{"effort":"max"}
	}`)

	got, _, err := TranslateAnthropicToCodexWithModels(raw, "", []string{"gpt-5.4", "gpt-5.4-mini"})
	if err != nil {
		t.Fatalf("TranslateAnthropicToCodexWithModels returned error: %v", err)
	}

	if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != "xhigh" {
		t.Fatalf("reasoning.effort = %q, want xhigh; body=%s", effort, got)
	}
	if summary := gjson.GetBytes(got, "reasoning.summary").String(); summary != "auto" {
		t.Fatalf("reasoning.summary = %q, want auto; body=%s", summary, got)
	}
}

func TestTranslateAnthropicToCodex_OutputConfigHighIsExplicit(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}],
		"output_config":{"effort":"high"}
	}`)

	got, _, err := TranslateAnthropicToCodexWithModels(raw, "", []string{"gpt-5.4"})
	if err != nil {
		t.Fatalf("TranslateAnthropicToCodexWithModels returned error: %v", err)
	}

	if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != "high" {
		t.Fatalf("reasoning.effort = %q, want high; body=%s", effort, got)
	}
}

func TestTranslateAnthropicToCodex_DefaultsReasoningHighWithSummary(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}]
	}`)

	got, _, err := TranslateAnthropicToCodexWithModels(raw, "", []string{"gpt-5.4"})
	if err != nil {
		t.Fatalf("TranslateAnthropicToCodexWithModels returned error: %v", err)
	}

	if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != "high" {
		t.Fatalf("reasoning.effort = %q, want high; body=%s", effort, got)
	}
	if summary := gjson.GetBytes(got, "reasoning.summary").String(); summary != "auto" {
		t.Fatalf("reasoning.summary = %q, want auto; body=%s", summary, got)
	}
}

func TestTranslateAnthropicToCodex_ThinkingBudgetDoesNotControlEffort(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}],
		"thinking":{"type":"enabled","budget_tokens":4096}
	}`)

	got, _, err := TranslateAnthropicToCodexWithModels(raw, "", []string{"gpt-5.4"})
	if err != nil {
		t.Fatalf("TranslateAnthropicToCodexWithModels returned error: %v", err)
	}

	if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != "high" {
		t.Fatalf("reasoning.effort = %q, want high; body=%s", effort, got)
	}
}

func TestTranslateAnthropicToCodexCanonicalizesDynamicMappedModelAlias(t *testing.T) {
	raw := []byte(`{
		"model":"claude-haiku-4-5-20251001",
		"max_tokens":1024,
		"messages":[{"role":"user","content":"hello"}]
	}`)

	body, originalModel, err := TranslateAnthropicToCodexWithModels(raw, `{"claude-haiku-4-5-20251001":"gpt5-4"}`, []string{"gpt-5.4", "gpt-5.4-mini"})
	if err != nil {
		t.Fatalf("TranslateAnthropicToCodexWithModels returned error: %v", err)
	}
	if originalModel != "claude-haiku-4-5-20251001" {
		t.Fatalf("originalModel = %q, want claude-haiku-4-5-20251001", originalModel)
	}

	var out struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal translated body: %v", err)
	}
	if out.Model != "gpt-5.4" {
		t.Fatalf("translated model = %q, want gpt-5.4", out.Model)
	}
}

func TestTranslateAnthropicToCodexDoesNotCanonicalizeDisabledModelAlias(t *testing.T) {
	raw := []byte(`{
		"model":"claude-haiku-4-5-20251001",
		"max_tokens":1024,
		"messages":[{"role":"user","content":"hello"}]
	}`)

	body, _, err := TranslateAnthropicToCodexWithModels(raw, `{"claude-haiku-4-5-20251001":"gpt5-4"}`, []string{"gpt-5.4-mini"})
	if err != nil {
		t.Fatalf("TranslateAnthropicToCodexWithModels returned error: %v", err)
	}

	var out struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal translated body: %v", err)
	}
	if out.Model != "gpt5-4" {
		t.Fatalf("translated model = %q, want gpt5-4", out.Model)
	}
}
