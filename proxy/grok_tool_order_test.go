package proxy

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestStabilizeGrokToolOrderRemembersFirstSeen(t *testing.T) {
	resetGrokToolOrderMemo()
	first := []any{
		map[string]any{"type": "function", "name": "Read"},
		map[string]any{"type": "function", "name": "Write"},
		map[string]any{"type": "function", "name": "Bash"},
	}
	reordered := []any{
		map[string]any{"type": "function", "name": "Bash"},
		map[string]any{"type": "function", "name": "mcp__extra"},
		map[string]any{"type": "function", "name": "Read"},
		map[string]any{"type": "function", "name": "Write"},
	}
	got1 := stabilizeGrokToolOrder("conv-a", first)
	got2 := stabilizeGrokToolOrder("conv-a", reordered)
	if names := grokToolNames(got1); strings.Join(names, ",") != "Read,Write,Bash" {
		t.Fatalf("first turn = %v", names)
	}
	if names := grokToolNames(got2); strings.Join(names, ",") != "Read,Write,Bash,mcp__extra" {
		t.Fatalf("second turn should keep first-seen order and append new tools, got %v", names)
	}
	other := stabilizeGrokToolOrder("conv-b", reordered)
	if names := grokToolNames(other); strings.Join(names, ",") != "Bash,mcp__extra,Read,Write" {
		t.Fatalf("different conversation must not reuse memo: %v", names)
	}
}

func TestTranslateAnthropicToResponsesForGrokStabilizesToolsAndDropsCacheControl(t *testing.T) {
	resetGrokToolOrderMemo()
	turn1 := []byte(`{
		"model":"grok-4.6",
		"system":[{"type":"text","text":"CLAUDE.md","cache_control":{"type":"ephemeral"}}],
		"messages":[{"role":"user","content":"分析项目"}],
		"tools":[
			{"name":"Read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}},
			{"name":"Write","input_schema":{"type":"object"}}
		]
	}`)
	turn2 := []byte(`{
		"model":"grok-4.6",
		"system":[{"type":"text","text":"CLAUDE.md","cache_control":{"type":"ephemeral"}}],
		"messages":[{"role":"user","content":"分析项目"},{"role":"assistant","content":"ok"},{"role":"user","content":"继续"}],
		"tools":[
			{"name":"Write","input_schema":{"type":"object"}},
			{"name":"mcp__git","input_schema":{"type":"object"}},
			{"name":"Read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}
		]
	}`)
	got1, _, err := TranslateAnthropicToResponsesForGrok(turn1, "", []string{"grok-4.6"})
	if err != nil {
		t.Fatal(err)
	}
	got2, _, err := TranslateAnthropicToResponsesForGrok(turn2, "", []string{"grok-4.6"})
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(got1, "tools.0.name").String() != "Read" || gjson.GetBytes(got1, "tools.1.name").String() != "Write" {
		t.Fatalf("first tools = %s", gjson.GetBytes(got1, "tools").Raw)
	}
	if gjson.GetBytes(got2, "tools.#").Int() != 3 ||
		gjson.GetBytes(got2, "tools.0.name").String() != "Read" ||
		gjson.GetBytes(got2, "tools.1.name").String() != "Write" ||
		gjson.GetBytes(got2, "tools.2.name").String() != "mcp__git" {
		t.Fatalf("stabilized tools = %s", gjson.GetBytes(got2, "tools").Raw)
	}
	if strings.Contains(string(got1), "cache_control") || strings.Contains(string(got2), "cache_control") {
		t.Fatal("Grok tools must not forward cache_control")
	}
}

func TestGrokPrefixFingerprintIgnoresLaterUserTurns(t *testing.T) {
	a := []byte(`{"tools":[{"type":"function","name":"Read"}],"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"static"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"q1"}]}]}`)
	b := []byte(`{"tools":[{"type":"function","name":"Read"}],"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"static"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"q1"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"q2"}]}]}`)
	c := []byte(`{"tools":[{"type":"function","name":"Write"}],"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"static"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"q1"}]}]}`)
	if grokShortHash(gjson.GetBytes(a, "tools").Raw) != grokShortHash(gjson.GetBytes(b, "tools").Raw) {
		t.Fatal("same tools should share tools hash")
	}
	if grokShortHash(gjson.GetBytes(a, "tools").Raw) == grokShortHash(gjson.GetBytes(c, "tools").Raw) {
		t.Fatal("different tools must not share tools hash")
	}
}

func grokToolNames(tools []any) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, grokToolName(tool))
	}
	return out
}
