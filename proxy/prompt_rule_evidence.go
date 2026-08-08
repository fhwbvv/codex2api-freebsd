package proxy

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/codex2api/database"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const promptRuleLearningEvidenceContextKey = "prompt_rule_learning_evidence"

type promptRuleLearningEvidence struct {
	EvaluationState string
	Text            string
	Endpoint        string
	Protocol        string
	Provider        string
	Model           string
	Action          string
	Mode            string
	Profile         string
	Score           int
	RawScore        int
	AuditScore      int
	AuditRawScore   int
	Threshold       int
	ReasonCode      string
	Reason          string
	PrimaryOrigin   string
	StrikeEligible  bool
	ReviewModel     string
	ReviewFlagged   bool
	ReviewError     string
	Matches         []promptfilter.Match
}

type upstreamCyberPolicyAttempt struct {
	Transport    string
	StatusCode   int
	AccountID    int64
	AttemptIndex int
}

type promptPolicyRoutingSnapshot struct {
	AccountName             string
	AccountPlatform         string
	AccountGroupIDs         []int64
	AccountGroupNames       []string
	APIKeyAllowedGroupIDs   []int64
	APIKeyAllowedGroupNames []string
}

func (h *Handler) capturePromptPolicyRoutingSnapshot(accountID, apiKeyID int64) promptPolicyRoutingSnapshot {
	if h == nil || h.store == nil {
		return promptPolicyRoutingSnapshot{}
	}
	snapshot := promptPolicyRoutingSnapshot{}
	if account := h.store.FindByID(accountID); account != nil {
		account.Mu().RLock()
		snapshot.AccountName = strings.TrimSpace(account.Email)
		snapshot.AccountPlatform = strings.TrimSpace(account.UpstreamType)
		account.Mu().RUnlock()
		if snapshot.AccountPlatform == "" {
			snapshot.AccountPlatform = database.UpstreamChannelCodex
		}
		snapshot.AccountGroupIDs = account.GroupIDSnapshot()
		snapshot.AccountGroupNames = h.store.ResolveGroupNames(snapshot.AccountGroupIDs)
	}
	snapshot.APIKeyAllowedGroupIDs = h.store.GetAPIKeyAllowedGroups(apiKeyID)
	snapshot.APIKeyAllowedGroupNames = h.store.ResolveGroupNames(snapshot.APIKeyAllowedGroupIDs)
	return snapshot
}

// capturePromptRuleLearningEvidence retains the immutable local decision that
// was synchronously applied before forwarding. Deferred shadow audits remain
// separate logs and never rewrite this snapshot.
func (h *Handler) capturePromptRuleLearningEvidence(c *gin.Context, endpoint, model string, evaluation promptGuardEvaluation) {
	if c == nil {
		return
	}
	text := strings.TrimSpace(promptGuardReviewText(evaluation.Decision, evaluation.Envelope))
	protocol := ""
	if evaluation.Envelope.Protocol != promptfilter.ProtocolUnknown {
		protocol = string(evaluation.Envelope.Protocol)
	}
	provider := ""
	if evaluation.Envelope.ModelFamily != promptfilter.ModelFamilyUnknown {
		provider = string(evaluation.Envelope.ModelFamily)
	}
	c.Set(promptRuleLearningEvidenceContextKey, promptRuleLearningEvidence{
		EvaluationState: database.PromptPolicyEvaluationCompleted,
		Text:            text,
		Endpoint:        endpoint,
		Protocol:        protocol,
		Provider:        provider,
		Model:           model,
		Action:          evaluation.Verdict.Action,
		Mode:            evaluation.Verdict.Mode,
		Profile:         evaluation.Decision.Profile,
		Score:           evaluation.Verdict.Score,
		RawScore:        evaluation.Verdict.RawScore,
		AuditScore:      evaluation.Decision.AuditScore,
		AuditRawScore:   evaluation.Decision.AuditRawScore,
		Threshold:       evaluation.Verdict.Threshold,
		ReasonCode:      evaluation.Decision.ReasonCode,
		Reason:          evaluation.Decision.Reason,
		PrimaryOrigin:   string(evaluation.Decision.PrimaryOrigin),
		StrikeEligible:  evaluation.Decision.StrikeEligible,
		ReviewModel:     evaluation.Verdict.ReviewModel,
		ReviewFlagged:   evaluation.Verdict.ReviewFlagged,
		ReviewError:     evaluation.Verdict.ReviewError,
		Matches:         append([]promptfilter.Match(nil), evaluation.Verdict.Matched...),
	})
}

