package proxy

import (
	"context"
	"net/http"
	"strings"

	"github.com/codex2api/api"
	"github.com/codex2api/database"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// promptFilterFullTextMaxRunes limits the persisted redacted blocked-request text preview.
const promptFilterFullTextMaxRunes = 32000

// promptFilterMatchContextMaxRunes keeps audit evidence useful without storing
// an entire tool result, session transcript, or attachment in the log table.
const promptFilterMatchContextMaxRunes = 2000

func (h *Handler) inspectPromptFilterOpenAI(c *gin.Context, rawBody []byte, endpoint string, model string) bool {
	return h.inspectPromptFilterOpenAIWithBlockWriter(c, rawBody, endpoint, model, nil)
}

// InspectPromptFilterOpenAI exposes the same V1 Adapter/Guard path used by the
// synchronous proxy handlers to in-process V1 entry points such as async image
// jobs. writeBlock may preserve an endpoint-specific error envelope; verified
// NewAPI policy decisions still take precedence when they own the response.
func (h *Handler) InspectPromptFilterOpenAI(c *gin.Context, rawBody []byte, endpoint string, model string, writeBlock func(*gin.Context)) bool {
	h.capturePromptRequestIngress(c, rawBody)
	return h.inspectPromptFilterOpenAIWithBlockWriter(c, rawBody, endpoint, model, writeBlock)
}

func (h *Handler) inspectPromptFilterOpenAIWithBlockWriter(c *gin.Context, rawBody []byte, endpoint string, model string, writeBlock func(*gin.Context)) bool {
	if c != nil && c.GetBool("prompt_intelligence_internal") {
		return false
	}
	if h == nil || h.store == nil {
		return false
	}
	cfg := h.promptFilterConfigForRequest(c)
	// Skip envelope construction and body traversal when neither the local
	// filter nor a body-dependent extension is enabled (issue #417).
	if !promptfilter.RequiresRequestText(cfg) {
		return false
	}
	signedBody := ingressRequestBody(c, rawBody)
	evaluation := h.evaluatePromptGuardWithConfig(c, cfg, rawBody, signedBody, endpoint, model, promptfilter.TransportHTTP)
	verdict := evaluation.Verdict
	h.logPromptGuardEvaluation(c, endpoint, model, "local_filter", "", evaluation)
	if verdict.Action == promptfilter.ActionWarn {
		c.Header("X-Prompt-Filter-Warning", promptFilterWarningMessage(evaluation))
	}
	if verdict.Action != promptfilter.ActionBlock {
		return false
	}
	if h.sendNewAPIPolicyDecision(c, cfg, evaluation.Decision, verdict, rawBody, endpoint, model, signedBody) {
		return true
	}
	if writeBlock != nil {
		writeBlock(c)
		return true
	}
	api.SendErrorWithStatus(c, api.NewAPIError(
		api.ErrorCode("prompt_blocked"),
		"Request contains content blocked by prompt filter",
		api.ErrorTypeInvalidRequest,
	), http.StatusBadRequest)
	return true
}

func (h *Handler) inspectPromptFilterTextOpenAI(c *gin.Context, text string, endpoint string, model string) bool {
	if h == nil || h.store == nil {
		return false
	}
	cfg := h.promptFilterConfigForRequest(c)
	if !promptfilter.RequiresRequestText(cfg) {
		return false
	}
	evaluation := h.evaluatePromptGuardTextWithConfig(c, cfg, text, endpoint, model)
	verdict := evaluation.Verdict
	h.logPromptGuardEvaluation(c, endpoint, model, "local_filter", "", evaluation)
	if verdict.Action == promptfilter.ActionWarn {
		c.Header("X-Prompt-Filter-Warning", promptFilterWarningMessage(evaluation))
	}
	if verdict.Action != promptfilter.ActionBlock {
		return false
	}
	if h.sendNewAPIPolicyDecision(c, cfg, evaluation.Decision, verdict, []byte(text), endpoint, model, ingressRequestBody(c, nil)) {
		return true
	}
	api.SendErrorWithStatus(c, api.NewAPIError(
		api.ErrorCode("prompt_blocked"),
		"Request contains content blocked by prompt filter",
		api.ErrorTypeInvalidRequest,
	), http.StatusBadRequest)
	return true
}

func (h *Handler) inspectPromptFilterAnthropic(c *gin.Context, rawBody []byte, endpoint string, model string) bool {
	if h == nil || h.store == nil {
		return false
	}
	cfg := h.promptFilterConfigForRequest(c)
	if !promptfilter.RequiresRequestText(cfg) {
		return false
	}
	signedBody := ingressRequestBody(c, rawBody)
	evaluation := h.evaluatePromptGuardWithConfig(c, cfg, rawBody, signedBody, endpoint, model, promptfilter.TransportHTTP)
	verdict := evaluation.Verdict
	h.logPromptGuardEvaluation(c, endpoint, model, "local_filter", "", evaluation)
	if verdict.Action == promptfilter.ActionWarn {
		c.Header("X-Prompt-Filter-Warning", promptFilterWarningMessage(evaluation))
	}
	if verdict.Action == promptfilter.ActionBlock {
		if h.sendNewAPIPolicyDecision(c, cfg, evaluation.Decision, verdict, rawBody, endpoint, model, signedBody) {
			return true
		}
		sendAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "Request contains content blocked by prompt filter")
		return true
	}
	return false
}

