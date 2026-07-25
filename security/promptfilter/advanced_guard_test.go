package promptfilter

import "testing"

func TestGuardConfigRoundTrip(t *testing.T) {
	raw := `{
		"guard": {
			"mode": "warn",
			"default_profile": "research",
			"allow_trusted_overrides": true,
			"provider_profiles": {"openai":"balanced","anthropic":"strict","xai":"research"},
			"performance": {
				"async_shadow_auxiliary_enabled": true,
				"exact_segment_cache_enabled": false,
				"exact_segment_cache_entries": 1024,
				"exact_segment_cache_ttl_seconds": 120,
				"max_segments": 48,
				"max_current_user_bytes": 196608,
				"max_auxiliary_bytes": 24576,
				"scan_chunk_bytes": 4096,
				"scan_overlap_bytes": 256,
				"shadow_workers": 4,
				"shadow_queue_size": 128,
				"shadow_overflow_mode": "sync"
			},
			"rollout": {
				"enabled": true,
				"percent": 25,
				"fallback_mode": "shadow",
				"newapi_user_allowlist": ["42"],
				"api_key_allowlist": [7],
				"protocols": ["responses"],
				"providers": ["openai"]
			},
			"layers": {
				"current_user":{"mode":"enforce"},
				"history":{"mode":"shadow"},
				"system":{"mode":"off"},
				"developer":{"mode":"warn"},
				"instructions":{"mode":"enforce"},
				"tool_output":{"mode":"shadow"},
				"tool_arguments":{"mode":"warn"},
				"attachment_refs":{"mode":"off"}
			}
		}
	}`
	cfg, err := ParseAdvancedConfig(raw)
	if err != nil {
		t.Fatalf("ParseAdvancedConfig returned error: %v", err)
	}
	if cfg.Guard.Mode != GuardModeWarn || cfg.Guard.DefaultProfile != GuardProfileResearch {
		t.Fatalf("guard config was not parsed: %+v", cfg.Guard)
	}
	if !cfg.Guard.AllowTrustedOverrides {
		t.Fatal("allow_trusted_overrides was not parsed")
	}
	if !cfg.Guard.Performance.AsyncShadowAuxiliaryEnabled || cfg.Guard.Performance.ExactSegmentCacheEnabled || cfg.Guard.Performance.ExactSegmentCacheEntries != 1024 || cfg.Guard.Performance.MaxSegments != 48 || cfg.Guard.Performance.MaxCurrentUserBytes != 196608 || cfg.Guard.Performance.MaxAuxiliaryBytes != 24576 || cfg.Guard.Performance.ScanChunkBytes != 4096 || cfg.Guard.Performance.ScanOverlapBytes != 256 || cfg.Guard.Performance.ShadowWorkers != 4 || cfg.Guard.Performance.ShadowOverflowMode != GuardShadowOverflowDrop {
		t.Fatalf("guard performance config was not parsed: %+v", cfg.Guard.Performance)
	}
	if !cfg.Guard.Rollout.Enabled || cfg.Guard.Rollout.Percent != 25 || cfg.Guard.Rollout.FallbackMode != GuardModeShadow {
		t.Fatalf("rollout config was not parsed: %+v", cfg.Guard.Rollout)
	}
	if cfg.Guard.ProviderProfiles[string(ModelFamilyAnthropic)] != GuardProfileStrict {
		t.Fatalf("anthropic profile = %q, want strict", cfg.Guard.ProviderProfiles[string(ModelFamilyAnthropic)])
	}
	if cfg.Guard.Layers.Instructions.Mode != GuardModeShadow || cfg.Guard.Layers.History.Mode != GuardModeShadow {
		t.Fatalf("guard layers were not parsed: %+v", cfg.Guard.Layers)
	}

	roundTripped, err := ParseAdvancedConfig(MarshalAdvancedConfig(cfg))
	if err != nil {
		t.Fatalf("round-trip ParseAdvancedConfig returned error: %v", err)
	}
	if roundTripped.Guard.Mode != cfg.Guard.Mode || roundTripped.Guard.DefaultProfile != cfg.Guard.DefaultProfile {
		t.Fatalf("guard config changed after round trip: before=%+v after=%+v", cfg.Guard, roundTripped.Guard)
	}
	if !roundTripped.Guard.AllowTrustedOverrides {
		t.Fatal("allow_trusted_overrides changed after round trip")
	}
	if !roundTripped.Guard.Performance.AsyncShadowAuxiliaryEnabled || roundTripped.Guard.Performance.ExactSegmentCacheEnabled || roundTripped.Guard.Performance.ShadowQueueSize != 128 || roundTripped.Guard.Performance.MaxSegments != 48 || roundTripped.Guard.Performance.MaxCurrentUserBytes != 196608 || roundTripped.Guard.Performance.MaxAuxiliaryBytes != 24576 || roundTripped.Guard.Performance.ScanChunkBytes != 4096 || roundTripped.Guard.Performance.ScanOverlapBytes != 256 || roundTripped.Guard.Performance.ShadowOverflowMode != GuardShadowOverflowDrop {
		t.Fatalf("guard performance config changed after round trip: %+v", roundTripped.Guard.Performance)
	}
	if len(roundTripped.Guard.Rollout.NewAPIUserAllowlist) != 1 || len(roundTripped.Guard.Rollout.APIKeyAllowlist) != 1 {
		t.Fatalf("rollout allowlists changed after round trip: %+v", roundTripped.Guard.Rollout)
	}
	if roundTripped.Guard.Layers.ToolArguments.Mode != GuardModeWarn {
		t.Fatalf("tool_arguments mode = %q, want warn", roundTripped.Guard.Layers.ToolArguments.Mode)
	}
}