func (h *Handler) enqueueUpstreamCyberPolicyEvidence(c *gin.Context, endpoint, model, errorCode string, body []byte, attempt upstreamCyberPolicyAttempt) (string, bool) {
	if h == nil || h.db == nil || c == nil {
		return "", false
	}
	requestCorrelationID := ensurePromptPolicyRequestCorrelationID(c)
	incidentID := uuid.NewString()
	captured, available := h.promptRuleLearningEvidenceForUpstreamFailure(c, endpoint, model)
	if endpoint == "" {
		endpoint = captured.Endpoint
	}
	if model == "" {
		model = captured.Model
	}
	audit := h.capturePromptFilterAuditContext(c)
	routing := h.capturePromptPolicyRoutingSnapshot(attempt.AccountID, audit.APIKeyID)
	protocol := captured.Protocol
	if audit.Protocol != "" {
		protocol = audit.Protocol
	}
	provider := captured.Provider
	if audit.Provider != "" {
		provider = audit.Provider
	}
	platform := promptPolicyPlatform(c)
	transport := strings.TrimSpace(attempt.Transport)
	if transport == "" {
		transport = string(promptfilter.TransportHTTP)
	}
	matchesJSON, _ := json.Marshal(captured.Matches)
	if len(matchesJSON) == 0 || string(matchesJSON) == "null" {
		matchesJSON = []byte("[]")
	}
	fingerprint := promptfilter.PromptEvidenceFingerprint(captured.Text)
	if fingerprint == "" {
		fingerprint = promptfilter.StableEvidenceFingerprint("cyber-unavailable", requestCorrelationID+"\x00"+endpoint+"\x00"+model)
	}
	preview := promptfilter.RedactedPreview(captured.Text, 2000)
	checkText := promptfilter.RedactedPreview(captured.Text, promptFilterFullTextMaxRunes)
	state := captured.EvaluationState
	if state == "" {
		state = database.PromptPolicyEvaluationUnavailable
	}
	outcome := promptPolicyLocalOutcome(captured)
	localComparison := promptPolicyLocalComparison(captured, available)
	incident := database.PromptPolicyIncidentInput{
		IncidentID:              incidentID,
		RequestCorrelationID:    requestCorrelationID,
		AttemptIndex:            attempt.AttemptIndex,
		Transport:               transport,
		Endpoint:                endpoint,
		Protocol:                protocol,
		Provider:                provider,
		Model:                   model,
		StatusCode:              attempt.StatusCode,
		AccountID:               attempt.AccountID,
		AccountName:             routing.AccountName,
		AccountPlatform:         routing.AccountPlatform,
		AccountGroupIDs:         routing.AccountGroupIDs,
		AccountGroupNames:       routing.AccountGroupNames,
		APIKeyID:                audit.APIKeyID,
		APIKeyName:              audit.APIKeyName,
		APIKeyMasked:            audit.APIKeyMasked,
		APIKeyAllowedGroupIDs:   routing.APIKeyAllowedGroupIDs,
		APIKeyAllowedGroupNames: routing.APIKeyAllowedGroupNames,
		Platform:                platform,
		NewAPIPolicyStatus:      audit.NewAPIPolicyStatus,
		NewAPIPlatform:          audit.NewAPIPlatform,
		NewAPIUserID:            audit.NewAPIUserID,
		NewAPIUserName:          audit.NewAPIUserName,
		NewAPIUserEmail:         audit.NewAPIUserEmail,
		NewAPIUserGroup:         audit.NewAPIUserGroup,
		NewAPIRequestID:         audit.NewAPIRequestID,
		SessionHash:             audit.SessionHash,
		ClientIPHash:            audit.ClientIPHash,
		SourceRef:               promptPolicySourceRef(c, requestCorrelationID),
		UpstreamErrorCode:       errorCode,
		UpstreamError:           promptfilter.RedactedPreview(promptfilter.RedactSensitive(string(body)), 8192),
		LocalEvaluationState:    state,
		LocalOutcome:            outcome,
		LocalAction:             captured.Action,
		LocalThreshold:          captured.Threshold,
		LocalMode:               captured.Mode,
		LocalPolicyProfile:      captured.Profile,
		LocalReasonCode:         captured.ReasonCode,
		LocalReason:             captured.Reason,
		LocalPrimaryOrigin:      captured.PrimaryOrigin,
		LocalStrikeEligible:     captured.StrikeEligible,
		LocalReviewModel:        captured.ReviewModel,
		LocalReviewFlagged:      captured.ReviewFlagged,
		LocalReviewError:        captured.ReviewError,
		LocalMatchedPatterns:    string(matchesJSON),
		PromptFingerprint:       fingerprint,
		PromptPreview:           preview,
		PromptText:              checkText,
		PromptAvailable:         available,
		LocalComparison:         localComparison,
		ObservedAt:              time.Now().UTC(),
	}
	if state == database.PromptPolicyEvaluationCompleted {
		incident.LocalScore = promptPolicyInt(captured.Score)
		incident.LocalRawScore = promptPolicyInt(captured.RawScore)
		incident.LocalAuditScore = promptPolicyInt(captured.AuditScore)
		incident.LocalAuditRawScore = promptPolicyInt(captured.AuditRawScore)
	}
	metadata, _ := json.Marshal(map[string]any{
		"incident_id": incidentID, "request_correlation_id": requestCorrelationID,
		"error_code": errorCode, "endpoint": endpoint, "local_evaluation_state": state,
		"local_outcome": outcome, "local_action": captured.Action, "local_score": incident.LocalScore,
		"local_audit_score": incident.LocalAuditScore, "local_reason_code": captured.ReasonCode,
		"local_matches": captured.Matches, "platform": platform, "prompt_available": available, "local_comparison": localComparison,
		"account_id": attempt.AccountID, "account_groups": routing.AccountGroupNames,
		"newapi_policy_status": audit.NewAPIPolicyStatus, "newapi_platform": audit.NewAPIPlatform,
	})
	candidate := database.PromptRuleCandidateInput{
		Fingerprint:   fingerprint,
		Kind:          database.PromptRuleCandidateKindEvidence,
		Source:        database.PromptRuleCandidateSourceUpstreamCyberPolicy,
		SamplePreview: preview,
		Rationale:     "上游返回 cyber_policy，等待归因和候选规则审核",
	}
	evidence := database.PromptRuleCandidateEvidenceInput{
		SourceKind: database.PromptRuleCandidateSourceUpstreamCyberPolicy,
		SourceRef:  incident.SourceRef,
		SourceRefHash: promptfilter.StableEvidenceFingerprint(
			"cyber-incident", incidentID,
		),
		SamplePreview:          preview,
		MetadataJSON:           string(metadata),
		Protocol:               protocol,
		Provider:               provider,
		Model:                  model,
		APIKeyID:               audit.APIKeyID,
		APIKeyName:             audit.APIKeyName,
		PromptPolicyIncidentID: incidentID,
		ObservedAt:             incident.ObservedAt,
	}
	accepted := h.db.EnqueuePromptPolicyIncident(&incident, &candidate, &evidence)
	if accepted {
		if policy, subjectKey, trusted := h.promptRiskTrustPolicyForRequest(c); trusted {
			h.suspendPromptRiskTrustPolicy(policy, subjectKey, "上游返回 cyber_policy，恢复同步模型复核")
		}
	}
	return incidentID, accepted
}

