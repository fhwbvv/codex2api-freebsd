package admin

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/codex2api/database"
	"github.com/codex2api/proxy"
	"github.com/gin-gonic/gin"
)

// apiKeyScopeUsageItem 是某个 API Key 在一条 scope 限额上的当前用量（issue #439）。
// 只返回该条 scope 真正配了上限的窗口，前端据此渲染进度条。
type apiKeyScopeUsageItem struct {
	ScopeType   string                       `json:"scope_type"`
	ScopeID     int64                        `json:"scope_id"`
	ScopeName   string                       `json:"scope_name"`
	ScopeExists bool                         `json:"scope_exists"`
	OnExhausted string                       `json:"on_exhausted"`
	Windows     []apiKeyScopeUsageWindowItem `json:"windows"`
	// Cumulative 是累计额度（不随时间回落，需手动重置）的当前状态。
	Cumulative *apiKeyScopeCumulativeItem `json:"cumulative,omitempty"`
	// Skips 是这条预算被判定耗尽的运行态统计(进程内，重启清零)。预算耗尽后请求会静默
	// 落到其它账号，这个计数是唯一能直观回答「这条预算真的在生效吗」的信号。
	Skips *apiKeyScopeSkipItem `json:"skips,omitempty"`
}

type apiKeyScopeCumulativeItem struct {
	UsedCost      float64 `json:"used_cost"`
	UsedTokens    int64   `json:"used_tokens"`
	UsedRequests  int64   `json:"used_requests"`
	QuotaCost     float64 `json:"quota_cost,omitempty"`
	QuotaTokens   int64   `json:"quota_tokens,omitempty"`
	QuotaRequests int64   `json:"quota_requests,omitempty"`
	ResetCount    int     `json:"reset_count"`
	LastResetAt   string  `json:"last_reset_at,omitempty"`
	Exhausted     bool    `json:"exhausted"`
}

type apiKeyScopeSkipItem struct {
	Requests    int64  `json:"requests"`
	FirstAt     string `json:"first_at"`
	LastAt      string `json:"last_at"`
	LastMessage string `json:"last_message"`
}

type apiKeyScopeUsageWindowItem struct {
	Window       string  `json:"window"`
	Requests     int64   `json:"requests"`
	Tokens       int64   `json:"tokens"`
	UserBilled   float64 `json:"user_billed"`
	CostLimit    float64 `json:"cost_limit,omitempty"`
	TokenLimit   int64   `json:"token_limit,omitempty"`
	RequestLimit int64   `json:"request_limit,omitempty"`
	Exhausted    bool    `json:"exhausted"`
}

