package database

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// API Key 分组/账号维度限额的 scope 类型。
const (
	APIKeyScopeTypeGroup   = "group"
	APIKeyScopeTypeAccount = "account"
)

// scope 额度耗尽后的行为。
//   - skip:   把该 scope 的账号从本次请求的候选池剔除,自动落到其它分组/账号;
//     候选被剔空时返回 429 并说明是 scope 预算耗尽(而不是 503 无可用账号)。
//   - reject: 只要该 scope 超额,这个 Key 的请求一律 429,不换号。
const (
	APIKeyScopeOnExhaustedSkip   = "skip"
	APIKeyScopeOnExhaustedReject = "reject"
)

// APIKeyScopeWindow 是 scope 限额支持的滑动窗口。label 同时用于缓存键与错误文案。
type APIKeyScopeWindow struct {
	Label  string
	Window time.Duration
}

// APIKeyScopeWindowTotal 是累计额度在错误/展示里使用的伪窗口标签。
const APIKeyScopeWindowTotal = "total"

// APIKeyScopeWindows 是全部候选窗口,顺序即校验顺序(短窗口优先命中,文案更贴近现场)。
var APIKeyScopeWindows = []APIKeyScopeWindow{
	{Label: "5h", Window: 5 * time.Hour},
	{Label: "1d", Window: 24 * time.Hour},
	{Label: "7d", Window: 7 * 24 * time.Hour},
	{Label: "30d", Window: 30 * 24 * time.Hour},
}

// APIKeyScopeLimit 是一个 API Key 针对某个账号分组 / 单账号的用量上限(issue #439)。
//
// 语义:限额只约束「这个 Key 打到这个 scope 的用量」,不影响该 scope 被其它 Key 使用,
// 也不影响这个 Key 打到其它 scope。0 值表示该维度不限。
//
// 计量口径统一取 user_billed(与 Key 级 cost 限额一致);未配下游定价的模型 cost 恒为 0,
// 这类场景请改用 token / 请求数维度。
type APIKeyScopeLimit struct {
	ScopeType   string `json:"scope_type"`
	ScopeID     int64  `json:"scope_id"`
	OnExhausted string `json:"on_exhausted,omitempty"`

	Cost5h  float64 `json:"cost_5h,omitempty"`
	Cost1d  float64 `json:"cost_1d,omitempty"`
	Cost7d  float64 `json:"cost_7d,omitempty"`
	Cost30d float64 `json:"cost_30d,omitempty"`

	Token5h  int64 `json:"token_5h,omitempty"`
	Token1d  int64 `json:"token_1d,omitempty"`
	Token7d  int64 `json:"token_7d,omitempty"`
	Token30d int64 `json:"token_30d,omitempty"`

	Requests1d int64 `json:"requests_1d,omitempty"`

	// MaxConcurrency 是该 Key 在这条 scope 上允许的最大在途请求数(进程内软上限)。
	// 与 Key 级 max_concurrency 不同:它只约束打到这条 scope 的请求,位满时按 OnExhausted
	// 换号或拒绝。
	MaxConcurrency int `json:"max_concurrency,omitempty"`

	// 累计额度(不随时间衰减,用完须手动重置;语义对齐 api_keys.quota_limit)。
	// 已用量存在 api_key_scope_counters 表里,不放在这份配置里。
	QuotaCost     float64 `json:"quota_cost,omitempty"`
	QuotaTokens   int64   `json:"quota_tokens,omitempty"`
	QuotaRequests int64   `json:"quota_requests,omitempty"`
}

// HasCumulativeQuota 判断该条 scope 是否配了累计额度。
func (s APIKeyScopeLimit) HasCumulativeQuota() bool {
	return s.QuotaCost > 0 || s.QuotaTokens > 0 || s.QuotaRequests > 0
}