func promptFilterWarningMessage(evaluation promptGuardEvaluation) string {
	if reason := strings.TrimSpace(evaluation.Verdict.Reason); reason != "" {
		return reason
	}
	if reasonCode := strings.TrimSpace(evaluation.Decision.ReasonCode); reasonCode != "" {
		return reasonCode
	}
	return "prompt_policy_warning"
}

func (h *Handler) logPromptFilterVerdict(c *gin.Context, endpoint string, model string, source string, errorCode string, verdict promptfilter.Verdict) {
	h.logPromptFilterVerdictWithDecision(c, endpoint, model, source, errorCode, verdict, nil, nil)
}

func (h *Handler) logPromptGuardEvaluation(c *gin.Context, endpoint string, model string, source string, errorCode string, evaluation promptGuardEvaluation) {
	h.logPromptFilterVerdictWithDecision(c, endpoint, model, source, errorCode, evaluation.Verdict, &evaluation.Decision, &evaluation.Envelope)
	h.scheduleDeferredPromptGuardAudit(c, endpoint, model, source, errorCode, evaluation)
}

func (h *Handler) logPromptFilterVerdictWithDecision(c *gin.Context, endpoint string, model string, source string, errorCode string, verdict promptfilter.Verdict, decision *promptfilter.Decision, envelope *promptfilter.RequestEnvelope) {
	if h == nil || h.db == nil || !verdict.Enabled {
		return
	}
	if source == "local_filter" && len(verdict.Matched) == 0 && verdict.Action == promptfilter.ActionAllow && verdict.ReviewError == "" && verdict.ReviewModel == "" && !promptFilterDecisionRequiresAudit(decision) {
		return
	}
	logMatches := true
	cfg := promptfilter.DefaultConfig()
	if h.store != nil {
		cfg = h.promptFilterConfigForRequest(c)
		logMatches = cfg.LogMatches
		if source == "local_filter" && !logMatches {
			return
		}
	}
	auditContext := h.capturePromptFilterAuditContext(c)
	input := h.buildPromptFilterLogInput(auditContext, endpoint, model, source, errorCode, verdict, decision, envelope, logMatches)
	if input == nil {
		return
	}
	priority := database.PromptFilterLogPriorityLow
	if verdict.Action == promptfilter.ActionWarn || verdict.Action == promptfilter.ActionBlock || source == "upstream_cyber_policy" {
		priority = database.PromptFilterLogPriorityHigh
	}
	// Audit persistence must never delay account selection, upstream connect, or
	// first-token forwarding. Saturation is observable through DB queue metrics
	// and deliberately has no synchronous fallback.
	_ = h.db.EnqueuePromptFilterLog(input, priority)
}

type promptFilterAuditContext struct {
	ClientIP     string
	APIKeyID     int64
	APIKeyName   string
	APIKeyMasked string
	Endpoint     string
	Protocol     string
	Provider     string
}

func (h *Handler) capturePromptFilterAuditContext(c *gin.Context) promptFilterAuditContext {
	if c == nil {
		return promptFilterAuditContext{}
	}
	input := &database.PromptFilterLogInput{ClientIP: c.ClientIP()}
	// Logging must never initiate signature replay-cache I/O. Prompt evaluation
	// has already verified and cached metadata when it was needed; response-side
	// cyber-policy logging simply omits metadata if no verified context exists.
	h.populateCachedVerifiedNewAPIAuditMeta(c, input)
	populatePromptFilterAPIKeyMeta(c, input)
	return promptFilterAuditContext{
		ClientIP:     input.ClientIP,
		APIKeyID:     input.APIKeyID,
		APIKeyName:   input.APIKeyName,
		APIKeyMasked: input.APIKeyMasked,
		Endpoint:     input.Endpoint,
		Protocol:     input.Protocol,
		Provider:     input.Provider,
	}
}