// GetAPIKeyScopeUsage 返回某 API Key 各条分组/账号维度限额的当前用量。
// GET /api/admin/keys/:id/scope-usage
//
// 口径与网关判定完全一致（同一套按账号拆分的窗口聚合 + 当前分组成员关系折算），
// 唯一差别是这里不叠加网关进程内的增量事件，因此可能比实际用量少最多一个批量落库周期。
func (h *Handler) GetAPIKeyScopeUsage(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效的 API Key ID")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	row, err := h.db.GetAPIKeyByID(ctx, id)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if row == nil {
		writeError(c, http.StatusNotFound, "API Key 不存在")
		return
	}

	scopes := database.NormalizeAPIKeyScopeLimits(row.Limits.ScopeLimits)
	items := make([]apiKeyScopeUsageItem, 0, len(scopes))
	if len(scopes) == 0 {
		c.JSON(http.StatusOK, gin.H{"items": items})
		return
	}

	// 每个用到的窗口只查一次，全部 scope 共享同一份按账号拆分的聚合。
	usageByWindow := make(map[string]map[int64]database.APIKeyWindowUsage, len(database.APIKeyScopeWindows))
	for _, window := range database.APIKeyScopeWindows {
		needed := false
		for _, scope := range scopes {
			if scope.NeedsWindow(window.Label) {
				needed = true
				break
			}
		}
		if !needed {
			continue
		}
		usage, err := h.db.GetAPIKeyAccountWindowUsage(ctx, id, window.Window)
		if err != nil {
			writeInternalError(c, err)
			return
		}
		usageByWindow[window.Label] = usage
	}

	groupNames := h.accountGroupNameIndex(ctx)
	groupsByAccount := make(map[int64][]int64)
	for _, perAccount := range usageByWindow {
		h.collectAccountGroups(perAccount, groupsByAccount)
	}
	skips := proxy.APIKeyScopeSkipSnapshot(id)
	counters, err := h.db.ListAPIKeyScopeCounters(ctx, id)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	for _, scope := range scopes {
		item := apiKeyScopeUsageItem{
			ScopeType:   scope.ResolveScopeType(),
			ScopeID:     scope.ScopeID,
			OnExhausted: scope.ResolveOnExhausted(),
			Windows:     make([]apiKeyScopeUsageWindowItem, 0, len(database.APIKeyScopeWindows)),
		}
		if item.ScopeType == database.APIKeyScopeTypeAccount {
			// 账号展示名由前端用自己的账号列表解析，这里只判断账号是否还在池中。
			item.ScopeExists = h.store != nil && h.store.FindByID(scope.ScopeID) != nil
		} else if name, ok := groupNames[scope.ScopeID]; ok {
			item.ScopeName = name
			item.ScopeExists = true
		}

		for _, window := range database.APIKeyScopeWindows {
			if !scope.NeedsWindow(window.Label) {
				continue
			}
			perAccount, ok := usageByWindow[window.Label]
			if !ok {
				continue
			}
			usage := aggregateScopeUsage(scope, perAccount, groupsByAccount)
			_, exhausted := scope.CheckWindow(window.Label, usage)
			entry := apiKeyScopeUsageWindowItem{
				Window:     window.Label,
				Requests:   usage.Requests,
				Tokens:     usage.Tokens,
				UserBilled: usage.UserBilled,
				Exhausted:  exhausted,
			}
			switch window.Label {
			case "5h":
				entry.CostLimit, entry.TokenLimit = scope.Cost5h, scope.Token5h
			case "1d":
				entry.CostLimit, entry.TokenLimit, entry.RequestLimit = scope.Cost1d, scope.Token1d, scope.Requests1d
			case "7d":
				entry.CostLimit, entry.TokenLimit = scope.Cost7d, scope.Token7d
			case "30d":
				entry.CostLimit, entry.TokenLimit = scope.Cost30d, scope.Token30d
			}
			item.Windows = append(item.Windows, entry)
		}
		if scope.HasCumulativeQuota() {
			counter := counters[database.APIKeyScopeCounterKey{ScopeType: item.ScopeType, ScopeID: item.ScopeID}]
			_, exhausted := scope.CheckCumulative(counter)
			cumulative := &apiKeyScopeCumulativeItem{
				UsedCost:      counter.UsedCost,
				UsedTokens:    counter.UsedTokens,
				UsedRequests:  counter.UsedRequests,
				QuotaCost:     scope.QuotaCost,
				QuotaTokens:   scope.QuotaTokens,
				QuotaRequests: scope.QuotaRequests,
				ResetCount:    counter.ResetCount,
				Exhausted:     exhausted,
			}
			if counter.LastResetAt.Valid {
				cumulative.LastResetAt = counter.LastResetAt.Time.Format(time.RFC3339)
			}
			item.Cumulative = cumulative
		}
		if stat, ok := skips[fmt.Sprintf("%s:%d", item.ScopeType, item.ScopeID)]; ok && stat.Requests > 0 {
			item.Skips = &apiKeyScopeSkipItem{
				Requests:    stat.Requests,
				FirstAt:     stat.FirstAt.Format(time.RFC3339),
				LastAt:      stat.LastAt.Format(time.RFC3339),
				LastMessage: stat.LastMessage,
			}
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{"items": items})
}

type resetAPIKeyScopeQuotaReq struct {
	ScopeType string `json:"scope_type"`
	ScopeID   int64  `json:"scope_id"`
}

// ResetAPIKeyScopeQuota 把某条 scope 的累计额度清零并记一次重置。
// POST /api/admin/keys/:id/scope-quota/reset
//
// 累计额度不随时间回落（这正是它与滑动窗口的区别），所以必须有这个显式入口，
// 否则用完就永久卡死。语义对齐 Key 级额度的 reset_quota。
func (h *Handler) ResetAPIKeyScopeQuota(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		writeError(c, http.StatusBadRequest, "无效的 API Key ID")
		return
	}
	var req resetAPIKeyScopeQuotaReq
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.ScopeID <= 0 {
		writeError(c, http.StatusBadRequest, "scope_id 必须为正整数")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	row, err := h.db.GetAPIKeyByID(ctx, id)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if row == nil {
		writeError(c, http.StatusNotFound, "API Key 不存在")
		return
	}
	if err := h.db.ResetAPIKeyScopeCounter(ctx, id, req.ScopeType, req.ScopeID); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	// 重置后立刻重新观察命中统计，否则历史计数会让人误以为预算还在被拦。
	proxy.ResetAPIKeyScopeSkipStats(id)
	writeMessage(c, http.StatusOK, "累计额度已重置")
}