func promptPolicyLocalComparison(captured promptRuleLearningEvidence, promptAvailable bool) string {
	if captured.EvaluationState == database.PromptPolicyEvaluationLegacyUnknown {
		return database.PromptPolicyComparisonLegacyUnknown
	}
	if captured.EvaluationState != database.PromptPolicyEvaluationCompleted {
		return database.PromptPolicyComparisonNotComparable
	}
	if promptPolicyLocalOutcome(captured) != database.PromptPolicyOutcomeNoHit {
		return database.PromptPolicyComparisonLocalDetected
	}
	if !promptAvailable {
		return database.PromptPolicyComparisonEvidenceUnavailable
	}
	return database.PromptPolicyComparisonConfirmedMiss
}

func (h *Handler) promptRuleLearningEvidenceForUpstreamFailure(c *gin.Context, endpoint, model string) (promptRuleLearningEvidence, bool) {
	if raw, exists := c.Get(promptRuleLearningEvidenceContextKey); exists {
		if captured, ok := raw.(promptRuleLearningEvidence); ok {
			return captured, strings.TrimSpace(captured.Text) != ""
		}
	}
	return h.capturePromptRuleLearningEvidenceOnUpstreamFailure(c, endpoint, model)
}

// capturePromptRuleLearningEvidenceOnUpstreamFailure is a rare-path fallback
// used only after CY when local evaluation did not run. It extracts and redacts
// the prompt without assigning a synthetic allow score.
func (h *Handler) capturePromptRuleLearningEvidenceOnUpstreamFailure(c *gin.Context, endpoint, model string) (promptRuleLearningEvidence, bool) {
	fallback := promptRuleLearningEvidence{
		EvaluationState: database.PromptPolicyEvaluationUnavailable,
		Endpoint:        endpoint,
		Model:           model,
	}
	if h == nil || h.store == nil || c == nil {
		return fallback, false
	}
	raw, exists := c.Get("raw_body")
	body, ok := raw.([]byte)
	if !exists || !ok || len(body) == 0 {
		return fallback, false
	}
	cfg := h.promptFilterConfigForRequest(c)
	envelope := promptfilter.BuildEnvelopeWithModelsAndPerformance(
		body, endpoint, model, model, promptfilter.TransportHTTP, cfg.MaxTextLength, cfg.Advanced.Guard.Performance,
	)
	text := strings.TrimSpace(envelopeCurrentUserText(envelope))
	if text == "" {
		return fallback, false
	}
	fallback.EvaluationState = database.PromptPolicyEvaluationNotRun
	fallback.Text = text
	fallback.Mode = cfg.Mode
	fallback.Threshold = cfg.Threshold
	if envelope.Protocol != promptfilter.ProtocolUnknown {
		fallback.Protocol = string(envelope.Protocol)
	}
	if envelope.ModelFamily != promptfilter.ModelFamilyUnknown {
		fallback.Provider = string(envelope.ModelFamily)
	}
	return fallback, true
}

