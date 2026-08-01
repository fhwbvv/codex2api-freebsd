package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/codex2api/database"
	"github.com/codex2api/security"
	"github.com/gin-gonic/gin"
)

const (
	adminAPIKeyLimitsCacheNamespace = "api-key-limits"
	apiKeyResetWindow5h             = "5h"
	apiKeyResetWindow7d             = "7d"
)

// ResetAPIKeyQuota manually renews one API key's cumulative and 5h/7d quota
// periods while preserving historical usage logs and total usage.
//
// POST /api/admin/keys/:id/reset-quota
//
// The reset timestamp is used as an aggregation cutoff for 5h/7d cost and
// token limits, so neither the admin UI nor the gateway counts older events.
// Runtime caches are evicted before success is returned.
func (h *Handler) ResetAPIKeyQuota(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(c, http.StatusBadRequest, "无效的 API Key ID")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	target, err := h.db.ResetAPIKeyQuota(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(c, http.StatusNotFound, "API Key 不存在")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}

	h.invalidateResetAPIKeyCaches(ctx, *target)
	security.SecurityAuditLog("API_KEY_QUOTA_RESET", fmt.Sprintf("id=%d ip=%s", id, c.ClientIP()))
	c.JSON(http.StatusOK, gin.H{"message": "API Key 累计额度及 5h/7d 用量已重置"})
}

// ResetAllAPIKeyQuotas renews cumulative and 5h/7d quota periods for all API
// keys in one statement, preserving historical usage logs.
//
// POST /api/admin/keys/reset-all-quotas
func (h *Handler) ResetAllAPIKeyQuotas(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	targets, err := h.db.ResetAllAPIKeyQuotas(ctx)
	if err != nil {
		writeInternalError(c, err)
		return
	}

	h.deleteRuntimeCache(ctx, adminAPIKeyCountNamespace, "all")
	for _, target := range targets {
		h.invalidateResetAPIKeyCaches(ctx, target)
	}
	security.SecurityAuditLog("API_KEY_QUOTA_RESET_ALL", fmt.Sprintf("count=%d ip=%s", len(targets), c.ClientIP()))
	c.JSON(http.StatusOK, gin.H{
		"message":     "所有 API Key 累计额度及 5h/7d 用量已重置",
		"reset_count": len(targets),
	})
}

func (h *Handler) invalidateResetAPIKeyCaches(ctx context.Context, target database.APIKeyQuotaResetTarget) {
	h.deleteRuntimeCache(ctx, adminAPIKeyCacheNamespace, target.Key)
	for _, label := range []string{apiKeyResetWindow5h, apiKeyResetWindow7d} {
		h.deleteRuntimeCache(ctx, adminAPIKeyLimitsCacheNamespace, fmt.Sprintf("%d:usage:%s", target.ID, label))
	}
}
