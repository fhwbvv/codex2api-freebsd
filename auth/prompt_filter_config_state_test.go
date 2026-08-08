package auth

import (
	"encoding/json"
	"testing"

	"github.com/codex2api/database"
	"github.com/codex2api/security/promptfilter"
)

func TestSetPromptFilterConfigPreservesAdvancedUnknownFields(t *testing.T) {
	store := &Store{}
	cfg := promptfilter.DefaultConfig()
	raw := `{"guard":{"mode":"shadow","future_guard":true},"future_root":{"revision":3}}`
	document, err := promptfilter.ParseAdvancedConfigDocument(raw)
	if err != nil {
		t.Fatalf("ParseAdvancedConfigDocument: %v", err)
	}
	cfg.Advanced = document.Effective
	if err := store.SetPromptFilterConfigWithAdvancedRaw(cfg, document.Raw); err != nil {
		t.Fatalf("SetPromptFilterConfigWithAdvancedRaw: %v", err)
	}

	updated := store.GetPromptFilterConfig()
	updated.Advanced.Output.Enabled = true
	store.SetPromptFilterConfig(updated)

	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(store.GetPromptFilterAdvancedConfig()), &root); err != nil {
		t.Fatalf("decode stored raw config: %v", err)
	}
	if _, ok := root["future_root"]; !ok {
		t.Fatal("future_root was lost after backend runtime update")
	}
	var guard map[string]json.RawMessage
	if err := json.Unmarshal(root["guard"], &guard); err != nil {
		t.Fatalf("decode guard: %v", err)
	}
	if _, ok := guard["future_guard"]; !ok {
		t.Fatal("future_guard was lost after backend runtime update")
	}
	if !store.GetPromptFilterConfig().Advanced.Output.Enabled {
		t.Fatal("effective output.enabled = false, want true")
	}
}

func TestPromptFilterConfigFromInvalidAdvancedSettingsUsesSafeDefaultDocument(t *testing.T) {
	settings := &database.SystemSettings{}
	settings.PromptFilterAdvancedConfig = `{"guard":`

	cfg, raw := promptFilterConfigFromSettings(settings)
	if cfg.Advanced.Guard.Mode != promptfilter.DefaultAdvancedConfig().Guard.Mode {
		t.Fatalf("guard.mode = %q, want default %q", cfg.Advanced.Guard.Mode, promptfilter.DefaultAdvancedConfig().Guard.Mode)
	}
	if _, err := promptfilter.ParseAdvancedConfigDocument(raw); err != nil {
		t.Fatalf("fallback raw document is invalid: %v", err)
	}
}

func TestPromptFilterConfigLoadsReviewAdapterFromAdvancedSettings(t *testing.T) {
	settings := &database.SystemSettings{
		PromptFilterReviewEnabled:        true,
		PromptFilterReviewAPIKey:         "stored-key",
		PromptFilterReviewBaseURL:        "https://review.example",
		PromptFilterReviewModel:          "review-model",
		PromptFilterReviewTimeoutSeconds: 7,
		PromptFilterAdvancedConfig: `{"review_adapter":{
			"request_mode":"chat_completions",
			"scope":"local_candidates",
			"system_prompt":"system",
			"user_prompt_template":"<user_input>{{text}}</user_input>",
			"payload_template":"",
			"confidence_threshold":0.76,
			"max_concurrent":19,
			"max_text_length":12000
		}}`,
	}
	cfg, _ := promptFilterConfigFromSettings(settings)
	adapter := cfg.Review.Adapter
	if adapter.RequestMode != promptfilter.ReviewRequestModeChatCompletions || adapter.Scope != promptfilter.ReviewScopeLocalCandidates || adapter.ConfidenceThreshold != 0.76 || adapter.MaxConcurrent != 19 || adapter.MaxTextLength != 12000 {
		t.Fatalf("review adapter = %+v", adapter)
	}
}

func TestSetPromptFilterConfigQuarantinesUnsafeLegacyCustomRule(t *testing.T) {
	store := &Store{}
	cfg := promptfilter.DefaultConfig()
	enabled := true
	cfg.CustomPatterns = []promptfilter.PatternConfig{{
		Name:     "all",
		Pattern:  `(?i)\ball\b`,
		Weight:   600,
		Category: "legacy",
		Strict:   true,
		Enabled:  &enabled,
	}}

	store.SetPromptFilterConfig(cfg)
	got := store.GetPromptFilterConfig().CustomPatterns
	if len(got) != 1 {
		t.Fatalf("custom patterns = %#v", got)
	}
	if got[0].Enabled == nil || *got[0].Enabled {
		t.Fatalf("unsafe legacy rule enabled = %#v, want false", got[0].Enabled)
	}
	if got[0].Name != "all" || got[0].Pattern != `(?i)\ball\b` || got[0].Weight != 600 || got[0].Category != "legacy" || !got[0].Strict {
		t.Fatalf("quarantine did not preserve rule content: %#v", got[0])
	}
	encoded := promptfilter.MarshalCustomPatterns(got)
	var roundTrip []promptfilter.PatternConfig
	if err := json.Unmarshal([]byte(encoded), &roundTrip); err != nil {
		t.Fatalf("decode marshaled patterns: %v", err)
	}
	if len(roundTrip) != 1 || roundTrip[0].Enabled == nil || *roundTrip[0].Enabled {
		t.Fatalf("marshaled quarantine = %#v", roundTrip)
	}
}
