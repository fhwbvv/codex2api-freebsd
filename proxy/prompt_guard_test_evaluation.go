package proxy

import (
	"strings"

	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

// PromptGuardTestEvaluation is the non-persisting result returned to
// administrative rule testing. Request metadata is included so callers can
// verify that endpoint and model selection reached the same policy profile
// resolver used by live V1 requests.
type PromptGuardTestEvaluation struct {
	Verdict  promptfilter.Verdict     `json:"verdict"`
	Decision promptfilter.Decision    `json:"decision"`
	Protocol promptfilter.Protocol    `json:"protocol"`
	Provider promptfilter.ModelFamily `json:"provider"`
	Endpoint string                   `json:"endpoint"`
	Model    string                   `json:"model"`
}

// EvaluatePromptGuardTextForTest evaluates one direct current-user prompt
// through the real GuardPipeline decision path without sending an upstream
// model generation request or persisting local operational state. The evaluation intentionally
// disables session correlation, cumulative risk and signed NewAPI identity;
// it also uses an isolated Handler without a database or runtime cache so it
// cannot write logs, extension breaker state, penalties or sidecar cache data.
// Configured semantic sidecar and secondary review calls remain available so
// the returned action reflects the final live decision as closely as possible;
// those external detector calls may consume their configured service quota.
func (h *Handler) EvaluatePromptGuardTextForTest(c *gin.Context, cfg promptfilter.Config, text string, endpoint string, model string) PromptGuardTestEvaluation {
	cfg.Enabled = true
	cfg.LogMatches = false
	cfg.Advanced.Session.Enabled = false
	cfg.Advanced.Risk.Enabled = false
	cfg.Advanced.NewAPI.Enabled = false
	cfg.Advanced.Guard.AllowTrustedOverrides = false
	// Administrative probes must not warm the process-wide exact-segment cache.
	// The cache is behavior-preserving, but keeping the probe fully isolated
	// makes repeated tests unable to alter live-request cache residency.
	cfg.Advanced.Guard.Performance.ExactSegmentCacheEnabled = false
	cfg = promptfilter.NormalizeConfig(cfg)

	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = "/v1/responses"
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "gpt-5.5"
	}
	envelope := promptfilter.BuildTextEnvelopeWithModelsAndConfig(
		text,
		endpoint,
		model,
		model,
		promptfilter.TransportHTTP,
		cfg,
	)

	// Never reuse the live handler's cache or database. evaluatePromptGuardEnvelope
	// itself is the production decision path, while this isolated handler makes
	// the administrative probe observational only.
	tester := NewHandler(nil, nil, nil, nil)
	evaluation := tester.evaluatePromptGuardEnvelope(
		c,
		cfg,
		envelope,
		false,
		"",
		"",
	)

	return PromptGuardTestEvaluation{
		Verdict:  evaluation.Verdict,
		Decision: evaluation.Decision,
		Protocol: evaluation.Envelope.Protocol,
		Provider: evaluation.Envelope.ModelFamily,
		Endpoint: endpoint,
		Model:    model,
	}
}
