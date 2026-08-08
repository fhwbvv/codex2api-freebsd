package admin

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/codex2api/database"
	"github.com/gin-gonic/gin"
)

const promptRiskTrustSyncInterval = 30 * time.Second
const promptRiskTrustAdaptiveReconcileInterval = 5 * time.Minute

type promptRiskTrustPolicyRequest struct {
	DurationHours int    `json:"duration_hours"`
	RiskThreshold int    `json:"risk_threshold"`
	Reason        string `json:"reason"`
}

func (h *Handler) startPromptRiskTrustSync(ctx context.Context) {
	if h == nil || h.db == nil || h.store == nil {
		return
	}
	h.startDBBackgroundTaskWithParent(ctx, func(ctx context.Context) {
		ticker := time.NewTicker(promptRiskTrustSyncInterval)
		defer ticker.Stop()
		lastError := ""
		lastAdaptiveReconcile := time.Time{}
		for {
			promptFilterConfig := h.store.GetPromptFilterConfig()
			advanced := promptFilterConfig.Advanced.AdaptiveReview
			now := time.Now().UTC()
			var policies []*database.PromptRiskTrustPolicy
			var err error
			if promptFilterConfig.Review.Enabled && advanced.Enabled && (lastAdaptiveReconcile.IsZero() || now.Sub(lastAdaptiveReconcile) >= promptRiskTrustAdaptiveReconcileInterval) {
				policies, err = h.db.ReconcileAdaptivePromptRiskTrustPolicies(ctx, database.PromptRiskTrustAdaptiveOptions{
					MinCleanReviews: advanced.MinCleanReviews, MinObservationHours: advanced.MinObservationHours,
					TrustDurationHours: advanced.TrustDurationHours, ReactivationCleanReviews: advanced.ReactivationCleanReviews,
					ReactivationCooldownHours: advanced.ReactivationCooldownHours, RiskThreshold: 35,
				})
				if err == nil {
					lastAdaptiveReconcile = now
				}
			} else {
				policies, err = h.db.ReconcilePromptRiskTrustPolicies(ctx)
			}
			if err != nil {
				if message := err.Error(); message != lastError {
					log.Printf("prompt risk adaptive trust sync failed: %v", err)
					lastError = message
				}
			} else {
				h.store.ReplacePromptRiskTrustPolicies(policies)
				lastError = ""
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

func (h *Handler) refreshPromptRiskTrustSnapshot(ctx context.Context) error {
	policies, err := h.db.ReconcilePromptRiskTrustPolicies(ctx)
	if err != nil {
		return err
	}
	h.store.ReplacePromptRiskTrustPolicies(policies)
	return nil
}

func (h *Handler) UpsertPromptRiskTrustPolicy(c *gin.Context) {
	subjectType := strings.TrimSpace(c.Param("subject_type"))
	subjectKey := strings.TrimSpace(c.Param("subject_key"))
	if subjectType != database.PromptRiskSubjectNewAPIUser || subjectKey == "" {
		writeError(c, http.StatusBadRequest, "仅已签名的人员画像可启用自适应可信策略")
		return
	}
	var req promptRiskTrustPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		writeError(c, http.StatusBadRequest, "请填写启用自适应可信策略的原因")
		return
	}
	if req.DurationHours <= 0 {
		req.DurationHours = 24
	}
	if req.DurationHours > 30*24 {
		writeError(c, http.StatusBadRequest, "自适应可信期限不能超过 30 天")
		return
	}
	if req.RiskThreshold <= 0 {
		req.RiskThreshold = 35
	}
	if req.RiskThreshold > 100 {
		writeError(c, http.StatusBadRequest, "恢复审核阈值不能超过 100")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	profile, err := h.db.GetPromptRiskProfile(ctx, subjectType, subjectKey)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(c, http.StatusNotFound, "人员画像不存在")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if !profile.IsPerson || profile.NewAPIUserID == "" {
		writeError(c, http.StatusBadRequest, "该画像不是可验证的人员身份")
		return
	}
	if profile.RiskScore >= req.RiskThreshold || profile.RiskLevel == database.PromptRiskLevelHigh || profile.RiskLevel == database.PromptRiskLevelCritical {
		writeError(c, http.StatusConflict, "当前画像风险已达到重新审核阈值，不能启用模型复核豁免")
		return
	}
	policy, err := h.db.UpsertPromptRiskTrustPolicy(ctx, database.PromptRiskTrustPolicyInput{
		SubjectType: subjectType, SubjectKey: subjectKey, Reason: req.Reason,
		RiskThreshold: req.RiskThreshold, ValidUntil: time.Now().UTC().Add(time.Duration(req.DurationHours) * time.Hour),
	})
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.refreshPromptRiskTrustSnapshot(ctx); err != nil {
		writeInternalError(c, err)
		return
	}
	policy, err = h.db.GetPromptRiskTrustPolicy(ctx, subjectType, subjectKey)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"policy": policy})
}

func (h *Handler) RevokePromptRiskTrustPolicy(c *gin.Context) {
	subjectType := strings.TrimSpace(c.Param("subject_type"))
	subjectKey := strings.TrimSpace(c.Param("subject_key"))
	if subjectType != database.PromptRiskSubjectNewAPIUser || subjectKey == "" {
		writeError(c, http.StatusBadRequest, "自适应可信标识无效")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	policy, err := h.db.RevokePromptRiskTrustPolicy(ctx, subjectType, subjectKey, "管理员撤销自适应可信策略")
	if errors.Is(err, sql.ErrNoRows) {
		writeError(c, http.StatusNotFound, "自适应可信策略不存在")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if err := h.refreshPromptRiskTrustSnapshot(ctx); err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"policy": policy})
}