func (h *Handler) logPromptFilterVerdictWithAuditContext(_ context.Context, auditContext promptFilterAuditContext, endpoint string, model string, source string, errorCode string, verdict promptfilter.Verdict, decision *promptfilter.Decision, envelope *promptfilter.RequestEnvelope, logMatches bool) error {
	if h == nil || h.db == nil {
		return nil
	}
	input := h.buildPromptFilterLogInput(auditContext, endpoint, model, source, errorCode, verdict, decision, envelope, logMatches)
	if input == nil {
		return nil
	}
	// Deferred shadow evaluation already runs outside the request, but its
	// persistence still shares the same bounded low-priority queue so it cannot
	// compete synchronously with block/warn audit writes for database latency.
	_ = h.db.EnqueuePromptFilterLog(input, database.PromptFilterLogPriorityLow)
	return nil
}

func (h *Handler) buildPromptFilterLogInput(auditContext promptFilterAuditContext, endpoint string, model string, source string, errorCode string, verdict promptfilter.Verdict, decision *promptfilter.Decision, envelope *promptfilter.RequestEnvelope, logMatches bool) *database.PromptFilterLogInput {
	if h == nil || !verdict.Enabled {
		return nil
	}
	if source == "local_filter" && len(verdict.Matched) == 0 && verdict.Action == promptfilter.ActionAllow && verdict.ReviewError == "" && verdict.ReviewModel == "" && !promptFilterDecisionRequiresAudit(decision) {
		return nil
	}
	if source == "local_filter" && !logMatches {
		return nil
	}
	input := &database.PromptFilterLogInput{
		Source:          source,
		Endpoint:        endpoint,
		Model:           model,
		Action:          verdict.Action,
		Mode:            verdict.Mode,
		Score:           verdict.Score,
		Threshold:       verdict.Threshold,
		MatchedPatterns: promptfilter.MatchesJSON(verdict.Matched),
		TextPreview:     promptfilter.RedactedPreview(verdict.TextPreview, 500),
		MatchContext:    promptfilter.RedactedPreview(verdict.MatchContext, promptFilterMatchContextMaxRunes),
		ClientIP:        auditContext.ClientIP,
		ErrorCode:       errorCode,
		ReviewModel:     verdict.ReviewModel,
		ReviewFlagged:   verdict.ReviewFlagged,
		ReviewError:     verdict.ReviewError,
	}
	if envelope != nil {
		if envelope.Protocol != promptfilter.ProtocolUnknown {
			input.Protocol = string(envelope.Protocol)
		}
		if envelope.ModelFamily != promptfilter.ModelFamilyUnknown {
			input.Provider = string(envelope.ModelFamily)
		}
	}
	if auditContext.Endpoint != "" {
		input.Endpoint = auditContext.Endpoint
	}
	if auditContext.Protocol != "" {
		input.Protocol = auditContext.Protocol
	}
	if auditContext.Provider != "" {
		input.Provider = auditContext.Provider
	}
	if decision != nil {
		input.AuditScore = decision.AuditScore
		input.PolicyProfile = decision.Profile
		input.ReasonCode = decision.ReasonCode
		input.PrimaryOrigin = string(decision.PrimaryOrigin)
		input.StrikeEligible = decision.StrikeEligible
	}
	// 被拦截（block）的请求仅记录脱敏后的检查文本预览，便于排查触发原因，
	// 同时避免把 Authorization/API Key/token 等敏感值持久化到日志。
	if verdict.Action == promptfilter.ActionBlock {
		input.FullText = promptfilter.RedactedPreview(verdict.FullText, promptFilterFullTextMaxRunes)
	}
	input.APIKeyID = auditContext.APIKeyID
	input.APIKeyName = auditContext.APIKeyName
	input.APIKeyMasked = auditContext.APIKeyMasked
	return input
}

func promptFilterDecisionRequiresAudit(decision *promptfilter.Decision) bool {
	return decision != nil && decision.ReasonCode == promptfilter.ReasonCodeAdapterUnclassified
}

