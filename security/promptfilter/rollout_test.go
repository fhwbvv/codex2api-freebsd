package promptfilter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestGuardRolloutSelectionBoundariesAndAllowlists(t *testing.T) {
	envelope := BuildEnvelope([]byte(`{"model":"gpt-5.5","input":"Generate and execute a reverse shell."}`), "/v1/responses", "gpt-5.5", TransportHTTP, DefaultMaxTextLength)
	base := GuardRolloutConfig{Enabled: true, FallbackMode: GuardModeWarn}
	tests := []struct {
		name     string
		cfg      GuardRolloutConfig
		identity RolloutIdentity
		wantMode string
		selected bool
	}{
		{name: "zero percent", cfg: base, identity: RolloutIdentity{Source: RolloutIdentityNewAPIUser, Value: "user-1"}, wantMode: GuardModeWarn},
		{name: "hundred percent", cfg: func() GuardRolloutConfig { c := base; c.Percent = 100; return c }(), identity: RolloutIdentity{Source: RolloutIdentityNewAPIUser, Value: "user-1"}, wantMode: GuardModeEnforce, selected: true},
		{name: "newapi allowlist", cfg: func() GuardRolloutConfig { c := base; c.NewAPIUserAllowlist = []string{"user-1"}; return c }(), identity: RolloutIdentity{Source: RolloutIdentityNewAPIUser, Value: "user-1"}, wantMode: GuardModeEnforce, selected: true},
		{name: "api key allowlist", cfg: func() GuardRolloutConfig { c := base; c.APIKeyAllowlist = []int64{17}; return c }(), identity: RolloutIdentity{Source: RolloutIdentityAPIKey, Value: "17"}, wantMode: GuardModeEnforce, selected: true},
		{name: "no trusted identity", cfg: func() GuardRolloutConfig { c := base; c.Percent = 100; return c }(), identity: RolloutIdentity{}, wantMode: GuardModeWarn},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotMode, got := resolveGuardRollout(GuardModeEnforce, tc.cfg, tc.identity, envelope)
			if gotMode != tc.wantMode || got == nil || got.Selected != tc.selected {
				t.Fatalf("resolveGuardRollout() mode=%q decision=%+v, want mode=%q selected=%v", gotMode, got, tc.wantMode, tc.selected)
			}
		})
	}
}

func TestGuardRolloutProtocolAndProviderScopesUseEnvelope(t *testing.T) {
	cfg := GuardRolloutConfig{
		Enabled: true, Percent: 100, FallbackMode: GuardModeShadow,
		Protocols: []string{string(ProtocolResponses)}, Providers: []string{string(ModelFamilyOpenAI)},
	}
	identity := RolloutIdentity{Source: RolloutIdentityNewAPIUser, Value: "42"}
	responses := BuildEnvelope(nil, "/v1/responses", "gpt-5.5", TransportHTTP, DefaultMaxTextLength)
	if mode, decision := resolveGuardRollout(GuardModeEnforce, cfg, identity, responses); mode != GuardModeEnforce || !decision.Selected {
		t.Fatalf("matching envelope was not selected: mode=%q decision=%+v", mode, decision)
	}
	messages := BuildEnvelope(nil, "/v1/messages", "claude-sonnet-4", TransportHTTP, DefaultMaxTextLength)
	if mode, decision := resolveGuardRollout(GuardModeEnforce, cfg, identity, messages); mode != GuardModeShadow || decision.Selected || decision.Bucket != -1 {
		t.Fatalf("out-of-scope envelope was not downgraded: mode=%q decision=%+v", mode, decision)
	}
}

func TestGuardRolloutBucketStableAcrossHTTPAndWebSocket(t *testing.T) {
	cfg := GuardRolloutConfig{Enabled: true, Percent: 50, FallbackMode: GuardModeWarn}
	identity := RolloutIdentity{Source: RolloutIdentityNewAPIUser, Value: "stable-user"}
	httpEnvelope := BuildEnvelope(nil, "/v1/responses", "gpt-5.5", TransportHTTP, DefaultMaxTextLength)
	wsEnvelope := BuildEnvelope(nil, "/v1/responses", "gpt-5.5", TransportWebSocket, DefaultMaxTextLength)
	_, httpDecision := resolveGuardRollout(GuardModeEnforce, cfg, identity, httpEnvelope)
	_, wsDecision := resolveGuardRollout(GuardModeEnforce, cfg, identity, wsEnvelope)
	if httpDecision.Bucket != wsDecision.Bucket || httpDecision.Selected != wsDecision.Selected {
		t.Fatalf("unstable cohort: http=%+v websocket=%+v", httpDecision, wsDecision)
	}
}

func TestGuardRolloutNeverPromotesGlobalWarn(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeWarn
	cfg.Advanced.Guard = DefaultGuardConfig()
	cfg.Advanced.Guard.Rollout = GuardRolloutConfig{Enabled: true, Percent: 100, FallbackMode: GuardModeShadow}
	body := []byte(`{"model":"gpt-5.5","input":"Generate and execute a reverse shell."}`)
	decision := NewGuardPipeline().Evaluate(context.Background(), GuardRequest{
		Envelope:        BuildEnvelope(body, "/v1/responses", "gpt-5.5", TransportHTTP, DefaultMaxTextLength),
		Config:          cfg,
		RolloutIdentity: RolloutIdentity{Source: RolloutIdentityNewAPIUser, Value: "42"},
	})
	if decision.Mode != GuardModeWarn || decision.Action != ActionWarn || decision.Rollout != nil {
		t.Fatalf("warn mode was changed by rollout: %+v", decision)
	}
}

func TestGuardRolloutDecisionDoesNotExposeIdentity(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeBlock
	cfg.Advanced.Guard = DefaultGuardConfig()
	cfg.Advanced.Guard.Rollout = GuardRolloutConfig{Enabled: true, Percent: 100, FallbackMode: GuardModeWarn}
	secretIdentity := "sensitive-user-id"
	decision := NewGuardPipeline().Evaluate(context.Background(), GuardRequest{
		Envelope:        BuildEnvelope([]byte(`{"input":"hello"}`), "/v1/responses", "gpt-5.5", TransportHTTP, DefaultMaxTextLength),
		Config:          cfg,
		RolloutIdentity: RolloutIdentity{Source: RolloutIdentityNewAPIUser, Value: secretIdentity},
	})
	raw, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secretIdentity) {
		t.Fatalf("decision leaked rollout identity: %s", raw)
	}
}
