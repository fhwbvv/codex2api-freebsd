package auth

import "testing"

func TestGrokFollowUpEffortConfigFromJSON(t *testing.T) {
	def := DefaultGrokFollowUpEffortConfig()
	got := GrokFollowUpEffortConfigFromJSON("")
	if got != def {
		t.Fatalf("empty = %#v, want %#v", got, def)
	}

	disabled := GrokFollowUpEffortConfigFromJSON(`{"follow_up_effort_enabled":false,"follow_up_tool_effort":"high","follow_up_small_effort":"medium"}`)
	if disabled.Enabled || disabled.ToolEffort != "high" || disabled.SmallEffort != "medium" {
		t.Fatalf("custom = %#v", disabled)
	}

	invalid := GrokFollowUpEffortConfigFromJSON(`{"follow_up_tool_effort":"xhigh","follow_up_small_effort":"nope"}`)
	if invalid.Enabled || invalid.ToolEffort != "medium" || invalid.SmallEffort != "low" {
		t.Fatalf("invalid efforts should fall back: %#v", invalid)
	}
}