// apiKeyScopeSummaryItem 是列表页用的 scope 预算概览:只给"最紧的那个窗口"的占比,
// 详情留给编辑抽屉。
type apiKeyScopeSummaryItem struct {
	ScopeType    string  `json:"scope_type"`
	ScopeID      int64   `json:"scope_id"`
	ScopeName    string  `json:"scope_name"`
	OnExhausted  string  `json:"on_exhausted"`
	Window       string  `json:"window"`
	Metric       string  `json:"metric"`
	Ratio        float64 `json:"ratio"`
	Exhausted    bool    `json:"exhausted"`
	SkipRequests int64   `json:"skip_requests,omitempty"`
}

// GetAPIKeysScopeSummary 一次返回全部（配了 scope 预算的）Key 的预算概览。
// GET /api/admin/keys-scope-summary
//
// 每个窗口只做一次跨 Key 的聚合查询，避免列表页按 Key 逐个查导致 N×窗口 的放大。
func (h *Handler) GetAPIKeysScopeSummary(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	keys, err := h.db.ListAPIKeys(ctx)
	if err != nil {
		writeInternalError(c, err)
		return
	}

	scopesByKey := make(map[int64][]database.APIKeyScopeLimit)
	keyIDs := make([]int64, 0, len(keys))
	for _, key := range keys {
		scopes := database.NormalizeAPIKeyScopeLimits(key.Limits.ScopeLimits)
		if len(scopes) == 0 {
			continue
		}
		scopesByKey[key.ID] = scopes
		keyIDs = append(keyIDs, key.ID)
	}
	summary := make(map[string][]apiKeyScopeSummaryItem)
	if len(keyIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"summary": summary})
		return
	}

	// 只查真的被用到的窗口。
	usageByWindow := make(map[string]map[int64]map[int64]database.APIKeyWindowUsage)
	for _, window := range database.APIKeyScopeWindows {
		needed := false
		for _, scopes := range scopesByKey {
			for _, scope := range scopes {
				if scope.NeedsWindow(window.Label) {
					needed = true
					break
				}
			}
			if needed {
				break
			}
		}
		if !needed {
			continue
		}
		usage, err := h.db.GetAPIKeysAccountWindowUsage(ctx, keyIDs, window.Window)
		if err != nil {
			writeInternalError(c, err)
			return
		}
		usageByWindow[window.Label] = usage
	}

	groupNames := h.accountGroupNameIndex(ctx)
	groupsByAccount := make(map[int64][]int64)
	for _, perKey := range usageByWindow {
		for _, perAccount := range perKey {
			h.collectAccountGroups(perAccount, groupsByAccount)
		}
	}
	countersByKey, err := h.db.ListAPIKeyScopeCountersForKeys(ctx, keyIDs)
	if err != nil {
		writeInternalError(c, err)
		return
	}

	for keyID, scopes := range scopesByKey {
		skips := proxy.APIKeyScopeSkipSnapshot(keyID)
		items := make([]apiKeyScopeSummaryItem, 0, len(scopes))
		for _, scope := range scopes {
			item := apiKeyScopeSummaryItem{
				ScopeType:   scope.ResolveScopeType(),
				ScopeID:     scope.ScopeID,
				OnExhausted: scope.ResolveOnExhausted(),
			}
			if item.ScopeType == database.APIKeyScopeTypeGroup {
				item.ScopeName = groupNames[scope.ScopeID]
			}
			if stat, ok := skips[fmt.Sprintf("%s:%d", item.ScopeType, item.ScopeID)]; ok {
				item.SkipRequests = stat.Requests
			}
			// 概览只保留「用得最满」的那个窗口。占比 >= 1 与 CheckWindow 的耗尽判定等价
			// （两者都是 used >= limit），所以不必再单独跑一遍判定。
			for _, window := range database.APIKeyScopeWindows {
				if !scope.NeedsWindow(window.Label) {
					continue
				}
				perKey, ok := usageByWindow[window.Label]
				if !ok {
					continue
				}
				usage := aggregateScopeUsage(scope, perKey[keyID], groupsByAccount)
				ratio, metric := scopeWindowRatio(scope, window.Label, usage)
				if item.Window != "" && ratio <= item.Ratio {
					continue
				}
				item.Ratio = ratio
				item.Window = window.Label
				item.Metric = metric
				item.Exhausted = ratio >= 1
			}
			// 累计额度也要参与「最紧」的比较，否则只配累计额度的 Key 在列表里
			// 会显示成 0%。
			if scope.HasCumulativeQuota() {
				counter := countersByKey[keyID][database.APIKeyScopeCounterKey{
					ScopeType: item.ScopeType,
					ScopeID:   item.ScopeID,
				}]
				ratio, metric := scopeCumulativeRatio(scope, counter)
				if item.Window == "" || ratio > item.Ratio {
					item.Ratio = ratio
					item.Window = database.APIKeyScopeWindowTotal
					item.Metric = metric
					item.Exhausted = ratio >= 1
				}
			}
			items = append(items, item)
		}
		summary[strconv.FormatInt(keyID, 10)] = items
	}

	c.JSON(http.StatusOK, gin.H{"summary": summary})
}