func (h *Handler) populateVerifiedNewAPIAuditMeta(c *gin.Context, input *database.PromptFilterLogInput) {
	if h == nil || h.store == nil || c == nil || input == nil {
		return
	}
	cfg := h.promptFilterConfigForRequest(c)
	policyContext, verified := h.verifyNewAPIPolicyContext(c, cfg.Advanced.NewAPI, ingressRequestBody(c, nil))
	if !verified || !policyContext.AuditMetaVerified {
		return
	}
	applyVerifiedNewAPIAuditMeta(policyContext, input)
}

func (h *Handler) populateCachedVerifiedNewAPIAuditMeta(c *gin.Context, input *database.PromptFilterLogInput) {
	if h == nil || c == nil || input == nil {
		return
	}
	cached, exists := c.Get(newAPIPolicyMetaContextKey)
	if !exists {
		return
	}
	policyContext, ok := cached.(verifiedNewAPIPolicyContext)
	if !ok || !policyContext.AuditMetaVerified {
		return
	}
	applyVerifiedNewAPIAuditMeta(policyContext, input)
}

func applyVerifiedNewAPIAuditMeta(policyContext verifiedNewAPIPolicyContext, input *database.PromptFilterLogInput) {
	if input == nil {
		return
	}
	if policyContext.Audit.Endpoint != "" {
		input.Endpoint = policyContext.Audit.Endpoint
	}
	if protocol := strings.TrimSpace(policyContext.Audit.Protocol); protocol != "" && !strings.EqualFold(protocol, string(promptfilter.ProtocolUnknown)) {
		input.Protocol = protocol
	}
	if provider := strings.TrimSpace(policyContext.Audit.Provider); provider != "" && !strings.EqualFold(provider, string(promptfilter.ModelFamilyUnknown)) {
		input.Provider = provider
	}
}

func (h *Handler) logUpstreamCyberPolicy(c *gin.Context, endpoint string, model string, body []byte) {
	if h == nil || h.store == nil {
		return
	}
	errorCode := upstreamCyberPolicyCode(body)
	if errorCode == "" {
		return
	}
	cfg := h.promptFilterConfigForRequest(c)
	verdict := promptfilter.Verdict{
		Enabled:   true,
		Mode:      cfg.Mode,
		Action:    promptfilter.ActionBlock,
		Score:     0,
		Threshold: cfg.Threshold,
		Reason:    "upstream returned cyber policy",
		// 上游 cyber_policy 没有本地提取文本，把脱敏后的上游错误体作为「详细内容」记录，
		// 方便在日志里看清触发详情，同时避免持久化敏感字段。
		FullText: promptfilter.RedactSensitive(string(body)),
	}
	h.logPromptFilterVerdict(c, endpoint, model, "upstream_cyber_policy", errorCode, verdict)
}

func upstreamCyberPolicyCode(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	raw := string(body)
	for _, path := range []string{"codex_error_info", "error.codex_error_info", "error.code", "code"} {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); strings.EqualFold(value, "cyber_policy") {
			return "cyber_policy"
		}
	}
	if strings.Contains(strings.ToLower(raw), "cyber_policy") || strings.Contains(strings.ToLower(raw), "cyber security risk") {
		return "cyber_policy"
	}
	return ""
}

func populatePromptFilterAPIKeyMeta(c *gin.Context, input *database.PromptFilterLogInput) {
	if c == nil || input == nil {
		return
	}
	if v, exists := c.Get(contextAPIKeyID); exists && v != nil {
		switch typed := v.(type) {
		case int64:
			input.APIKeyID = typed
		case int:
			input.APIKeyID = int64(typed)
		}
	}
	if v, exists := c.Get(contextAPIKeyName); exists && v != nil {
		if name, ok := v.(string); ok {
			input.APIKeyName = name
		}
	}
	if v, exists := c.Get(contextAPIKeyMasked); exists && v != nil {
		if masked, ok := v.(string); ok {
			input.APIKeyMasked = masked
		}
	}
}

func shouldReviewPromptFilterVerdict(verdict promptfilter.Verdict, cfg promptfilter.Config) bool {
	if verdict.Action != promptfilter.ActionWarn && verdict.Action != promptfilter.ActionBlock {
		return false
	}
	return promptfilter.NormalizeReviewConfig(cfg.Review).Ready()
}

func (h *Handler) reviewPromptFilterVerdict(ctx context.Context, text string, verdict promptfilter.Verdict, cfg promptfilter.Config) promptfilter.Verdict {
	flagged, model, err := promptfilter.DefaultReviewClient.ReviewText(ctx, text, cfg.Review)
	return promptfilter.ApplyReviewResult(verdict, flagged, model, err, cfg.Review)
}
