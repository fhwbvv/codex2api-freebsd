package proxy

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

var defaultPromptGuardPipeline = promptfilter.NewGuardPipeline()

const (
	promptGuardShadowQueueCapacity = 4096
	promptGuardShadowMaxBytes      = 64 * 1024 * 1024
	promptGuardShadowTaskTimeout   = 15 * time.Second
	promptGuardShadowDropLogEvery  = 5 * time.Second
)

var defaultPromptGuardShadowDispatcher = newPromptGuardShadowDispatcher()

var (
	promptGuardShadowEnqueued     atomic.Uint64
	promptGuardShadowCompleted    atomic.Uint64
	promptGuardShadowFallbackSync atomic.Uint64
	promptGuardShadowDropped      atomic.Uint64
	promptGuardShadowFailures     atomic.Uint64
	promptGuardShadowLastDropLog  atomic.Int64
)

type promptGuardShadowAuditJob struct {
	handler        *Handler
	audit          promptfilter.DeferredAudit
	auditContext   promptFilterAuditContext
	envelope       promptfilter.RequestEnvelope
	currentPreview string
	currentChars   int
	endpoint       string
	model          string
	source         string
	errorCode      string
	logMatches     bool
	queuedAt       time.Time
	retainedBytes  int64
}

type promptGuardShadowDispatcher struct {
	queue chan promptGuardShadowAuditJob
	mu    sync.Mutex

	workers        int
	desiredWorkers int
	pendingJobs    int
	pendingBytes   int64
}

type promptGuardShadowEnqueueStatus uint8

const (
	promptGuardShadowEnqueueInvalid promptGuardShadowEnqueueStatus = iota
	promptGuardShadowEnqueueAccepted
	promptGuardShadowEnqueueOverflow
)

func newPromptGuardShadowDispatcher() *promptGuardShadowDispatcher {
	return &promptGuardShadowDispatcher{queue: make(chan promptGuardShadowAuditJob, promptGuardShadowQueueCapacity)}
}

func (d *promptGuardShadowDispatcher) enqueue(job promptGuardShadowAuditJob, workers int, queueLimit int) (promptGuardShadowEnqueueStatus, string) {
	if d == nil || job.handler == nil || job.audit.SegmentCount() == 0 {
		return promptGuardShadowEnqueueInvalid, "invalid_job"
	}
	if workers < 1 {
		workers = 1
	}
	if workers > 16 {
		workers = 16
	}
	if queueLimit < 1 {
		queueLimit = 1
	}
	if queueLimit > promptGuardShadowQueueCapacity {
		queueLimit = promptGuardShadowQueueCapacity
	}
	job.retainedBytes = int64(job.audit.ByteSize() + len(job.currentPreview))
	if job.retainedBytes <= 0 {
		return promptGuardShadowEnqueueInvalid, "empty_job"
	}
	if job.retainedBytes > promptGuardShadowMaxBytes {
		return promptGuardShadowEnqueueOverflow, "job_too_large"
	}

	d.mu.Lock()
	if d.pendingJobs >= queueLimit {
		d.mu.Unlock()
		return promptGuardShadowEnqueueOverflow, "queue_limit"
	}
	if d.pendingBytes+job.retainedBytes > promptGuardShadowMaxBytes {
		d.mu.Unlock()
		return promptGuardShadowEnqueueOverflow, "retained_bytes_limit"
	}
	d.desiredWorkers = workers
	startWorkers := d.desiredWorkers - d.workers
	if startWorkers > 0 {
		d.workers = workers
	}
	d.pendingJobs++
	d.pendingBytes += job.retainedBytes
	d.mu.Unlock()
	for range startWorkers {
		go d.worker()
	}

	select {
	case d.queue <- job:
		promptGuardShadowEnqueued.Add(1)
		return promptGuardShadowEnqueueAccepted, ""
	default:
		d.complete(job.retainedBytes)
		return promptGuardShadowEnqueueOverflow, "queue_channel_full"
	}
}

func (d *promptGuardShadowDispatcher) worker() {
	for job := range d.queue {
		func() {
			defer d.complete(job.retainedBytes)
			defer func() {
				if recovered := recover(); recovered != nil {
					promptGuardShadowFailures.Add(1)
					log.Printf("prompt guard shadow audit panic: %v", recovered)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), promptGuardShadowTaskTimeout)
			err := runDeferredPromptGuardAudit(ctx, job)
			cancel()
			if err != nil {
				promptGuardShadowFailures.Add(1)
				log.Printf("prompt guard shadow audit failed: %v", err)
				return
			}
			promptGuardShadowCompleted.Add(1)
		}()
		if d.retireWorkerIfExcess() {
			return
		}
	}
}

