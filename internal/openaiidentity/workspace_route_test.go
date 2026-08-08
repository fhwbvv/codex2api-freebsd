package openaiidentity

import "testing"

func TestEffectiveWorkspaceIDPrefersRouteOverride(t *testing.T) {
	headers := map[string]string{"chatgpt-account-id": " team-workspace "}
	if got := EffectiveWorkspaceID("personal-workspace", headers); got != "team-workspace" {
		t.Fatalf("EffectiveWorkspaceID = %q, want team-workspace", got)
	}
}

func TestEffectiveWorkspaceIDFallsBackToTokenWorkspace(t *testing.T) {
	if got := EffectiveWorkspaceID(" personal-workspace ", nil); got != "personal-workspace" {
		t.Fatalf("EffectiveWorkspaceID = %q, want personal-workspace", got)
	}
	if got := EffectiveWorkspaceID("user-abc", nil); got != "" {
		t.Fatalf("EffectiveWorkspaceID user workspace = %q, want empty", got)
	}
	if got := EffectiveWorkspaceID("personal-workspace", map[string]string{"chatgpt-account-id": "  "}); got != "personal-workspace" {
		t.Fatalf("EffectiveWorkspaceID blank override = %q, want personal-workspace", got)
	}
}

func TestWorkspaceOverrideFromHeadersIsCaseInsensitive(t *testing.T) {
	headers := map[string]string{"CHATGPT-ACCOUNT-ID": "team-workspace"}
	if got := WorkspaceOverrideFromHeaders(headers); got != "team-workspace" {
		t.Fatalf("WorkspaceOverrideFromHeaders = %q, want team-workspace", got)
	}
}

func TestWorkspaceOverrideFromHeadersRejectsConflictingCaseVariants(t *testing.T) {
	headers := map[string]string{
		"Chatgpt-Account-Id": "team-a",
		"chatgpt-account-id": "team-b",
	}
	if _, err := WorkspaceOverrideFromHeadersChecked(headers); err == nil {
		t.Fatal("WorkspaceOverrideFromHeadersChecked should reject conflicting values")
	}
	if got := WorkspaceOverrideFromHeaders(headers); got != "" {
		t.Fatalf("WorkspaceOverrideFromHeaders = %q, want empty on conflict", got)
	}
}
