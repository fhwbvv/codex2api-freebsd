package proxy

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/codex2api/database"
)

// Codex 过载熔断：上游返回 server_is_overloaded（service_unavailable_error，
// "Our servers are currently overloaded"）时，单个账号在滑动窗口内该错误占比
// 达到阈值就自动暂停调度一段时间，避免调度器持续把流量砸向已经过载的账号。
//
// 统计钩子挂在用量日志公共汇点（logUsage）：所有 attempt 无论成败都会经过那里，
// 天然拿到 账号ID/渠道/状态码/错误摘要。只统计 Codex 渠道；触发后走既有账号冷却
// 机制（reason=overload_paused），调度器跳过、账号页显示原因与恢复倒计时、到期
// 自动恢复，均复用现成链路。计数为实例内存级，多实例部署各自独立熔断。

// overloadPauseReason 是过载熔断使用的冷却原因，进限流状态口径（accounts_paged）。
const overloadPauseReason = "overload_paused"

// overloadMinSamples 触发判定的窗口内最小请求数：样本太少时比例没有意义，
// 避免低流量账号被单次报错误伤。
const overloadMinSamples = 5

// overloadErrorCode 按错误 code 识别过载，不匹配人类可读文案。
const overloadErrorCode = "server_is_overloaded"

type overloadBucket struct {
	total      int
	overloaded int
}

type overloadBreaker struct {
	mu sync.Mutex
	// windows[accountID][unixMinute] -> 计数桶；record 时顺手剔除窗口外的旧桶。
	windows map[int64]map[int64]*overloadBucket
}

var globalOverloadBreaker = &overloadBreaker{windows: make(map[int64]map[int64]*overloadBucket)}

// record 记入一次请求结果并返回该账号窗口内的 (总数, 过载数)。
func (b *overloadBreaker) record(accountID int64, overloaded bool, windowMinutes int, now time.Time) (int, int) {
	minute := now.Unix() / 60
	cutoff := minute - int64(windowMinutes) + 1

	b.mu.Lock()
	defer b.mu.Unlock()
	buckets := b.windows[accountID]
	if buckets == nil {
		buckets = make(map[int64]*overloadBucket)
		b.windows[accountID] = buckets
	}
	for m := range buckets {
		if m < cutoff {
			delete(buckets, m)
		}
	}
	bucket := buckets[minute]
	if bucket == nil {
		bucket = &overloadBucket{}
		buckets[minute] = bucket
	}
	bucket.total++
	if overloaded {
		bucket.overloaded++
	}
	total, over := 0, 0
	for _, item := range buckets {
		total += item.total
		over += item.overloaded
	}
	return total, over
}

// reset 清空账号的统计窗口（触发暂停后调用，恢复时从零开始，避免立即复触）。
func (b *overloadBreaker) reset(accountID int64) {
	b.mu.Lock()
	delete(b.windows, accountID)
	b.mu.Unlock()
}

// shouldTripOverload 报告窗口计数是否达到熔断条件。
func shouldTripOverload(total, overloaded, thresholdPercent int) bool {
	if overloaded == 0 || total < overloadMinSamples {
		return false
	}
	return overloaded*100 >= total*thresholdPercent
}

// isOverloadedUsageError 报告该条用量日志是否为上游过载错误。
func isOverloadedUsageError(input *database.UsageLogInput) bool {
	return input != nil && input.StatusCode >= 400 &&
		strings.Contains(input.ErrorMessage, overloadErrorCode)
}

// noteOverloadOutcome 在用量日志落库前记入过载统计，达到阈值时暂停该账号调度。
// 要求 input.Channel 已解析（logUsage 里在渠道固化之后调用）。
func (h *Handler) noteOverloadOutcome(input *database.UsageLogInput) {
	settings := CurrentRuntimeSettings()
	if !settings.CodexOverloadPauseEnabled {
		return
	}
	if input == nil || input.AccountID <= 0 || input.Channel != database.UpstreamChannelCodex {
		return
	}
	overloaded := isOverloadedUsageError(input)
	total, over := globalOverloadBreaker.record(input.AccountID, overloaded, settings.CodexOverloadWindowMinutes, time.Now())
	// 只有本次是过载错误时才可能新越过阈值，顺带避免为成功请求做无谓判定。
	if !overloaded || !shouldTripOverload(total, over, settings.CodexOverloadThresholdPercent) {
		return
	}
	if h == nil || h.store == nil {
		return
	}
	account := h.store.FindByID(input.AccountID)
	if account == nil || account.HasActiveCooldown() {
		return
	}
	globalOverloadBreaker.reset(input.AccountID)
	pause := time.Duration(settings.CodexOverloadPauseMinutes) * time.Minute
	message := fmt.Sprintf("过载熔断：%d 分钟窗口内 %d/%d 次 %s，暂停调度 %d 分钟",
		settings.CodexOverloadWindowMinutes, over, total, overloadErrorCode, settings.CodexOverloadPauseMinutes)
	h.store.MarkCooldownWithErrorExactDuration(account, pause, overloadPauseReason, message)
	log.Printf("[账号 %d] %s", input.AccountID, message)
}
