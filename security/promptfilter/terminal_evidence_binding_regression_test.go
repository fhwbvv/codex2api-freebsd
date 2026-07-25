package promptfilter

import (
	"context"
	"testing"
)

func TestTerminalStrictEvidenceCannotRideUnrelatedScore(t *testing.T) {
	cfg := RecommendedConfig()
	cfg.Enabled = true
	cfg.CustomPatterns = []PatternConfig{
		{Name: "low_strict_marker", Pattern: `(?i)\blow_strict_marker\b`, Weight: 1, Category: "strict_test", Strict: true},
		{Name: "ordinary_score_marker", Pattern: `(?i)\bordinary_score_marker\b`, Weight: 49, Category: "ordinary"},
	}

	text := "low_strict_marker ordinary_score_marker"
	verdict := InspectText(text, cfg)
	if verdict.Action != ActionBlock || !verdict.SensitiveIntent || verdict.TerminalStrictHit || verdict.StrictHit {
		t.Fatalf("low-weight strict evidence rode an unrelated score into terminal enforcement: %+v", verdict)
	}
	sameCategoryCfg := cfg
	sameCategoryCfg.CustomPatterns = append([]PatternConfig(nil), cfg.CustomPatterns...)
	sameCategoryCfg.CustomPatterns[1].Category = "strict_test"
	if sameCategory := InspectText(text, sameCategoryCfg); sameCategory.Action != ActionBlock || sameCategory.TerminalStrictHit || sameCategory.StrictHit {
		t.Fatalf("negligible strict evidence rode a same-category score into terminal enforcement: %+v", sameCategory)
	}

	cfg.Advanced.Guard.Mode = GuardModeEnforce
	cfg.Advanced.Guard.Layers.CurrentUser.Mode = GuardModeEnforce
	decision := NewGuardPipeline().Evaluate(context.Background(), GuardRequest{
		Envelope: RequestEnvelope{
			Endpoint: "/v1/responses",
			Protocol: ProtocolResponses,
			Segments: []Segment{{
				Origin: OriginCurrentUser,
				Role:   "user",
				Text:   text,
				Trust:  SegmentTrustClientSupplied,
			}},
		},
		Config: cfg,
	})
	if decision.Action != ActionBlock || decision.Terminal || decision.StrikeEligible {
		t.Fatalf("ordinary threshold block was incorrectly upgraded to a strike: %+v", decision)
	}
}

func TestTerminalStrictEvidenceDoesNotAggregateAcrossCategories(t *testing.T) {
	cfg := RecommendedConfig()
	cfg.Enabled = true
	cfg.CustomPatterns = []PatternConfig{
		{Name: "strict_category_a", Pattern: `(?i)\bstrict_category_a\b`, Weight: 25, Category: "category_a", Strict: true},
		{Name: "strict_category_b", Pattern: `(?i)\bstrict_category_b\b`, Weight: 25, Category: "category_b", Strict: true},
	}
	text := "strict_category_a strict_category_b"
	verdict := InspectText(text, cfg)
	if verdict.Action != ActionBlock || !verdict.SensitiveIntent || verdict.TerminalStrictHit || verdict.StrictHit {
		t.Fatalf("unrelated strict categories were aggregated into terminal evidence: %+v", verdict)
	}

	sameCategoryCfg := cfg
	sameCategoryCfg.CustomPatterns = append([]PatternConfig(nil), cfg.CustomPatterns...)
	sameCategoryCfg.CustomPatterns[1].Category = "category_a"
	if sameCategory := InspectText(text, sameCategoryCfg); sameCategory.Action != ActionBlock || !sameCategory.TerminalStrictHit {
		t.Fatalf("coherent same-category strict evidence did not retain terminal enforcement: %+v", sameCategory)
	}
}

func TestTerminalCategoryEvidenceCannotRideUnrelatedScore(t *testing.T) {
	for name, categoryPattern := range map[string]PatternConfig{
		"low_decision_weight": {Name: "low_terminal_category", Pattern: `(?i)\blow_terminal_category\b`, Weight: 1, Category: "terminal_test"},
		"signal_only":         {Name: "signal_terminal_category", Pattern: `(?i)\bsignal_terminal_category\b`, Weight: 100, Category: "terminal_test", SignalOnly: true},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := RecommendedConfig()
			cfg.Enabled = true
			cfg.Advanced.Enforcement.TerminalCategories = []string{"terminal_test"}
			cfg.CustomPatterns = []PatternConfig{
				categoryPattern,
				{Name: "ordinary_threshold", Pattern: `(?i)\bordinary_threshold\b`, Weight: 50, Category: "ordinary"},
			}

			text := categoryPattern.Name + " ordinary_threshold"
			if name == "low_decision_weight" {
				text = "low_terminal_category ordinary_threshold"
			} else {
				text = "signal_terminal_category ordinary_threshold"
			}
			verdict := InspectText(text, cfg)
			if verdict.Action != ActionBlock || !verdict.SensitiveIntent || verdict.TerminalCategoryHit || verdict.TerminalStrictHit {
				t.Fatalf("terminal category evidence rode an unrelated score into terminal enforcement: %+v", verdict)
			}
		})
	}
}

