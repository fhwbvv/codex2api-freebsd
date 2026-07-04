package proxy

import (
	"net/http"
	"strings"
	"testing"
)

func TestEvaluateEngineFingerprintRequiredSignals(t *testing.T) {
	signals := []EngineFingerprintSignal{
		{Type: FingerprintSignalHeaderPrefix, Match: []string{"x-codex-"}, Required: true},
		{Type: FingerprintSignalBodyPath, Match: []string{"client_metadata.x-codex-installation-id"}, Required: true},
		{Type: FingerprintSignalHeaderExact, Match: []string{"session-id"}, Required: false},
	}

	headers := http.Header{"X-Codex-Turn-State": []string{"state"}}
	body := []byte(`{"client_metadata":{"x-codex-installation-id":"install-1"}}`)
	if !EvaluateEngineFingerprint(headers, body, signals) {
		t.Fatal("EvaluateEngineFingerprint() should match all required signals")
	}

	missingBody := []byte(`{"client_metadata":{}}`)
	if EvaluateEngineFingerprint(headers, missingBody, signals) {
		t.Fatal("EvaluateEngineFingerprint() should reject missing required body path")
	}
}

func TestEvaluateEngineFingerprintDefaultSignals(t *testing.T) {
	if !EvaluateEngineFingerprint(http.Header{"X-Codex-Trace": []string{"trace"}}, nil, nil) {
		t.Fatal("EvaluateEngineFingerprint() should use default x-codex header signal")
	}
	if EvaluateEngineFingerprint(http.Header{"Session-Id": []string{"session"}}, nil, nil) {
		t.Fatal("EvaluateEngineFingerprint() should require default x-codex header prefix")
	}
}

func TestEvaluateEngineFingerprintNormalizesSignalTypeAndHeaderName(t *testing.T) {
	signals := []EngineFingerprintSignal{
		{Type: " header_exact ", Match: []string{"session-id"}, Required: true},
	}
	headers := http.Header{"session-id": []string{"session-1"}}
	if !EvaluateEngineFingerprint(headers, nil, signals) {
		t.Fatal("EvaluateEngineFingerprint() should trim signal type and match raw header names case-insensitively")
	}
}

func TestEvaluateEngineFingerprintMatchVariants(t *testing.T) {
	signals := []EngineFingerprintSignal{
		{Type: FingerprintSignalHeaderExact, Match: []string{"session-id", "session_id"}, Required: true},
	}
	if !EvaluateEngineFingerprint(http.Header{"Session_id": []string{"session"}}, nil, signals) {
		t.Fatal("EvaluateEngineFingerprint() should OR match variants in a single signal")
	}
}

func TestValidateEngineFingerprintSignalsJSON(t *testing.T) {
	valid := `[{"type":"header_prefix","match":["x-codex-"],"required":true}]`
	if err := ValidateEngineFingerprintSignalsJSON(valid); err != nil {
		t.Fatalf("ValidateEngineFingerprintSignalsJSON() error = %v", err)
	}

	invalidType := `[{"type":"bad","match":["x-codex-"],"required":true}]`
	if err := ValidateEngineFingerprintSignalsJSON(invalidType); err == nil {
		t.Fatal("ValidateEngineFingerprintSignalsJSON() should reject unknown signal type")
	}

	emptyMatch := `[{"type":"header_prefix","match":["  "],"required":true}]`
	if err := ValidateEngineFingerprintSignalsJSON(emptyMatch); err == nil {
		t.Fatal("ValidateEngineFingerprintSignalsJSON() should reject empty match values")
	}
}

func TestParseEngineFingerprintSignals(t *testing.T) {
	signals, ok := ParseEngineFingerprintSignals("")
	if !ok {
		t.Fatal("ParseEngineFingerprintSignals() should accept empty raw config")
	}
	if len(signals) != len(DefaultEngineFingerprintSignals) {
		t.Fatalf("ParseEngineFingerprintSignals() returned %d defaults, want %d", len(signals), len(DefaultEngineFingerprintSignals))
	}

	if _, ok := ParseEngineFingerprintSignals(`[{"type":"bad","match":["x"]}]`); ok {
		t.Fatal("ParseEngineFingerprintSignals() should reject invalid config")
	}
}

func TestDefaultEngineFingerprintSignalsJSON(t *testing.T) {
	raw := DefaultEngineFingerprintSignalsJSON()
	if !strings.Contains(raw, FingerprintSignalHeaderPrefix) {
		t.Fatalf("DefaultEngineFingerprintSignalsJSON() = %q", raw)
	}
	if err := ValidateEngineFingerprintSignalsJSON(raw); err != nil {
		t.Fatalf("default signal JSON should validate: %v", err)
	}
}