// CheckCumulative 用累计计数器判定该条 scope 的累计额度是否用尽。
func (s APIKeyScopeLimit) CheckCumulative(counter APIKeyScopeCounter) (APIKeyScopeExhaustion, bool) {
	if s.QuotaCost > 0 && counter.UsedCost >= s.QuotaCost {
		return APIKeyScopeExhaustion{Window: APIKeyScopeWindowTotal, Metric: "cost", Used: counter.UsedCost, Limit: s.QuotaCost}, true
	}
	if s.QuotaTokens > 0 && counter.UsedTokens >= s.QuotaTokens {
		return APIKeyScopeExhaustion{Window: APIKeyScopeWindowTotal, Metric: "tokens", Used: float64(counter.UsedTokens), Limit: float64(s.QuotaTokens)}, true
	}
	if s.QuotaRequests > 0 && counter.UsedRequests >= s.QuotaRequests {
		return APIKeyScopeExhaustion{Window: APIKeyScopeWindowTotal, Metric: "requests", Used: float64(counter.UsedRequests), Limit: float64(s.QuotaRequests)}, true
	}
	return APIKeyScopeExhaustion{}, false
}

// ResolveScopeType 归一 scope 类型;未知值返回 ""(调用方应丢弃该条)。
func (s APIKeyScopeLimit) ResolveScopeType() string {
	switch strings.ToLower(strings.TrimSpace(s.ScopeType)) {
	case APIKeyScopeTypeGroup:
		return APIKeyScopeTypeGroup
	case APIKeyScopeTypeAccount:
		return APIKeyScopeTypeAccount
	}
	return ""
}

// ResolveOnExhausted 归一耗尽行为;未知值一律按 skip(更保守:仍可服务)。
func (s APIKeyScopeLimit) ResolveOnExhausted() string {
	if strings.ToLower(strings.TrimSpace(s.OnExhausted)) == APIKeyScopeOnExhaustedReject {
		return APIKeyScopeOnExhaustedReject
	}
	return APIKeyScopeOnExhaustedSkip
}

// HasAnyLimit 判断该条 scope 是否配了至少一个有效上限。全 0 的条目没有约束力,
// 保存时应被丢弃,否则会白白拖起一次窗口聚合查询。
func (s APIKeyScopeLimit) HasAnyLimit() bool {
	return s.Cost5h > 0 || s.Cost1d > 0 || s.Cost7d > 0 || s.Cost30d > 0 ||
		s.Token5h > 0 || s.Token1d > 0 || s.Token7d > 0 || s.Token30d > 0 ||
		s.Requests1d > 0 || s.MaxConcurrency > 0 || s.HasCumulativeQuota()
}

// NeedsWindow 判断该条 scope 是否用到某个窗口,用于按需触发窗口聚合查询。
func (s APIKeyScopeLimit) NeedsWindow(label string) bool {
	switch label {
	case "5h":
		return s.Cost5h > 0 || s.Token5h > 0
	case "1d":
		return s.Cost1d > 0 || s.Token1d > 0 || s.Requests1d > 0
	case "7d":
		return s.Cost7d > 0 || s.Token7d > 0
	case "30d":
		return s.Cost30d > 0 || s.Token30d > 0
	}
	return false
}

// APIKeyScopeExhaustion 描述一条 scope 限额的触发结果。
type APIKeyScopeExhaustion struct {
	Window string
	Metric string
	Used   float64
	Limit  float64
}

// Describe 返回给下游客户端看的超额说明。金额与 token 用不同的格式,和 Key 级
// 限额的文案风格保持一致。
func (e APIKeyScopeExhaustion) Describe(scopeLabel string) string {
	// 累计额度没有"最近 N"的说法，单独措辞，否则会读成 "in last total"。
	scopeWindow := "in last " + e.Window
	if e.Window == APIKeyScopeWindowTotal {
		scopeWindow = "in total (needs a manual reset)"
	}
	if e.Metric == "cost" {
		return fmt.Sprintf("API key scope budget exhausted: %s used $%s / $%s %s",
			scopeLabel, formatScopeCost(e.Used), formatScopeCost(e.Limit), scopeWindow)
	}
	return fmt.Sprintf("API key scope budget exhausted: %s used %d / %d %s %s",
		scopeLabel, int64(e.Used), int64(e.Limit), e.Metric, scopeWindow)
}

