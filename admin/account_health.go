package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/codex2api/database"
	"github.com/gin-gonic/gin"
)

const (
	// accountHealthBlockCount / accountHealthBlockMinutes 决定「健康状态」条的粒度：
	// 20 个格子 × 10 分钟 ≈ 覆盖最近 3.3 小时。需与前端 AccountHealthBar 保持一致。
	accountHealthBlockCount   = 20
	accountHealthBlockMinutes = 10
)

// GetAccountHealthBars 返回各账号最近一段时间的请求成败分桶，用于账号管理页的
// 「健康状态」条。GET /api/accounts/health-bars
//
// 返回结构：{ buckets: { "<accountId>": [{success,failed}, ...20], ... },
//            block_count: 20, block_minutes: 10 }
func (h *Handler) GetAccountHealthBars(c *gin.Context) {
	ctx := c.Request.Context()

	buckets, err := h.db.GetAccountsHealthBuckets(
		ctx,
		time.Now(),
		accountHealthBlockCount,
		accountHealthBlockMinutes*time.Minute,
	)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "获取健康状态失败："+err.Error())
		return
	}

	out := make(map[string][]database.AccountHealthBucket, len(buckets))
	for id, b := range buckets {
		out[strconv.FormatInt(id, 10)] = b
	}

	c.JSON(http.StatusOK, gin.H{
		"buckets":       out,
		"block_count":   accountHealthBlockCount,
		"block_minutes": accountHealthBlockMinutes,
	})
}
