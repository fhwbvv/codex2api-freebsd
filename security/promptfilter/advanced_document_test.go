package promptfilter

import (
	"encoding/json"
	"testing"
)

func TestParseAdvancedConfigDocumentPreservesUnknownFields(t *testing.T) {
	raw := `{
		"normalization":{"enabled":true,"future_decoder":"v2"},
		"guard":{"mode":"shadow","future_guard":{"enabled":true}},
		"newapi":{"Secret":"must-not-leak"},
		"future_root":{"large_id":9007199254740993}
	}`

	document, err := ParseAdvancedConfigDocument(raw)
	if err != nil {
		t.Fatalf("ParseAdvancedConfigDocument: %v", err)
	}
	if !document.Effective.Normalization.Enabled {
		t.Fatal("effective normalization.enabled = false, want true")
	}
	if document.Effective.Guard.Mode != GuardModeShadow {
		t.Fatalf("effective guard.mode = %q, want %q", document.Effective.Guard.Mode, GuardModeShadow)
	}

	root := decodeDocumentObject(t, document.Raw)
	if got := rawMessageString(t, decodeDocumentObject(t, string(root["normalization"]))["future_decoder"]); got != "v2" {
		t.Fatalf("future_decoder = %q, want v2", got)
	}
	guard := decodeDocumentObject(t, string(root["guard"]))
	if _, ok := guard["future_guard"]; !ok {
		t.Fatal("future_guard was removed")
	}
	futureRoot := decodeDocumentObject(t, string(root["future_root"]))
	if got := string(futureRoot["large_id"]); got != "9007199254740993" {
		t.Fatalf("large_id = %s, want exact integer", got)
	}
	for _, known := range []string{"sidecar", "session", "attachment", "output", "newapi"} {
		if _, ok := root[known]; !ok {
			t.Fatalf("legacy document was not expanded with known field %q", known)
		}
	}
	for key := range decodeDocumentObject(t, string(root["newapi"])) {
		if key == "secret" || key == "Secret" {
			t.Fatalf("newapi.%s was persisted in advanced config document", key)
		}
	}
}

func TestMergeAdvancedConfigDocumentUpdatesFieldsWithoutDroppingUnknowns(t *testing.T) {
	base := `{
		"normalization":{"enabled":true,"future_decoder":"v2"},
		"guard":{"mode":"shadow","provider_profiles":{"openai":"strict","grok":"research"}},
		"future_root":{"enabled":true}
	}`
	patch := `{
		"guard":{"mode":"enforce","provider_profiles":{"openai":"balanced"}},
		"future_patch":{"version":2}
	}`

	document, err := MergeAdvancedConfigDocument(base, patch)
	if err != nil {
		t.Fatalf("MergeAdvancedConfigDocument: %v", err)
	}
	if document.Effective.Guard.Mode != GuardModeEnforce {
		t.Fatalf("effective guard.mode = %q, want %q", document.Effective.Guard.Mode, GuardModeEnforce)
	}
	if got := document.Effective.Guard.ProviderProfiles; len(got) != 1 || got["openai"] != GuardProfileBalanced {
		t.Fatalf("provider_profiles = %#v, want replacement map", got)
	}

	root := decodeDocumentObject(t, document.Raw)
	if _, ok := root["future_root"]; !ok {
		t.Fatal("future_root was removed by known-field update")
	}
	if _, ok := root["future_patch"]; !ok {
		t.Fatal("future field supplied by update was not retained")
	}
	normalization := decodeDocumentObject(t, string(root["normalization"]))
	if got := rawMessageString(t, normalization["future_decoder"]); got != "v2" {
		t.Fatalf("future_decoder = %q, want v2", got)
	}
	guard := decodeDocumentObject(t, string(root["guard"]))
	profiles := decodeDocumentObject(t, string(guard["provider_profiles"]))
	if len(profiles) != 1 {
		t.Fatalf("persisted provider_profiles = %s, want removed grok entry", guard["provider_profiles"])
	}
}

func TestMergeAdvancedConfigDocumentNullRemovesUnknownField(t *testing.T) {
	document, err := MergeAdvancedConfigDocument(`{"future":{"enabled":true}}`, `{"future":null}`)
	if err != nil {
		t.Fatalf("MergeAdvancedConfigDocument: %v", err)
	}
	if _, ok := decodeDocumentObject(t, document.Raw)["future"]; ok {
		t.Fatal("future field remains after null removal")
	}
}

func TestAdvancedConfigDocumentRejectsInvalidJSON(t *testing.T) {
	if _, err := ParseAdvancedConfigDocument(`{"guard":`); err == nil {
		t.Fatal("ParseAdvancedConfigDocument accepted invalid JSON")
	}
	if _, err := MergeAdvancedConfigDocument(`{"future":true}`, `{"guard":`); err == nil {
		t.Fatal("MergeAdvancedConfigDocument accepted invalid patch JSON")
	}
	if _, err := MergeAdvancedConfigDocument(`{"future":`, `{"guard":{"mode":"shadow"}}`); err == nil {
		t.Fatal("MergeAdvancedConfigDocument accepted invalid base JSON")
	}
}

func decodeDocumentObject(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		t.Fatalf("decode document %q: %v", raw, err)
	}
	return object
}

func rawMessageString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode string %s: %v", raw, err)
	}
	return value
}