// formatScopeCost 格式化金额:常规预算按分保留两位,极小额度(如按次计费的实验值)
// 多留几位,避免文案显示成 "$0.00 / $0.00" 让人无从判断。
func formatScopeCost(value float64) string {
	if value > 0 && value < 0.01 {
		return strconv.FormatFloat(value, 'f', 6, 64)
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

// CheckWindow 用某窗口的实际用量判定该条 scope 是否已耗尽。命中返回 (结果, true)。
// 判定用 >= ,与 Key 级限额一致:达到上限即视为用尽。
func (s APIKeyScopeLimit) CheckWindow(label string, usage APIKeyWindowUsage) (APIKeyScopeExhaustion, bool) {
	var costLimit float64
	var tokenLimit, requestLimit int64
	switch label {
	case "5h":
		costLimit, tokenLimit = s.Cost5h, s.Token5h
	case "1d":
		costLimit, tokenLimit, requestLimit = s.Cost1d, s.Token1d, s.Requests1d
	case "7d":
		costLimit, tokenLimit = s.Cost7d, s.Token7d
	case "30d":
		costLimit, tokenLimit = s.Cost30d, s.Token30d
	default:
		return APIKeyScopeExhaustion{}, false
	}
	if costLimit > 0 && usage.UserBilled >= costLimit {
		return APIKeyScopeExhaustion{Window: label, Metric: "cost", Used: usage.UserBilled, Limit: costLimit}, true
	}
	if tokenLimit > 0 && usage.Tokens >= tokenLimit {
		return APIKeyScopeExhaustion{Window: label, Metric: "tokens", Used: float64(usage.Tokens), Limit: float64(tokenLimit)}, true
	}
	if requestLimit > 0 && usage.Requests >= requestLimit {
		return APIKeyScopeExhaustion{Window: label, Metric: "requests", Used: float64(usage.Requests), Limit: float64(requestLimit)}, true
	}
	return APIKeyScopeExhaustion{}, false
}

// NormalizeAPIKeyScopeLimits 归一 scope 限额列表:丢弃未知类型 / 非法 ID / 全 0 的条目,
// 同 (type, id) 去重(后者覆盖前者),负值置 0。返回 nil 表示没有任何有效 scope 限额。
func NormalizeAPIKeyScopeLimits(in []APIKeyScopeLimit) []APIKeyScopeLimit {
	if len(in) == 0 {
		return nil
	}
	type scopeKey struct {
		scopeType string
		scopeID   int64
	}
	index := make(map[scopeKey]int, len(in))
	out := make([]APIKeyScopeLimit, 0, len(in))
	for _, item := range in {
		scopeType := item.ResolveScopeType()
		if scopeType == "" || item.ScopeID <= 0 {
			continue
		}
		clean := APIKeyScopeLimit{
			ScopeType:   scopeType,
			ScopeID:     item.ScopeID,
			OnExhausted: item.ResolveOnExhausted(),
			Cost5h:      maxScopeFloat(item.Cost5h),
			Cost1d:      maxScopeFloat(item.Cost1d),
			Cost7d:      maxScopeFloat(item.Cost7d),
			Cost30d:     maxScopeFloat(item.Cost30d),
			Token5h:     maxScopeInt64(item.Token5h),
			Token1d:     maxScopeInt64(item.Token1d),
			Token7d:     maxScopeInt64(item.Token7d),
			Token30d:    maxScopeInt64(item.Token30d),
			Requests1d:  maxScopeInt64(item.Requests1d),

			MaxConcurrency: int(maxScopeInt64(int64(item.MaxConcurrency))),

			QuotaCost:     maxScopeFloat(item.QuotaCost),
			QuotaTokens:   maxScopeInt64(item.QuotaTokens),
			QuotaRequests: maxScopeInt64(item.QuotaRequests),
		}
		if !clean.HasAnyLimit() {
			continue
		}
		key := scopeKey{scopeType: scopeType, scopeID: item.ScopeID}
		if pos, ok := index[key]; ok {
			out[pos] = clean
			continue
		}
		index[key] = len(out)
		out = append(out, clean)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// PruneAPIKeyScopeLimitsForScope 删除指向某个已删除分组 / 账号的 scope 限额条目。
// 返回 (剩余条目, 是否有改动)。
func PruneAPIKeyScopeLimitsForScope(in []APIKeyScopeLimit, scopeType string, scopeID int64) ([]APIKeyScopeLimit, bool) {
	if len(in) == 0 || scopeID <= 0 {
		return in, false
	}
	out := make([]APIKeyScopeLimit, 0, len(in))
	changed := false
	for _, item := range in {
		if item.ResolveScopeType() == scopeType && item.ScopeID == scopeID {
			changed = true
			continue
		}
		out = append(out, item)
	}
	if !changed {
		return in, false
	}
	if len(out) == 0 {
		return nil, true
	}
	return out, true
}

func maxScopeFloat(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

func maxScopeInt64(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
