package promptfilter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGuardRolloutConfigJSONUsesStringIDsAndAcceptsLegacyNumbers(t *testing.T) {
	var cfg GuardRolloutConfig
	if err := json.Unmarshal([]byte(`{
		"enabled":true,
		"percent":5,
		"fallback_mode":"warn",
		"newapi_user_allowlist":[42,"0043","tenant-a"],
		"api_key_allowlist":[7,"8","0009"],
		"protocols":["responses"],
		"providers":["openai"]
	}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.NewAPIUserAllowlist, ","); got != "42,43,tenant-a" {
		t.Fatalf("newapi users = %q", got)
	}
	if len(cfg.APIKeyAllowlist) != 3 || cfg.APIKeyAllowlist[0] != 7 || cfg.APIKeyAllowlist[1] != 8 || cfg.APIKeyAllowlist[2] != 9 {
		t.Fatalf("api keys = %v", cfg.APIKeyAllowlist)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{`"newapi_user_allowlist":["42","43","tenant-a"]`, `"api_key_allowlist":["7","8","9"]`, `"protocols":["responses"]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("marshal = %s, missing %s", text, want)
		}
	}
}

func TestGuardRolloutConfigJSONRejectsAmbiguousIdentityNumbers(t *testing.T) {
	for _, raw := range []string{
		`{"newapi_user_allowlist":[1.5]}`,
		`{"newapi_user_allowlist":["1e3"]}`,
		`{"newapi_user_allowlist":["-1"]}`,
		`{"newapi_user_allowlist":["+1"]}`,
		`{"api_key_allowlist":[1e3]}`,
		`{"api_key_allowlist":["1.5"]}`,
	} {
		var cfg GuardRolloutConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err == nil {
			t.Fatalf("accepted ambiguous identity: %s", raw)
		}
	}
}
