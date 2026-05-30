package proxy

import "testing"

func TestRetryAccountExclusionsSoftResetPreservesHard(t *testing.T) {
	exclusions := newRetryAccountExclusions()
	exclusions.MarkSoftFirstTokenTimeout(1)
	exclusions.MarkHard(2)

	selection := exclusions.ForSelection()
	if !selection[1] || !selection[2] {
		t.Fatalf("selection excludes = %#v, want soft and hard accounts", selection)
	}

	if !exclusions.ResetSoft() {
		t.Fatal("ResetSoft() = false, want true")
	}
	selection = exclusions.ForSelection()
	if selection[1] {
		t.Fatalf("soft account still excluded after reset: %#v", selection)
	}
	if !selection[2] {
		t.Fatalf("hard account was cleared by soft reset: %#v", selection)
	}
}

func TestRetryAccountExclusionsHardOverridesSoft(t *testing.T) {
	exclusions := newRetryAccountExclusions()
	exclusions.MarkSoftFirstTokenTimeout(1)
	exclusions.MarkHard(1)

	if exclusions.ResetSoft() {
		t.Fatal("ResetSoft() cleared a hard-only account")
	}
	selection := exclusions.ForSelection()
	if !selection[1] {
		t.Fatalf("hard account missing from selection excludes: %#v", selection)
	}
}

func TestIsFirstTokenTimeoutOutcome(t *testing.T) {
	if !isFirstTokenTimeoutOutcome(firstTokenTimeoutOutcome(10)) {
		t.Fatal("first-token timeout outcome should be classified as timeout")
	}
	if isFirstTokenTimeoutOutcome(streamOutcome{failureKind: "transport"}) {
		t.Fatal("transport outcome should not be classified as first-token timeout")
	}
}