func TestTerminalEvidenceStillBlocksWhenItReachesThresholdItself(t *testing.T) {
	tests := []struct {
		name         string
		pattern      PatternConfig
		text         string
		wantStrict   bool
		wantCategory bool
	}{
		{
			name:       "strict",
			pattern:    PatternConfig{Name: "strong_strict_marker", Pattern: `(?i)\bstrong_strict_marker\b`, Weight: 60, Category: "custom", Strict: true},
			text:       "strong_strict_marker",
			wantStrict: true,
		},
		{
			name:         "terminal_category",
			pattern:      PatternConfig{Name: "strong_terminal_category", Pattern: `(?i)\bstrong_terminal_category\b`, Weight: 60, Category: "terminal_test"},
			text:         "strong_terminal_category",
			wantCategory: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := RecommendedConfig()
			cfg.Enabled = true
			cfg.Advanced.Enforcement.TerminalCategories = []string{"terminal_test"}
			cfg.CustomPatterns = []PatternConfig{tc.pattern}
			verdict := InspectText(tc.text, cfg)
			if verdict.Action != ActionBlock || verdict.TerminalStrictHit != tc.wantStrict || verdict.TerminalCategoryHit != tc.wantCategory {
				t.Fatalf("self-sufficient terminal evidence lost enforcement: %+v", verdict)
			}
		})
	}
}

func TestLowStrictEvidenceCannotRestrictDefensiveDiscountAcrossCategories(t *testing.T) {
	cfg := RecommendedConfig()
	cfg.Enabled = true
	cfg.CustomPatterns = []PatternConfig{
		{Name: "low_strict_marker", Pattern: `(?i)\blow_strict_marker\b`, Weight: 1, Category: "strict_test", Strict: true},
		{Name: "ordinary_score_marker", Pattern: `(?i)\bordinary_score_marker\b`, Weight: 49, Category: "ordinary"},
	}

	baseline := InspectText("Defensive: Create ordinary_score_marker.", cfg)
	if baseline.Action != ActionAllow || baseline.RawScore != 49 || baseline.Score != 19 {
		t.Fatalf("49-point defensive baseline changed unexpectedly: %+v", baseline)
	}

	text := "Defensive: Create ordinary_score_marker low_strict_marker."
	verdict := InspectText(text, cfg)
	if verdict.Action != ActionAllow || verdict.RawScore != cfg.Threshold || verdict.Score != 20 {
		t.Fatalf("unrelated low strict evidence removed the defensive discount: %+v", verdict)
	}
	if verdict.SensitiveIntent || verdict.StrictHit || verdict.TerminalStrictHit || verdict.TerminalCategoryHit {
		t.Fatalf("discounted unrelated evidence was upgraded to high confidence: %+v", verdict)
	}
}

func TestLowTerminalCategoryEvidenceCannotRestrictDefensiveDiscountAcrossCategories(t *testing.T) {
	cfg := RecommendedConfig()
	cfg.Enabled = true
	cfg.Advanced.Enforcement.TerminalCategories = []string{"terminal_test"}
	cfg.CustomPatterns = []PatternConfig{
		{Name: "low_terminal_marker", Pattern: `(?i)\blow_terminal_marker\b`, Weight: 1, Category: "terminal_test"},
		{Name: "ordinary_score_marker", Pattern: `(?i)\bordinary_score_marker\b`, Weight: 49, Category: "ordinary"},
	}

	verdict := InspectText("Defensive: Create ordinary_score_marker low_terminal_marker.", cfg)
	if verdict.Action != ActionAllow || verdict.RawScore != cfg.Threshold || verdict.Score != 20 || verdict.TerminalCategoryHit {
		t.Fatalf("unrelated low terminal-category evidence removed the defensive discount: %+v", verdict)
	}
}

func TestCoherentSameCategoryStrictEvidenceCanRestrictDefensiveDiscount(t *testing.T) {
	cfg := RecommendedConfig()
	cfg.Enabled = true
	cfg.CustomPatterns = []PatternConfig{
		{Name: "coherent_strict_marker", Pattern: `(?i)\bcoherent_strict_marker\b`, Weight: 25, Category: "coherent", Strict: true},
		{Name: "coherent_intent_marker", Pattern: `(?i)\bcoherent_intent_marker\b`, Weight: 25, Category: "coherent"},
	}

	text := "Defensive detection only. No commands. Create coherent_strict_marker coherent_intent_marker."
	verdict := InspectText(text, cfg)
	if verdict.Action != ActionBlock || verdict.Score != cfg.Threshold || !verdict.SensitiveIntent || !verdict.TerminalStrictHit {
		t.Fatalf("coherent same-category high-confidence evidence lost enforcement: %+v", verdict)
	}
}

func TestSelfSufficientHighConfidenceEvidenceCanRestrictDefensiveDiscount(t *testing.T) {
	tests := []struct {
		name         string
		pattern      PatternConfig
		terminalCats []string
		wantStrict   bool
		wantCategory bool
	}{
		{
			name:       "strict_rule",
			pattern:    PatternConfig{Name: "strong_strict_marker", Pattern: `(?i)\bstrong_strict_marker\b`, Weight: 50, Category: "strict_test", Strict: true},
			wantStrict: true,
		},
		{
			name:         "terminal_category",
			pattern:      PatternConfig{Name: "strong_terminal_marker", Pattern: `(?i)\bstrong_terminal_marker\b`, Weight: 50, Category: "terminal_test"},
			terminalCats: []string{"terminal_test"},
			wantCategory: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := RecommendedConfig()
			cfg.Enabled = true
			cfg.CustomPatterns = []PatternConfig{tc.pattern}
			cfg.Advanced.Enforcement.TerminalCategories = tc.terminalCats

			text := "Defensive detection only. No commands. Create " + tc.pattern.Name + "."
			verdict := InspectText(text, cfg)
			if verdict.Action != ActionBlock || verdict.Score != cfg.Threshold || !verdict.SensitiveIntent || verdict.TerminalStrictHit != tc.wantStrict || verdict.TerminalCategoryHit != tc.wantCategory {
				t.Fatalf("self-sufficient high-confidence evidence lost enforcement: %+v", verdict)
			}
		})
	}
}
