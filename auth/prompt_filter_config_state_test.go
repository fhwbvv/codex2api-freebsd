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