func promptPolicyLocalOutcome(captured promptRuleLearningEvidence) string {
	switch captured.Action {
	case promptfilter.ActionBlock:
		return database.PromptPolicyOutcomeBlock
	case promptfilter.ActionWarn:
		return database.PromptPolicyOutcomeWarn
	}
	if captured.EvaluationState == database.PromptPolicyEvaluationCompleted && (captured.AuditScore > 0 || len(captured.Matches) > 0) {
		return database.PromptPolicyOutcomeAuditHit
	}
	return database.PromptPolicyOutcomeNoHit
}

func promptPolicyPlatform(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if raw, exists := c.Get(newAPIPolicyMetaContextKey); exists {
		if policyContext, ok := raw.(verifiedNewAPIPolicyContext); ok && strings.TrimSpace(policyContext.Platform) != "" {
			return policyContext.Platform
		}
	}
	if raw, exists := c.Get(newAPIIdentityContextKey); exists {
		if identityContext, ok := raw.(verifiedNewAPIIdentityContext); ok {
			return identityContext.Platform
		}
	}
	return ""
}

func promptPolicySourceRef(c *gin.Context, fallback string) string {
	if c != nil {
		for _, name := range []string{"X-NewAPI-Request-ID", "X-Request-ID"} {
			if value := strings.TrimSpace(c.GetHeader(name)); value != "" {
				return value
			}
		}
	}
	return fallback
}

func promptPolicyInt(value int) *int {
	result := value
	return &result
}

func acceptedPromptPolicyIncidentID(incidentID string, accepted bool) string {
	if !accepted {
		return ""
	}
	return incidentID
}

func upstreamPromptPolicyTransport(stream, viaWebsocket bool) string {
	if viaWebsocket {
		return "websocket"
	}
	if stream {
		return "sse"
	}
	return "http"
}

func (h *Handler) logPromptPolicyRetryUsage(c *gin.Context, input database.UsageLogInput, incidentID string) {
	if strings.TrimSpace(incidentID) == "" {
		return
	}
	input.PromptPolicyIncidentID = incidentID
	input.IsRetryAttempt = true
	h.logUsageForRequest(c, &input)
}
