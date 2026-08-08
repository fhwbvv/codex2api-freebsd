package admin

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type accountPageStatsItem struct {
	Usage5hDetail *accountUsageWindow `json:"usage_5h_detail,omitempty"`
	Usage7dDetail *accountUsageWindow `json:"usage_7d_detail,omitempty"`
	Billed5h      *float64            `json:"billed_5h,omitempty"`
	Billed7d      *float64            `json:"billed_7d,omitempty"`
}

// GetAccountPageStats loads log-derived usage and billing values after the
// visible account page has rendered. The request is strictly bounded to one
// page, avoiding any pool-wide aggregation on the first-screen path.
func (h *Handler) GetAccountPageStats(c *gin.Context) {
	ids, err := parseAccountListIDs(c.Query("ids"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "ids 参数无效")
		return
	}
	if len(ids) == 0 {
		c.JSON(http.StatusOK, gin.H{"stats": map[int64]accountPageStatsItem{}})
		return
	}
	if len(ids) > accountListPageMax {
		writeError(c, http.StatusBadRequest, "ids 最多允许 500 个")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	now := time.Now()
	usage5h, usage7d, err := h.db.GetAccountUsageWindowsByIDs(ctx, ids, now.Add(-5*time.Hour), now.AddDate(0, 0, -7))
	if err != nil {
		writeInternalError(c, err)
		return
	}

	billing5hWindows, billing7dWindows := h.accountBillingWindows(ids)
	billed5h, err := h.db.GetAccountsBilledSince(ctx, billing5hWindows)
	if err != nil {
		log.Printf("获取当前页账号 5h 成本失败: %v", err)
		billed5h = map[int64]float64{}
	}
	billed7d, err := h.db.GetAccountsBilledSince(ctx, billing7dWindows)
	if err != nil {
		log.Printf("获取当前页账号长窗口成本失败: %v", err)
		billed7d = map[int64]float64{}
	}

	stats := make(map[int64]accountPageStatsItem, len(ids))
	for _, id := range ids {
		item := accountPageStatsItem{}
		if value := usage5h[id]; value != nil {
			item.Usage5hDetail = &accountUsageWindow{
				Requests: value.Requests, Tokens: value.Tokens,
				AccountBilled: value.AccountBilled, UserBilled: value.UserBilled,
			}
		}
		if value := usage7d[id]; value != nil {
			item.Usage7dDetail = &accountUsageWindow{
				Requests: value.Requests, Tokens: value.Tokens,
				AccountBilled: value.AccountBilled, UserBilled: value.UserBilled,
			}
		}
		if value, ok := billed5h[id]; ok {
			item.Billed5h = &value
		}
		if value, ok := billed7d[id]; ok {
			item.Billed7d = &value
		}
		stats[id] = item
	}
	c.JSON(http.StatusOK, gin.H{"stats": stats})
}

func (h *Handler) accountBillingWindows(ids []int64) (map[int64]time.Time, map[int64]time.Time) {
	shortWindows := make(map[int64]time.Time, len(ids))
	longWindows := make(map[int64]time.Time, len(ids))
	if h.store == nil {
		return shortWindows, longWindows
	}
	for _, id := range ids {
		account := h.store.FindByID(id)
		if account == nil {
			continue
		}
		if resetAt := account.GetReset5hAt(); !resetAt.IsZero() {
			shortWindows[id] = resetAt.Add(-5 * time.Hour)
		}
		if resetAt := account.GetReset7dAt(); !resetAt.IsZero() {
			window := 7 * 24 * time.Hour
			if seconds := account.GetWindow7dSeconds(); seconds > 0 {
				window = time.Duration(seconds) * time.Second
			}
			longWindows[id] = resetAt.Add(-window)
		}
	}
	return shortWindows, longWindows
}
