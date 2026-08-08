package promptfilter

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type productionInterceptReplayDocument struct {
	Samples []productionInterceptReplaySample `json:"samples"`
}

type productionInterceptReplaySample struct {
	Cluster                 string   `json:"cluster"`
	Endpoint                string   `json:"endpoint"`
	Model                   string   `json:"model"`
	Prompt                  string   `json:"prompt"`
	ExpectedAfterRefinement string   `json:"expected_after_refinement"`
	Rules                   []string `json:"rules"`
}

// TestProductionInterceptReplay is opt-in because its input is an anonymized
// production export kept outside the repository. It replays ordinary prompts
// through InspectText and closed Codex application requests through the full
// GuardPipeline, so source-classification regressions cannot hide behind a
// regex-only test.
func TestProductionInterceptReplay(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("PROMPT_FILTER_PRODUCTION_REPLAY"))
	if path == "" {
		t.Skip("set PROMPT_FILTER_PRODUCTION_REPLAY to an anonymized production review JSON")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read production replay: %v", err)
	}
	var document productionInterceptReplayDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode production replay: %v", err)
	}
	if len(document.Samples) == 0 {
		t.Fatal("production replay contains no samples")
	}

	cfg := productionRefinementConfig()
	for _, sample := range document.Samples {
		sample := sample
		t.Run(sample.Cluster, func(t *testing.T) {
			text := strings.NewReplacer(
				"⟦PF_HIT⟧", "",
				"⟦/PF_HIT⟧", "",
				"<uuid>", "00000000-0000-0000-0000-000000000001",
			).Replace(sample.Prompt)
			got := ActionAllow
			if strings.EqualFold(strings.TrimSpace(sample.Model), "codex-auto-review") {
				// Some legacy log rows reached the database preview limit before the
				// closing approval markers. They are useful for rule attribution but
				// cannot prove a closed application template, so fail closed in
				// production and skip only this incomplete offline replay artifact.
				if !strings.Contains(text, approvalRequestEnd) {
					t.Skip("production export ended before the closed approval suffix")
				}
				envelope := RequestEnvelope{
					Endpoint:       sample.Endpoint,
					Protocol:       ProtocolForEndpoint(sample.Endpoint),
					Transport:      TransportHTTP,
					RequestedModel: sample.Model,
					EffectiveModel: sample.Model,
					ModelFamily:    ModelFamilyOpenAI,
					Segments: []Segment{{
						Origin: OriginCurrentUser,
						Role:   "user",
						Text:   text,
						Trust:  SegmentTrustClientSupplied,
					}},
				}
				got = NewGuardPipeline().Evaluate(context.Background(), GuardRequest{Envelope: envelope, Config: cfg}).Action
			} else {
				got = InspectText(text, cfg).Action
			}
			want := ActionAllow
			if strings.EqualFold(strings.TrimSpace(sample.ExpectedAfterRefinement), string(ActionBlock)) {
				want = ActionBlock
			}
			if got != want {
				t.Fatalf("production replay action=%s want=%s original_rules=%v", got, want, sample.Rules)
			}
		})
	}
}
