package promptfilter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInspectTextHighlightsNonSensitiveMatchContext(t *testing.T) {
	cfg := testConfig(ModeBlock)
	v := InspectText("Classify Codex ambient suggestion candidates for policy safety. Later text asks for a ddos attack plan.", cfg)
	if len(v.Matched) == 0 {
		t.Fatalf("matched = 0; verdict=%+v", v)
	}
	if !strings.Contains(v.TextPreview, HitStartMarker) || !strings.Contains(v.TextPreview, HitEndMarker) {
		t.Fatalf("text_preview = %q, want hit markers", v.TextPreview)
	}
	if !strings.Contains(v.TextPreview, "ddos") {
		t.Fatalf("text_preview = %q, want hit context containing ddos", v.TextPreview)
	}
}

func TestInspectTextSkipsHighlightWhenContextContainsSensitiveData(t *testing.T) {
	cfg := testConfig(ModeBlock)
	v := InspectText("Classify Codex ambient suggestion candidates for policy safety. Authorization: Bearer sk-sensitive123456 Later text asks for a ddos attack plan.", cfg)
	if len(v.Matched) == 0 {
		t.Fatalf("matched = 0; verdict=%+v", v)
	}
	var found Match
	for _, match := range v.Matched {
		if match.Name == "ddos_attack" {
			found = match
			break
		}
	}
	if found.Name == "" {
		t.Fatalf("ddos_attack not found in matches: %+v", v.Matched)
	}
	if strings.Contains(v.TextPreview, "sk-sensitive123456") {
		t.Fatalf("text_preview leaked API key: %q", v.TextPreview)
	}
	if strings.Contains(v.TextPreview, HitStartMarker) || strings.Contains(v.TextPreview, HitEndMarker) {
		t.Fatalf("text_preview = %q, want no hit markers when sensitive data is present", v.TextPreview)
	}
	if !strings.Contains(v.TextPreview, "ddos") {
		t.Fatalf("text_preview = %q, want hit context containing ddos", v.TextPreview)
	}
	if !strings.Contains(v.TextPreview, "[REDACTED]") && !strings.Contains(v.TextPreview, "[REDACTED_API_KEY]") {
		t.Fatalf("text_preview = %q, want redaction marker", v.TextPreview)
	}
}

func TestMatchesJSONDoesNotIncludeContextFields(t *testing.T) {
	matches := []Match{{Name: "rule", Weight: 50, Category: "custom"}}
	raw := MatchesJSON(matches)
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("MatchesJSON returned invalid JSON %q: %v", raw, err)
	}
	if len(parsed) != 1 {
		t.Fatalf("parsed matches length = %d, want 1", len(parsed))
	}
	if _, ok := parsed[0]["sample"]; ok {
		t.Fatalf("matched_patterns unexpectedly contains sample: %q", raw)
	}
	if _, ok := parsed[0]["context"]; ok {
		t.Fatalf("matched_patterns unexpectedly contains context: %q", raw)
	}
}

func TestRedactSensitiveMasksCommonSecrets(t *testing.T) {
	input := `Authorization: Bearer sk-live123456 password="hunter2" api_key=sk-testabcdef email admin@example.com jwt eyJabc1234567890.eyJdef1234567890.sig1234567890`
	got := RedactSensitive(input)
	for _, leaked := range []string{"sk-live123456", "hunter2", "sk-testabcdef", "admin@example.com", "eyJabc1234567890.eyJdef1234567890.sig1234567890"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("RedactSensitive leaked %q in %q", leaked, got)
		}
	}
}
