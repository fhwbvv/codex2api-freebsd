package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

const portableCompactionFixture = "sub2api-emulated-compaction-v1:PHN1bW1hcnk+cG9ydGFibGUgY29udGV4dDwvc3VtbWFyeT4="

func TestNormalizeResponsesCompactionItemsDecodesPortableEncryptedSummary(t *testing.T) {
	body := map[string]any{
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "before"},
			map[string]any{"type": "compaction", "encrypted_content": portableCompactionFixture},
			map[string]any{"type": "message", "role": "user", "content": "after"},
		},
	}

	if !normalizeResponsesCompactionItems(body) {
		t.Fatal("portable compaction should be normalized")
	}

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal normalized body: %v", err)
	}
	items := gjson.GetBytes(raw, "input").Array()
	if len(items) != 3 {
		t.Fatalf("input length = %d, want 3: %s", len(items), raw)
	}
	if got := items[1].Get("type").String(); got != "message" {
		t.Fatalf("normalized item type = %q, want message: %s", got, items[1].Raw)
	}
	if got := items[1].Get("role").String(); got != "developer" {
		t.Fatalf("normalized item role = %q, want developer: %s", got, items[1].Raw)
	}
	text := items[1].Get("content.0.text").String()
	if !strings.Contains(text, "portable context") {
		t.Fatalf("normalized summary missing decoded text: %q", text)
	}
	if strings.Contains(string(raw), portableCompactionEnvelopePrefix) {
		t.Fatalf("portable envelope should be removed: %s", raw)
	}
}

func TestNormalizePortableResponsesCompactionHistory(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.6-sol","input":[{"type":"compaction","encrypted_content":"` + portableCompactionFixture + `"},{"type":"message","role":"user","content":"continue"}]}`)

	got, changed := normalizePortableResponsesCompactionHistory(raw)
	if !changed {
		t.Fatal("portable compaction request should be rewritten before routing")
	}
	if strings.Contains(string(got), portableCompactionEnvelopePrefix) {
		t.Fatalf("portable envelope should not reach affinity routing: %s", got)
	}
	if text := gjson.GetBytes(got, "input.0.content.0.text").String(); !strings.Contains(text, "portable context") {
		t.Fatalf("decoded summary missing from rewritten body: %s", got)
	}
	if gotText := gjson.GetBytes(got, "input.1.content").String(); gotText != "continue" {
		t.Fatalf("unrelated input changed: got %q; body=%s", gotText, got)
	}
}

func TestNormalizePortableResponsesCompactionHistoryLeavesOpaqueOrInvalidContentUntouched(t *testing.T) {
	tests := []struct {
		name      string
		encrypted string
	}{
		{name: "opaque upstream state", encrypted: "opaque-blob"},
		{name: "malformed base64", encrypted: portableCompactionEnvelopePrefix + "%%%"},
		{name: "invalid utf8", encrypted: portableCompactionEnvelopePrefix + "//4="},
		{name: "wrong wrapper", encrypted: portableCompactionEnvelopePrefix + "PG5vdC1zdW1tYXJ5PnBvcnRhYmxlIGNvbnRleHQ8L25vdC1zdW1tYXJ5Pg=="},
		{name: "empty summary", encrypted: portableCompactionEnvelopePrefix + "PHN1bW1hcnk+ICAgPC9zdW1tYXJ5Pg=="},
		{name: "prefix must be exact", encrypted: " " + portableCompactionFixture},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"input":[{"type":"compaction","encrypted_content":"` + tc.encrypted + `"}]}`)
			got, changed := normalizePortableResponsesCompactionHistory(raw)
			if changed {
				t.Fatalf("invalid or opaque content should stay affinity-bound: %s", got)
			}
			if string(got) != string(raw) {
				t.Fatalf("body changed unexpectedly:\n got: %s\nwant: %s", got, raw)
			}
		})
	}
}

func TestNormalizePortableResponsesCompactionHistoryKeepsMixedOpaqueState(t *testing.T) {
	raw := []byte(`{"input":[{"type":"compaction","encrypted_content":"` + portableCompactionFixture + `"},{"type":"compaction","encrypted_content":"known-opaque"}]}`)

	got, changed := normalizePortableResponsesCompactionHistory(raw)
	if !changed {
		t.Fatal("valid portable item in mixed input should be normalized")
	}
	contents := requestCompactionEncryptedContents(got)
	if len(contents) != 1 || contents[0] != "known-opaque" {
		t.Fatalf("remaining opaque state = %v, want [known-opaque]; body=%s", contents, got)
	}
}

func TestNormalizePortableResponsesCompactionHistorySupportsKnownCompactionItemTypes(t *testing.T) {
	for _, itemType := range []string{"compaction", "context_compaction", "compaction_summary"} {
		t.Run(itemType, func(t *testing.T) {
			raw := []byte(`{"input":[{"type":"` + itemType + `","encrypted_content":"` + portableCompactionFixture + `"}]}`)
			got, changed := normalizePortableResponsesCompactionHistory(raw)
			if !changed || gjson.GetBytes(got, "input.0.type").String() != "message" {
				t.Fatalf("known compaction type was not normalized: changed=%v body=%s", changed, got)
			}
		})
	}
}
