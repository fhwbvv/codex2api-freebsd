package proxy

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestNormalizeServiceTierField(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.4","serviceTier":"fast"}`)

	got := normalizeServiceTierField(raw)

	if tier := gjson.GetBytes(got, "service_tier").String(); tier != "fast" {
		t.Fatalf("service_tier mismatch: got %q want %q", tier, "fast")
	}
	if gjson.GetBytes(got, "serviceTier").Exists() {
		t.Fatal("serviceTier should be removed after normalization")
	}
}

func TestResolveServiceTier(t *testing.T) {
	if got := resolveServiceTier("fast", "default"); got != "fast" {
		t.Fatalf("expected actual tier to win, got %q", got)
	}
	if got := resolveServiceTier("", "fast"); got != "fast" {
		t.Fatalf("expected requested tier fallback, got %q", got)
	}
	if got := resolveServiceTier("default", "fast"); got != "fast" {
		t.Fatalf("expected requested fast to win for logging, got %q", got)
	}
}

func TestSanitizeServiceTierForUpstream_DropsFast(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5.4",
		"service_tier":"fast"
	}`)

	got := sanitizeServiceTierForUpstream(raw)

	if gjson.GetBytes(got, "service_tier").Exists() {
		t.Fatal("unsupported fast tier should not be forwarded upstream")
	}
}

func TestTranslateRequest_PreservesSupportedServiceTier(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5.4",
		"messages":[{"role":"user","content":"hello"}],
		"serviceTier":"priority",
		"reasoning_effort":"high"
	}`)

	got, err := TranslateRequest(raw)
	if err != nil {
		t.Fatalf("TranslateRequest returned error: %v", err)
	}

	if tier := gjson.GetBytes(got, "service_tier").String(); tier != "priority" {
		t.Fatalf("service_tier mismatch: got %q want %q", tier, "priority")
	}
	if gjson.GetBytes(got, "serviceTier").Exists() {
		t.Fatal("serviceTier should not be present after translation")
	}
	if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != "high" {
		t.Fatalf("reasoning.effort mismatch: got %q want %q", effort, "high")
	}
}