// scopeCumulativeRatio 返回累计额度里"用得最满"的口径占比与口径名。
func scopeCumulativeRatio(scope database.APIKeyScopeLimit, counter database.APIKeyScopeCounter) (float64, string) {
	best := 0.0
	metric := ""
	if scope.QuotaCost > 0 {
		best = counter.UsedCost / scope.QuotaCost
		metric = "cost"
	}
	if scope.QuotaTokens > 0 {
		if ratio := float64(counter.UsedTokens) / float64(scope.QuotaTokens); ratio > best {
			best = ratio
			metric = "tokens"
		}
	}
	if scope.QuotaRequests > 0 {
		if ratio := float64(counter.UsedRequests) / float64(scope.QuotaRequests); ratio > best {
			best = ratio
			metric = "requests"
		}
	}
	return best, metric
}

// scopeWindowRatio 返回某窗口内"用得最满"的口径占比（0~1+）与该口径名。
func scopeWindowRatio(scope database.APIKeyScopeLimit, label string, usage database.APIKeyWindowUsage) (float64, string) {
	var costLimit float64
	var tokenLimit, requestLimit int64
	switch label {
	case "5h":
		costLimit, tokenLimit = scope.Cost5h, scope.Token5h
	case "1d":
		costLimit, tokenLimit, requestLimit = scope.Cost1d, scope.Token1d, scope.Requests1d
	case "7d":
		costLimit, tokenLimit = scope.Cost7d, scope.Token7d
	case "30d":
		costLimit, tokenLimit = scope.Cost30d, scope.Token30d
	}
	best := 0.0
	metric := ""
	if costLimit > 0 {
		best = usage.UserBilled / costLimit
		metric = "cost"
	}
	if tokenLimit > 0 {
		if ratio := float64(usage.Tokens) / float64(tokenLimit); ratio > best {
			best = ratio
			metric = "tokens"
		}
	}
	if requestLimit > 0 {
		if ratio := float64(usage.Requests) / float64(requestLimit); ratio > best {
			best = ratio
			metric = "requests"
		}
	}
	return best, metric
}

// aggregateScopeUsage 把按账号拆分的用量折算到一条 scope 上。
func aggregateScopeUsage(
	scope database.APIKeyScopeLimit,
	perAccount map[int64]database.APIKeyWindowUsage,
	groupsByAccount map[int64][]int64,
) database.APIKeyWindowUsage {
	if scope.ResolveScopeType() == database.APIKeyScopeTypeAccount {
		return perAccount[scope.ScopeID]
	}
	var usage database.APIKeyWindowUsage
	for accountID, accountUsage := range perAccount {
		matched := false
		for _, groupID := range groupsByAccount[accountID] {
			if groupID == scope.ScopeID {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		usage.Requests += accountUsage.Requests
		usage.Tokens += accountUsage.Tokens
		usage.UserBilled += accountUsage.UserBilled
	}
	return usage
}

// collectAccountGroups 把聚合结果里出现的账号当前所属分组累积到 out。已从账号池
// 移除的账号解析不到分组，其历史用量在分组维度上被忽略。
func (h *Handler) collectAccountGroups(perAccount map[int64]database.APIKeyWindowUsage, out map[int64][]int64) {
	if h == nil || h.store == nil || out == nil {
		return
	}
	for accountID := range perAccount {
		if _, ok := out[accountID]; ok {
			continue
		}
		account := h.store.FindByID(accountID)
		if account == nil {
			out[accountID] = nil
			continue
		}
		out[accountID] = account.GroupIDSnapshot()
	}
}

// accountGroupNameIndex 返回分组 ID → 名称。查询失败时返回空表（前端退回显示 ID）。
func (h *Handler) accountGroupNameIndex(ctx context.Context) map[int64]string {
	groups, err := h.db.ListAccountGroups(ctx)
	if err != nil {
		return map[int64]string{}
	}
	out := make(map[int64]string, len(groups))
	for _, group := range groups {
		out[group.ID] = group.Name
	}
	return out
}
