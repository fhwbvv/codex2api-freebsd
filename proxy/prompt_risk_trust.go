package proxy

import (
	"context"
	"hash/crc32"
	"log"
	"sync"
	"time"

	"github.com/codex2api/database"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

const promptRiskTrustBypassAuditInterval = 10 * time.Minute

var promptRiskTrustBypassAudit sync.Map // subject key -> time.Time

func (h *Handler) promptRiskTrustPolicyForRequest(c *gin.Context) (database.PromptRiskTrustPolicy, string, bool) {
	if h == nil || h.store == nil || c == nil {
		return database.PromptRiskTrustPolicy{}, "", false
	}
	raw, ok := c.Get(newAPIIdentityContextKey)
	if !ok {
		return database.PromptRiskTrustPolicy{}, "", false
	}
	identity, ok := raw.(verifiedNewAPIIdentityContext)
	if !ok {
		return database.PromptRiskTrustPolicy{}, "", false
	}
	subjectKey := database.PromptRiskNewAPIUserSubjectKey(identity.Platform, identity.Identity.UserID)
	policy, ok := h.store.GetPromptRiskTrustPolicy(subjectKey, time.Now().UTC())
	return policy, subjectKey, ok
}

func promptRiskTrustCanBypassReview(decision promptfilter.Decision, verdict promptfilter.Verdict, reviewText string) bool {
	if reviewText == "" || decision.Action != promptfilter.ActionAllow || verdict.Action != promptfilter.ActionAllow {
		return false
	}
	if decision.AuditScore > 0 || decision.AuditRawScore > 0 || len(decision.Signals) > 0 || len(verdict.Matched) > 0 {
		return false
	}
	return len(decision.Errors) == 0 && verdict.ReviewError == ""
}

func promptRiskTrustShouldSuspend(decision promptfilter.Decision, verdict promptfilter.Verdict) bool {
	return decision.Action != promptfilter.ActionAllow || verdict.Action != promptfilter.ActionAllow ||
		decision.AuditScore > 0 || decision.AuditRawScore > 0 || len(decision.Signals) > 0 ||
		len(verdict.Matched) > 0
}

func promptRiskTrustReviewShouldSuspend(verdict promptfilter.Verdict) bool {
	return verdict.ReviewError == "" && (verdict.ReviewFlagged || verdict.Action == promptfilter.ActionBlock)
}

func promptRiskTrustReviewRequired(c *gin.Context, cfg promptfilter.Config, policy database.PromptRiskTrustPolicy, subjectKey string) bool {
	adaptive := cfg.Advanced.AdaptiveReview
	if policy.ID <= 0 || subjectKey == "" {
		return true
	}
	if !adaptive.Enabled {
		return policy.Source == database.PromptRiskTrustSourceAutomatic
	}
	now := time.Now().UTC()
	forceInterval := time.Duration(adaptive.ForceReviewIntervalMinutes) * time.Minute
	forceDue := policy.LastModelReviewAt == nil || forceInterval <= 0 || now.Sub(policy.LastModelReviewAt.UTC()) >= forceInterval
	if forceDue {
		return true
	}
	if adaptive.SamplePercent <= 0 {
		return false
	}
	correlationID := ensurePromptPolicyRequestCorrelationID(c)
	bucket := crc32.ChecksumIEEE([]byte(subjectKey+"\x00"+correlationID)) % 100
	return int(bucket) < adaptive.SamplePercent
}

func (h *Handler) recordPromptRiskTrustBypass(c *gin.Context, policy database.PromptRiskTrustPolicy, subjectKey string) {
	if h == nil || h.db == nil || policy.ID <= 0 || subjectKey == "" {
		return
	}
	now := time.Now().UTC()
	if raw, ok := promptRiskTrustBypassAudit.Load(subjectKey); ok {
		if last, valid := raw.(time.Time); valid && now.Sub(last) < promptRiskTrustBypassAuditInterval {
			return
		}
	}
	promptRiskTrustBypassAudit.Store(subjectKey, now)
	requestIDHash := promptfilter.StableEvidenceFingerprint("adaptive-trust-request", ensurePromptPolicyRequestCorrelationID(c))
	h.db.RunBackgroundTask(func(ctx context.Context) {
		_ = h.db.RecordPromptRiskTrustBypass(ctx, policy.ID, policy.SubjectType, subjectKey, requestIDHash)
	})
}

func (h *Handler) recordPromptRiskTrustModelReview(c *gin.Context, policy database.PromptRiskTrustPolicy, subjectKey string) {
	if h == nil || h.store == nil || policy.ID <= 0 || subjectKey == "" {
		return
	}
	now := time.Now().UTC()
	h.store.RecordPromptRiskTrustModelReview(subjectKey, now)
	if h.db == nil {
		return
	}
	requestIDHash := promptfilter.StableEvidenceFingerprint("adaptive-trust-model-review", ensurePromptPolicyRequestCorrelationID(c))
	h.db.RunBackgroundTask(func(ctx context.Context) {
		if err := h.db.RecordPromptRiskTrustModelReview(ctx, policy.ID, policy.SubjectType, subjectKey, requestIDHash); err != nil {
			log.Printf("record adaptive prompt trust model review failed policy=%d subject=%s: %v", policy.ID, subjectKey, err)
		}
	})
}

func (h *Handler) suspendPromptRiskTrustPolicy(policy database.PromptRiskTrustPolicy, subjectKey, reason string) {
	if h == nil || h.store == nil || subjectKey == "" {
		return
	}
	h.store.RemovePromptRiskTrustPolicy(subjectKey)
	if h.db == nil || policy.ID <= 0 {
		return
	}
	score := policy.LastRiskScore
	if score < policy.RiskThreshold {
		score = policy.RiskThreshold
	}
	h.db.RunBackgroundTask(func(ctx context.Context) {
		if _, err := h.db.SuspendPromptRiskTrustPolicy(ctx, policy.SubjectType, subjectKey, reason, score, database.PromptRiskLevelElevated); err != nil {
			log.Printf("suspend adaptive prompt trust failed policy=%d subject=%s: %v", policy.ID, subjectKey, err)
		}
	})
}