func (d *promptGuardShadowDispatcher) retireWorkerIfExcess() bool {
	if d == nil {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.workers <= d.desiredWorkers {
		return false
	}
	d.workers--
	return true
}

func (d *promptGuardShadowDispatcher) complete(retainedBytes int64) {
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.pendingJobs > 0 {
		d.pendingJobs--
	}
	d.pendingBytes -= retainedBytes
	if d.pendingBytes < 0 {
		d.pendingBytes = 0
	}
	d.mu.Unlock()
}

type promptGuardEvaluation struct {
	Config   promptfilter.Config
	Envelope promptfilter.RequestEnvelope
	Decision promptfilter.Decision
	Verdict  promptfilter.Verdict
}

func (h *Handler) evaluatePromptGuard(c *gin.Context, rawBody []byte, signedBody []byte, endpoint string, model string, transport promptfilter.Transport) promptGuardEvaluation {
	cfg := h.promptFilterConfigForRequest(c)
	return h.evaluatePromptGuardWithConfig(c, cfg, rawBody, signedBody, endpoint, model, transport)
}

func (h *Handler) evaluatePromptGuardWithConfig(c *gin.Context, cfg promptfilter.Config, rawBody []byte, signedBody []byte, endpoint string, model string, transport promptfilter.Transport) promptGuardEvaluation {
	requestedModel, effectiveModel, trustedProfile, profileOverride, modeOverride, providerOverride, providerOverrideSet := h.resolvePromptGuardOverrides(c, cfg, signedBody, model)
	rolloutIdentity := h.resolvePromptGuardRolloutIdentity(c, cfg, signedBody)
	envelope := promptfilter.BuildEnvelopeWithModelsAndConfig(
		rawBody,
		endpoint,
		requestedModel,
		effectiveModel,
		transport,
		cfg,
	)
	applyPromptGuardProviderOverride(&envelope, cfg, trustedProfile, providerOverride, providerOverrideSet)
	extensionErrors := make([]string, 0, 3)
	var sessionPending *promptSessionCorrelationPending
	adapterOnly := envelope.AdapterUnclassified && len(envelope.Segments) == 0
	if !adapterOnly {
		var err error
		sessionPending, err = h.enrichPromptGuardSession(c, cfg, signedBody, &envelope)
		if err != nil {
			extensionErrors = append(extensionErrors, "session_correlation: "+err.Error())
		}
		if err := h.enrichPromptGuardAttachments(promptGuardRequestContext(c), cfg, &envelope); err != nil {
			extensionErrors = append(extensionErrors, "attachment_parser: "+err.Error())
		}
	}
	evaluation := h.evaluatePromptGuardEnvelope(c, cfg, envelope, trustedProfile, profileOverride, modeOverride, rolloutIdentity)
	if !adapterOnly && evaluation.Decision.ApplicationPromptKind == "" {
		if err := h.commitPromptGuardSession(c, cfg, sessionPending, evaluation.Decision); err != nil {
			extensionErrors = append(extensionErrors, "session_commit: "+err.Error())
		}
	}
	evaluation.Decision.Errors = append(evaluation.Decision.Errors, extensionErrors...)
	return evaluation
}

func (h *Handler) evaluatePromptGuardText(c *gin.Context, text string, endpoint string, model string) promptGuardEvaluation {
	cfg := h.promptFilterConfigForRequest(c)
	return h.evaluatePromptGuardTextWithConfig(c, cfg, text, endpoint, model)
}

func (h *Handler) evaluatePromptGuardTextWithConfig(c *gin.Context, cfg promptfilter.Config, text string, endpoint string, model string) promptGuardEvaluation {
	signedBody := ingressRequestBody(c, nil)
	requestedModel, effectiveModel, trustedProfile, profileOverride, modeOverride, providerOverride, providerOverrideSet := h.resolvePromptGuardOverrides(c, cfg, signedBody, model)
	rolloutIdentity := h.resolvePromptGuardRolloutIdentity(c, cfg, signedBody)
	envelope := promptfilter.BuildTextEnvelopeWithModelsAndConfig(
		text,
		endpoint,
		requestedModel,
		effectiveModel,
		promptfilter.TransportHTTP,
		cfg,
	)
	applyPromptGuardProviderOverride(&envelope, cfg, trustedProfile, providerOverride, providerOverrideSet)
	extensionErrors := make([]string, 0, 2)
	var sessionPending *promptSessionCorrelationPending
	adapterOnly := envelope.AdapterUnclassified && len(envelope.Segments) == 0
	if !adapterOnly {
		var err error
		sessionPending, err = h.enrichPromptGuardSession(c, cfg, signedBody, &envelope)
		if err != nil {
			extensionErrors = append(extensionErrors, "session_correlation: "+err.Error())
		}
	}
	evaluation := h.evaluatePromptGuardEnvelope(c, cfg, envelope, trustedProfile, profileOverride, modeOverride, rolloutIdentity)
	if !adapterOnly && evaluation.Decision.ApplicationPromptKind == "" {
		if err := h.commitPromptGuardSession(c, cfg, sessionPending, evaluation.Decision); err != nil {
			extensionErrors = append(extensionErrors, "session_commit: "+err.Error())
		}
	}
	evaluation.Decision.Errors = append(evaluation.Decision.Errors, extensionErrors...)
	return evaluation
}

func promptGuardRequestContext(c *gin.Context) context.Context {
	if c != nil && c.Request != nil {
		return c.Request.Context()
	}
	return context.Background()
}

func (h *Handler) resolvePromptGuardOverrides(c *gin.Context, cfg promptfilter.Config, signedBody []byte, model string) (string, string, bool, string, string, promptfilter.ModelFamily, bool) {
	requestedModel := model
	effectiveModel := model
	policyContext, verified := h.verifyNewAPIPolicyContext(c, cfg.Advanced.NewAPI, signedBody)
	if !verified || !policyContext.MetaVerified {
		return requestedModel, effectiveModel, false, "", "", promptfilter.ModelFamilyUnknown, false
	}
	if policyContext.Meta.RequestedModel != "" {
		requestedModel = policyContext.Meta.RequestedModel
	}
	if effectiveModel == "" && policyContext.Meta.UpstreamModel != "" {
		effectiveModel = policyContext.Meta.UpstreamModel
	}
	provider := promptfilter.ModelFamily(policyContext.Meta.Provider)
	return requestedModel, effectiveModel, true, policyContext.Meta.Profile, policyContext.Meta.Mode, provider, provider != promptfilter.ModelFamilyUnknown
}

func (h *Handler) resolvePromptGuardRolloutIdentity(c *gin.Context, cfg promptfilter.Config, signedBody []byte) promptfilter.RolloutIdentity {
	if policyContext, verified := h.verifyNewAPIPolicyContext(c, cfg.Advanced.NewAPI, signedBody); verified {
		if userID := strings.TrimSpace(policyContext.Identity.UserID); userID != "" {
			return promptfilter.RolloutIdentity{Source: promptfilter.RolloutIdentityNewAPIUser, Value: userID}
		}
	}
	if apiKeyID := requestAPIKeyID(c); apiKeyID > 0 {
		return promptfilter.RolloutIdentity{Source: promptfilter.RolloutIdentityAPIKey, Value: strconv.FormatInt(apiKeyID, 10)}
	}
	return promptfilter.RolloutIdentity{}
}

func applyPromptGuardProviderOverride(envelope *promptfilter.RequestEnvelope, cfg promptfilter.Config, trusted bool, provider promptfilter.ModelFamily, providerSet bool) {
	if envelope == nil || !trusted || !providerSet || !cfg.Advanced.Guard.AllowTrustedOverrides {
		return
	}
	switch provider {
	case promptfilter.ModelFamilyOpenAI, promptfilter.ModelFamilyAnthropic, promptfilter.ModelFamilyXAI:
		envelope.ModelFamily = provider
	}
}

func (h *Handler) evaluatePromptGuardEnvelope(c *gin.Context, cfg promptfilter.Config, envelope promptfilter.RequestEnvelope, trustedProfile bool, profileOverride string, modeOverride string, rolloutIdentity promptfilter.RolloutIdentity) promptGuardEvaluation {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	decision := defaultPromptGuardPipeline.Evaluate(ctx, promptfilter.GuardRequest{
		Envelope:        envelope,
		Config:          cfg,
		TrustedProfile:  trustedProfile,
		ProfileOverride: profileOverride,
		ModeOverride:    modeOverride,
		RolloutIdentity: rolloutIdentity,
	})
	if cfg.Enabled && decision.Mode == promptfilter.GuardModeOff && decision.ReasonCode != promptfilter.ReasonCodeAdapterUnclassified {
		return h.evaluateLegacyPromptGuard(c, ctx, cfg, envelope, decision.Profile)
	}
	verdict := decision.LegacyVerdict()
	verdict.Mode = legacyModeForPromptGuard(decision.Mode)
	// The detector selected for audit can come from history, tool output, or
	// session context. Semantic review, persisted request evidence, and the UI
	// preview must nevertheless describe only the direct current-user prompt.
	text := promptGuardReviewText(decision, envelope)
	verdict.FullText = text
	verdict.TextPreview = promptfilter.RedactedPreview(text, 500)
	verdict.ExtractedChars = len([]rune(text))
	// Semantic review must examine the text that produced the enforcement
	// decision. The persisted preview intentionally contains only the current
	// user prompt, so a history/tool/session enforcement signal cannot safely be
	// reviewed against that different text: a benign current prompt could clear
	// an administrator-enabled auxiliary-layer block. Clean-prompt sampling is
	// still allowed when the pipeline itself made no enforcement decision.
	inspectCurrentPrompt := promptGuardShouldInspect(decision, cfg)
	if inspectCurrentPrompt {
		advancedCfg := cfg
		verdict = h.applyPromptSemanticProtection(c, text, verdict, advancedCfg)
		if shouldReviewPromptGuardDecision(decision, verdict, cfg) {
			verdict = h.reviewPromptFilterVerdict(ctx, text, verdict, cfg)
		}
		if advancedCfg.Advanced.Risk.Enabled {
			verdict = h.applyPromptRisk(c, verdict, advancedCfg)
		}
	}
	decision = finalizePromptGuardDecision(decision, verdict)
	if decision.ApplicationPromptKind != "" && (decision.PrimaryOrigin == "" || decision.PrimaryOrigin == promptfilter.OriginSessionContext) {
		decision.ReasonCode = "application_prompt_" + decision.ApplicationPromptKind
	}
	verdict.Action = decision.Action
	verdict.Score = decision.Score
	verdict.RawScore = decision.RawScore
	verdict.Reason = decision.Reason
	return promptGuardEvaluation{Config: cfg, Envelope: envelope, Decision: decision, Verdict: verdict}
}

func (h *Handler) scheduleDeferredPromptGuardAudit(c *gin.Context, endpoint string, model string, source string, errorCode string, evaluation promptGuardEvaluation) {
	if h == nil || h.db == nil || source != "local_filter" || !evaluation.Config.LogMatches {
		return
	}
	audit, ok := evaluation.Decision.DeferredAudit()
	if !ok {
		return
	}
	envelopeMeta := evaluation.Envelope
	envelopeMeta.Segments = nil
	job := promptGuardShadowAuditJob{
		handler:        h,
		audit:          audit,
		auditContext:   h.capturePromptFilterAuditContext(c),
		envelope:       envelopeMeta,
		currentPreview: promptfilter.RedactedPreview(evaluation.Verdict.TextPreview, 500),
		currentChars:   evaluation.Verdict.ExtractedChars,
		endpoint:       endpoint,
		model:          model,
		source:         source,
		errorCode:      errorCode,
		logMatches:     evaluation.Config.LogMatches,
		queuedAt:       time.Now(),
	}
	performance := evaluation.Config.Advanced.Guard.Performance
	status, reason := defaultPromptGuardShadowDispatcher.enqueue(job, performance.ShadowWorkers, performance.ShadowQueueSize)
	if status == promptGuardShadowEnqueueAccepted {
		return
	}
	promptGuardShadowDropped.Add(1)
	logPromptGuardShadowDrop(job, reason)
}

func logPromptGuardShadowDrop(job promptGuardShadowAuditJob, reason string) {
	now := time.Now().UnixNano()
	last := promptGuardShadowLastDropLog.Load()
	if last != 0 && time.Duration(now-last) < promptGuardShadowDropLogEvery {
		return
	}
	if !promptGuardShadowLastDropLog.CompareAndSwap(last, now) {
		return
	}
	log.Printf(
		"prompt guard shadow audit dropped: reason=%s dropped_total=%d segments=%d bytes=%d endpoint=%s model=%s",
		reason,
		promptGuardShadowDropped.Load(),
		job.audit.SegmentCount(),
		job.audit.ByteSize(),
		job.endpoint,
		job.model,
	)
}

func runDeferredPromptGuardAudit(ctx context.Context, job promptGuardShadowAuditJob) error {
	if job.handler == nil || job.audit.SegmentCount() == 0 {
		return nil
	}
	if !promptGuardShadowLoggingEnabled(job) {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if wait := time.Since(job.queuedAt); wait >= 5*time.Second {
		log.Printf("prompt guard shadow audit queue wait=%dms segments=%d bytes=%d endpoint=%s model=%s", wait.Milliseconds(), job.audit.SegmentCount(), job.audit.ByteSize(), job.endpoint, job.model)
	}
	decision := defaultPromptGuardPipeline.EvaluateDeferred(ctx, job.audit)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("deferred evaluation deadline: %w", err)
	}
	if len(decision.Signals) == 0 && len(decision.Errors) == 0 {
		return nil
	}
	decision.ReasonCode = "prompt_policy_shadow_async"
	decision.Action = promptfilter.ActionAllow
	decision.Score = 0
	decision.RawScore = 0
	decision.StrikeEligible = false
	decision.Terminal = false
	verdict := decision.LegacyVerdict()
	verdict.Mode = legacyModeForPromptGuard(decision.Mode)
	verdict.Action = promptfilter.ActionAllow
	verdict.Score = 0
	verdict.RawScore = 0
	verdict.TextPreview = job.currentPreview
	verdict.ExtractedChars = job.currentChars
	verdict.Reason = decision.Reason
	if len(decision.Errors) > 0 {
		verdict.ReviewError = "shadow audit: " + strings.Join(decision.Errors, "; ")
	}
	// Configuration may change while this job waits or scans. The detector uses
	// the immutable request-time snapshot, but a newly disabled master/logging
	// switch must stop stale queued evidence from being persisted.
	if !promptGuardShadowLoggingEnabled(job) {
		return nil
	}
	if err := job.handler.logPromptFilterVerdictWithAuditContext(
		ctx,
		job.auditContext,
		job.endpoint,
		job.model,
		job.source,
		job.errorCode,
		verdict,
		&decision,
		&job.envelope,
		job.logMatches,
	); err != nil {
		return fmt.Errorf("persist deferred audit: %w", err)
	}
	return nil
}

func promptGuardShadowLoggingEnabled(job promptGuardShadowAuditJob) bool {
	if job.handler == nil || !job.logMatches {
		return false
	}
	if job.handler.store == nil {
		return true
	}
	cfg := job.handler.store.GetPromptFilterConfig()
	return cfg.Enabled && cfg.LogMatches
}

// Guard mode off disables the extensible pipeline, not the existing prompt
// filter. The master prompt-filter switch remains the only way to turn all
// input filtering off, so adopting GuardPipeline cannot accidentally create a
// bypass during rollout or via a trusted downstream mode override.
func (h *Handler) evaluateLegacyPromptGuard(c *gin.Context, ctx context.Context, cfg promptfilter.Config, envelope promptfilter.RequestEnvelope, profile string) promptGuardEvaluation {
	text := envelopeCurrentUserText(envelope)
	verdict := promptfilter.InspectText(text, cfg)
	verdict = h.applyPromptSemanticProtection(c, text, verdict, cfg)
	if shouldReviewPromptFilterVerdict(verdict, cfg) {
		verdict = h.reviewPromptFilterVerdict(ctx, text, verdict, cfg)
	}
	if cfg.Advanced.Risk.Enabled {
		verdict = h.applyPromptRisk(c, verdict, cfg)
	}
	decision := promptfilter.Decision{
		Enabled:        verdict.Enabled,
		Mode:           promptfilter.GuardModeOff,
		Profile:        profile,
		Action:         verdict.Action,
		WouldAction:    verdict.Action,
		Score:          verdict.Score,
		RawScore:       verdict.RawScore,
		AuditScore:     verdict.Score,
		AuditRawScore:  verdict.RawScore,
		Reason:         verdict.Reason,
		Terminal:       verdict.TerminalStrictHit || verdict.TerminalCategoryHit,
		StrikeEligible: verdict.Action == promptfilter.ActionBlock && verdict.SensitiveIntent && (verdict.TerminalStrictHit || verdict.TerminalCategoryHit),
	}
	if len(verdict.Matched) > 0 {
		decision.PrimaryOrigin = promptfilter.OriginCurrentUser
		decision.PrimaryDetector = "legacy_regex"
	}
	if decision.Terminal {
		decision.ReasonCode = "terminal_policy_match"
	} else if decision.Action == promptfilter.ActionBlock {
		decision.ReasonCode = "prompt_policy_match"
	} else if decision.Action == promptfilter.ActionWarn {
		decision.ReasonCode = "prompt_policy_warning"
	}
	return promptGuardEvaluation{Config: cfg, Envelope: envelope, Decision: decision, Verdict: verdict}
}

func legacyModeForPromptGuard(mode string) string {
	switch mode {
	case promptfilter.GuardModeEnforce:
		return promptfilter.ModeBlock
	case promptfilter.GuardModeWarn:
		return promptfilter.ModeWarn
	default:
		return promptfilter.ModeMonitor
	}
}

func promptGuardHasReviewableEnforcement(decision promptfilter.Decision) bool {
	if decision.Action == promptfilter.ActionAllow {
		return false
	}
	return decision.PrimaryOrigin == promptfilter.OriginCurrentUser || decision.PrimaryOrigin == promptfilter.OriginApplicationCandidate
}

func promptGuardShouldInspect(decision promptfilter.Decision, cfg promptfilter.Config) bool {
	if decision.ReasonCode == promptfilter.ReasonCodeAdapterUnclassified {
		return false
	}
	if promptGuardHasReviewableEnforcement(decision) {
		return true
	}
	if decision.Action != promptfilter.ActionAllow || !cfg.Advanced.Sidecar.ScanCleanEnabled {
		return false
	}
	// A verified ambient application prompt has already had its fixed policy
	// boilerplate removed. Its dynamic candidate is therefore safe to send to a
	// configured semantic scanner even when the local rules found no match.
	return decision.ApplicationPromptKind == "" || strings.TrimSpace(decision.ReviewText) != ""
}

func promptGuardReviewText(decision promptfilter.Decision, envelope promptfilter.RequestEnvelope) string {
	if decision.ApplicationPromptKind != "" || decision.PrimaryOrigin == promptfilter.OriginApplicationCandidate || decision.PrimaryOrigin == promptfilter.OriginCurrentUser {
		if candidate := strings.TrimSpace(decision.ReviewText); candidate != "" {
			return candidate
		}
	}
	return envelopeCurrentUserText(envelope)
}

func envelopeCurrentUserText(envelope promptfilter.RequestEnvelope) string {
	parts := make([]string, 0, 2)
	for _, segment := range envelope.Segments {
		if segment.Origin == promptfilter.OriginCurrentUser || (segment.Origin == promptfilter.OriginHistory && segment.Linked) {
			if text := strings.TrimSpace(segment.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func shouldReviewPromptGuardDecision(decision promptfilter.Decision, verdict promptfilter.Verdict, cfg promptfilter.Config) bool {
	if decision.Profile == promptfilter.GuardProfileStrict && decision.Terminal && decision.StrikeEligible {
		return false
	}
	return shouldReviewPromptFilterVerdict(verdict, cfg)
}

func finalizePromptGuardDecision(decision promptfilter.Decision, verdict promptfilter.Verdict) promptfilter.Decision {
	decision.Score = verdict.Score
	decision.RawScore = verdict.RawScore
	if strings.TrimSpace(verdict.Reason) != "" {
		decision.Reason = verdict.Reason
	}
	finalAction := verdict.Action
	switch decision.Mode {
	case promptfilter.GuardModeOff, promptfilter.GuardModeShadow:
		finalAction = promptfilter.ActionAllow
	case promptfilter.GuardModeWarn:
		if finalAction == promptfilter.ActionBlock {
			finalAction = promptfilter.ActionWarn
		}
	}
	decision.Action = finalAction
	if decision.ApplicationPromptKind != "" && finalAction != promptfilter.ActionAllow && decision.PrimaryOrigin == "" {
		decision.PrimaryOrigin = promptfilter.OriginApplicationCandidate
	}
	if decision.PrimaryOrigin == promptfilter.OriginApplicationCandidate {
		decision.StrikeEligible = false
	}
	if verdict.Reviewed && !verdict.ReviewFlagged {
		decision.Terminal = false
		decision.StrikeEligible = false
	}
	if finalAction != promptfilter.ActionBlock {
		decision.StrikeEligible = false
	}
	if finalAction == promptfilter.ActionWarn {
		decision.ReasonCode = "prompt_policy_warning"
	} else if finalAction == promptfilter.ActionBlock && strings.TrimSpace(decision.ReasonCode) == "" {
		decision.ReasonCode = "prompt_policy_classifier"
	} else if finalAction == promptfilter.ActionAllow && len(decision.Signals) > 0 {
		decision.ReasonCode = "prompt_policy_shadow"
	}
	return decision
}