func TestMissingGuardConfigPreservesLegacyDefaults(t *testing.T) {
	advanced, err := ParseAdvancedConfig(`{"normalization":{"enabled":true}}`)
	if err != nil {
		t.Fatalf("ParseAdvancedConfig returned error: %v", err)
	}
	if advanced.Guard.Mode != GuardModeInherit {
		t.Fatalf("guard mode = %q, want inherit", advanced.Guard.Mode)
	}
	if advanced.Guard.DefaultProfile != GuardProfileBalanced {
		t.Fatalf("default profile = %q, want balanced", advanced.Guard.DefaultProfile)
	}
	if advanced.Guard.Performance.AsyncShadowAuxiliaryEnabled || !advanced.Guard.Performance.ExactSegmentCacheEnabled || advanced.Guard.Performance.ShadowOverflowMode != GuardShadowOverflowDrop {
		t.Fatalf("legacy performance defaults = %+v, want sync shadow with exact cache", advanced.Guard.Performance)
	}
	profile := BuiltinProfileResolver{}.Resolve(RequestEnvelope{ModelFamily: ModelFamilyOpenAI}, advanced.Guard)
	if got := resolveGuardLayerMode(advanced.Guard, profile, OriginCurrentUser, GuardModeEnforce); got != GuardModeEnforce {
		t.Fatalf("current_user mode = %q, want enforce inherited from legacy block mode", got)
	}
	if got := resolveGuardLayerMode(advanced.Guard, profile, OriginInstructions, GuardModeEnforce); got != GuardModeOff {
		t.Fatalf("instructions mode = %q, want off under balanced profile", got)
	}
}

func TestRecommendedAdvancedConfigUsesExplicitCurrentPromptLayers(t *testing.T) {
	cfg := RecommendedAdvancedConfig()
	if cfg.Guard.Mode != GuardModeInherit || cfg.Guard.DefaultProfile != GuardProfileBalanced {
		t.Fatalf("recommended guard identity = %+v", cfg.Guard)
	}
	if cfg.Guard.Layers.CurrentUser.Mode != GuardModeEnforce || cfg.Guard.Layers.History.Mode != GuardModeOff || cfg.Guard.Layers.AttachmentRefs.Mode != GuardModeShadow {
		t.Fatalf("recommended source layers = %+v", cfg.Guard.Layers)
	}
	if cfg.Guard.Layers.ToolOutput.Mode != GuardModeShadow || cfg.Guard.Layers.ToolArguments.Mode != GuardModeOff || cfg.Guard.Layers.SessionContext.Mode != GuardModeShadow || cfg.Guard.Layers.AttachmentContent.Mode != GuardModeShadow {
		t.Fatalf("recommended extension layers = %+v", cfg.Guard.Layers)
	}
	if !cfg.Guard.Performance.AsyncShadowAuxiliaryEnabled || !cfg.Guard.Performance.ExactSegmentCacheEnabled || cfg.Guard.Performance.ShadowOverflowMode != GuardShadowOverflowDrop {
		t.Fatalf("recommended performance config = %+v", cfg.Guard.Performance)
	}
	if cfg.Session.Enabled || !cfg.Session.RequireSignedIdentity || cfg.Session.CombineShortFragments {
		t.Fatalf("recommended session config = %+v", cfg.Session)
	}
	if len(cfg.Enforcement.TerminalCategories) != 0 {
		t.Fatalf("terminal categories = %v, want empty", cfg.Enforcement.TerminalCategories)
	}
	if len(cfg.Intelligence.Queries) == 0 {
		t.Fatal("recommended intelligence queries must be a non-nil audit seed")
	}
}

func TestNormalizeGuardConfigRejectsUnknownModesAndProfiles(t *testing.T) {
	cfg := NormalizeGuardConfig(GuardConfig{
		Mode:           "invalid",
		DefaultProfile: "invalid",
		ProviderProfiles: map[string]string{
			"XAI": "invalid",
		},
		Layers: GuardLayerConfig{CurrentUser: GuardLayerModeConfig{Mode: "invalid"}},
		Rollout: GuardRolloutConfig{
			Percent: 140, FallbackMode: GuardModeEnforce,
			NewAPIUserAllowlist: []string{" 42 ", "42", ""},
			APIKeyAllowlist:     []int64{7, 7, 0, -1},
			Protocols:           []string{" Responses ", "responses"},
			Providers:           []string{" OPENAI ", "openai"},
		},
	})
	if cfg.Mode != GuardModeInherit || cfg.DefaultProfile != GuardProfileBalanced {
		t.Fatalf("invalid guard values were not normalized: %+v", cfg)
	}
	if cfg.ProviderProfiles[string(ModelFamilyXAI)] != GuardProfileBalanced {
		t.Fatalf("xai profile = %q, want balanced fallback", cfg.ProviderProfiles[string(ModelFamilyXAI)])
	}
	if cfg.Layers.CurrentUser.Mode != GuardModeInherit {
		t.Fatalf("current_user mode = %q, want inherit", cfg.Layers.CurrentUser.Mode)
	}
	if cfg.Rollout.Percent != 100 || cfg.Rollout.FallbackMode != GuardModeWarn {
		t.Fatalf("invalid rollout limits were not normalized: %+v", cfg.Rollout)
	}
	if len(cfg.Rollout.NewAPIUserAllowlist) != 1 || len(cfg.Rollout.APIKeyAllowlist) != 1 || len(cfg.Rollout.Protocols) != 1 || len(cfg.Rollout.Providers) != 1 {
		t.Fatalf("rollout lists were not normalized: %+v", cfg.Rollout)
	}
}
