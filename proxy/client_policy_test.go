package proxy

import "testing"

func TestMatchAllowedClientEntry(t *testing.T) {
	entry := ClientPolicyEntry{Originator: "codex_vscode", UAContains: "codex_vscode/"}
	if !MatchAllowedClientEntry(entry, "Codex_VSCode/1.2.3", "codex_vscode") {
		t.Fatal("MatchAllowedClientEntry() should match originator and UA contains")
	}
	if MatchAllowedClientEntry(entry, "codex_vscode/1.2.3", "other") {
		t.Fatal("MatchAllowedClientEntry() should require configured originator")
	}
	if MatchAllowedClientEntry(entry, "curl/8.0", "codex_vscode") {
		t.Fatal("MatchAllowedClientEntry() should require configured UA substring")
	}
}

func TestMatchAllowedClientEntries(t *testing.T) {
	entries := []ClientPolicyEntry{
		{Originator: "codex_cli_rs", UAContains: "codex_cli_rs/"},
		{Originator: "opencode", UAContains: "opencode/", SkipEngineFingerprint: true},
	}
	matched, ok := MatchAllowedClientEntries(entries, "opencode/0.5.0", "opencode")
	if !ok {
		t.Fatal("MatchAllowedClientEntries() should match second entry")
	}
	if !matched.SkipEngineFingerprint {
		t.Fatal("matched entry should preserve SkipEngineFingerprint")
	}
}

func TestMatchBlockedClientEntry(t *testing.T) {
	entry := ClientPolicyEntry{Originator: "bad-client", UAContains: "blocked"}
	if !MatchBlockedClientEntry(entry, "curl/8.0", "bad-client") {
		t.Fatal("MatchBlockedClientEntry() should match originator via OR")
	}
	if !MatchBlockedClientEntry(entry, "BlockedUA/1.0", "other") {
		t.Fatal("MatchBlockedClientEntry() should match UA substring via OR")
	}
	if MatchBlockedClientEntry(entry, "curl/8.0", "other") {
		t.Fatal("MatchBlockedClientEntry() should reject when no field matches")
	}
}

func TestValidateClientPolicyEntriesJSON(t *testing.T) {
	valid := `[{"originator":"codex_cli_rs","ua_contains":"codex_cli_rs/"},{"ua_contains":"opencode/"}]`
	if err := ValidateClientPolicyEntriesJSON(valid); err != nil {
		t.Fatalf("ValidateClientPolicyEntriesJSON() error = %v", err)
	}

	validSkip := `[{"originator":"opencode","ua_contains":"opencode/","skip_engine_fingerprint":true}]`
	if err := ValidateClientPolicyEntriesJSON(validSkip); err != nil {
		t.Fatalf("ValidateClientPolicyEntriesJSON() skip fingerprint error = %v", err)
	}

	skipWithoutDualFactor := `[{"ua_contains":"opencode/","skip_engine_fingerprint":true}]`
	if err := ValidateClientPolicyEntriesJSON(skipWithoutDualFactor); err == nil {
		t.Fatal("ValidateClientPolicyEntriesJSON() should reject skip_engine_fingerprint without dual-factor identity")
	}

	emptyEntry := `[{}]`
	if err := ValidateClientPolicyEntriesJSON(emptyEntry); err == nil {
		t.Fatal("ValidateClientPolicyEntriesJSON() should reject entries without match fields")
	}

	invalidJSON := `{"originator":"codex_cli_rs"}`
	if err := ValidateClientPolicyEntriesJSON(invalidJSON); err == nil {
		t.Fatal("ValidateClientPolicyEntriesJSON() should reject non-array JSON")
	}
}

func TestParseClientPolicyEntries(t *testing.T) {
	entries, ok := ParseClientPolicyEntries(`[{"originator":"codex_cli_rs"}]`)
	if !ok {
		t.Fatal("ParseClientPolicyEntries() should accept valid JSON")
	}
	if len(entries) != 1 || entries[0].Originator != "codex_cli_rs" {
		t.Fatalf("ParseClientPolicyEntries() = %#v", entries)
	}
	if _, ok := ParseClientPolicyEntries(`[{}]`); ok {
		t.Fatal("ParseClientPolicyEntries() should reject invalid entries")
	}
}
