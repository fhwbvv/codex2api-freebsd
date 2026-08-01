package admin

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/codex2api/auth"
)

// grokProbeRunGuard 单轮探测的整体超时兜底,避免异常账号把整轮卡死。
const grokProbeRunGuard = 15 * time.Minute

// StartGrokStatusProbe 启动 Grok 账号状态定期探测的常驻后台任务。
//
// 背景:账号的限流/冷却状态虽已持久化并在重启时恢复,但上游是滚动窗口——账号可能在网关
// 无流量期间耗尽或恢复,状态就会与真实情况脱节。该任务按 grok 系统设置里的间隔,对所有
// 未停用的 Grok 账号复跑一次连通性测试(复用批量测试的写状态 testFn):200→清冷却转可用,
// 429→按 Grok 语义落权威用量快照并标 usage_limited。开关默认关,由设置页控制。
//
// 采用 1 分钟粗粒度轮询 + lastRun 判定间隔,使设置变更(开/关/改间隔)在一分钟内生效,
// 无需重启或额外的唤醒信号通道。
func (h *Handler) StartGrokStatusProbe(ctx context.Context) {
	if h == nil || h.store == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.startDBBackgroundTaskWithParent(ctx, func(ctx context.Context) {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		var lastRun time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if !h.store.GrokProbeEnabled() {
				continue
			}
			interval := time.Duration(h.store.GrokProbeIntervalMinutes()) * time.Minute
			if !lastRun.IsZero() && time.Since(lastRun) < interval {
				continue
			}
			lastRun = time.Now()
			h.runGrokStatusProbe(ctx)
		}
	})
}

// grokImportProbeSlots 限制导入后探针的并发:每号要做 AT 刷新 + billing 探针 +
// 连通性测试(最多 3 个上游请求),批量导入几千个文件时不设闸会同时打爆上游/代理。
var grokImportProbeSlots = make(chan struct{}, 4)

// triggerGrokUsageProbe schedules the post-import probe chain under the
// database lifecycle so it cannot outlive account persistence on shutdown.
//
// 导入后的号要能直接在列表上看到套餐与用量进度条,需要三步:
//  1. AT 缺失或已过期先强刷一次——导入的 auth.json 常带过期 AT(文件躺了几小时),
//     拿它打 billing 会 401,套餐拿不到还可能被误标 unauthorized 冷却;
//  2. billing 探针:拉套餐/周月额度(套餐列、额度上限);
//  3. 连通性测试:写账号状态(active/限流),真实请求带回 x-ratelimit-* 头、
//     429 时解析权威用量,用量进度条由此点亮。
func (h *Handler) triggerGrokUsageProbe(accountID int64) {
	if h == nil || h.store == nil || h.probeUsage == nil {
		return
	}
	h.startDBBackgroundTask(func(parent context.Context) {
		select {
		case grokImportProbeSlots <- struct{}{}:
		case <-parent.Done():
			return
		}
		defer func() { <-grokImportProbeSlots }()

		account := h.store.FindByID(accountID)
		if account == nil {
			return
		}

		if account.GrokAuthKind() == auth.GrokAuthKindOAuth && grokAccessTokenStale(account) {
			refreshCtx, cancel := context.WithTimeout(parent, 30*time.Second)
			if err := h.store.RefreshSingle(refreshCtx, account.DBID); err != nil {
				log.Printf("[账号 %d] 导入后 AT 刷新失败(探针继续): %v", account.DBID, err)
			}
			cancel()
		}

		probeCtx, cancel := context.WithTimeout(parent, 25*time.Second)
		_ = h.probeUsage(probeCtx, account)
		cancel()

		testCtx, cancel := context.WithTimeout(parent, batchTestAccountTimeout+5*time.Second)
		h.runSingleBatchTest(testCtx, account)
		cancel()
	})
}

// grokAccessTokenStale 判断账号的 access_token 是否缺失或已过期/临期(2 分钟余量)。
// ExpiresAt 未知时保守视为可用,交给 billing 探针的 401 分支兜底。
func grokAccessTokenStale(account *auth.Account) bool {
	if account == nil {
		return true
	}
	account.Mu().RLock()
	defer account.Mu().RUnlock()
	if strings.TrimSpace(account.AccessToken) == "" {
		return true
	}
	if account.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(2 * time.Minute).After(account.ExpiresAt)
}

// runGrokStatusProbe 对所有未停用的 Grok 账号跑一轮写状态的连通性测试。
func (h *Handler) runGrokStatusProbe(ctx context.Context) {
	accounts := h.store.EnabledGrokAccounts()
	if len(accounts) == 0 {
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, grokProbeRunGuard)
	defer cancel()
	start := time.Now()
	counts := h.runBatchTest(probeCtx, accounts, 0, h.runSingleBatchTest, nil)
	log.Printf("[grok-probe] 定期探测完成: total=%d success=%d rate_limited=%d banned=%d failed=%d 耗时=%s",
		counts.Total, counts.Success, counts.RateLimited, counts.Banned, counts.Failed, time.Since(start).Round(time.Millisecond))
}
