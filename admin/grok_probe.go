package admin

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
	"github.com/codex2api/proxy"
)

const (
	// The shortest persisted fact TTL is 30 seconds (hot/exhausted billing), so
	// the read-only scanner must wake at least this often. It does no inference
	// generation and is intentionally independent from the operator probe switch.
	grokFreshnessScanInterval = 30 * time.Second
	// grokProbeRunGuard 单轮探测的整体超时兜底,避免异常账号把整轮卡死。
	grokProbeRunGuard = 15 * time.Minute
)

// StartGrokStatusProbe starts two independent maintenance loops:
//   - an always-on, read-only 30-second freshness scan for control-plane facts
//     and model catalogs, followed by a one-time native capability rebuild when
//     the current credential generation has no fresh capability facts;
//   - the historical inference connectivity probe, still governed by the
//     operator's GrokProbeEnabled switch and configured interval.
//
// This separation is security-relevant: disabling generation probes must not
// let a live plan, allow_access gate, balance, or catalog remain stale forever.
func (h *Handler) StartGrokStatusProbe(ctx context.Context) {
	if h == nil || h.store == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Refresh once at startup, then at the shortest fact freshness. The worker
	// only selects expired observations, so a hot billing row does not cause
	// user/settings/catalog requests every 30 seconds.
	h.startDBBackgroundTaskWithParent(ctx, func(ctx context.Context) {
		h.runGrokFreshnessScan(ctx)
		ticker := time.NewTicker(grokFreshnessScanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.runGrokFreshnessScan(ctx)
			}
		}
	})

	// Generation/connectivity remains optional and retains the coarser settings
	// cadence. Keeping it in a separate loop prevents a disabled probe switch or
	// a slow generation attempt from suppressing freshness maintenance.
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

// grokImportProbeSlots bounds read-only post-import control-plane/catalog
// synchronization so a large archive cannot fan out thousands of requests.
var grokImportProbeSlots = make(chan struct{}, 4)

// triggerGrokUsageProbe keeps its historical import-call-site name. It first
// performs the fenced control-plane/catalog synchronization and then schedules
// the required three minimal native capability probes. The latter are bounded
// globally and serialised per account by runGrokCapabilityProbe.
func (h *Handler) triggerGrokUsageProbe(accountID int64) {
	if h == nil || h.store == nil || h.db == nil || accountID <= 0 {
		return
	}
	h.startDBBackgroundTask(func(parent context.Context) {
		select {
		case grokImportProbeSlots <- struct{}{}:
		case <-parent.Done():
			return
		}
		defer func() { <-grokImportProbeSlots }()

		syncCtx, cancel := context.WithTimeout(parent, 2*time.Minute)
		if _, err := h.syncGrokAccountState(syncCtx, accountID); err != nil {
			log.Printf("[账号 %d] 导入后 Grok 只读状态同步失败: %v", accountID, err)
			cancel()
			return
		}
		cancel()
		if _, err := h.runGrokCapabilityProbe(parent, accountID, false); err != nil {
			log.Printf("[账号 %d] 导入后 Grok 协议能力探针失败: %v", accountID, err)
		}
	})
}

func grokPersistedStateRefreshSelection(account *auth.Account, state *database.GrokAccountState, now time.Time) grokStateSyncSelection {
	if account == nil {
		return grokStateSyncSelection{}
	}
	generation := account.GetCredentialGeneration()
	if generation <= 0 {
		generation = 1
	}
	if state == nil || state.CredentialGeneration != generation {
		return fullGrokStateSyncSelection()
	}
	selection := grokStateSyncSelection{FactKinds: map[proxy.GrokControlPlaneFactKind]struct{}{}}
	if account.GrokAuthKind() == auth.GrokAuthKindOAuth {
		for _, item := range []struct {
			persisted string
			kind      proxy.GrokControlPlaneFactKind
		}{
			{database.GrokFactUser, proxy.GrokControlPlaneUser},
			{database.GrokFactSettings, proxy.GrokControlPlaneSettings},
			{database.GrokFactBilling, proxy.GrokControlPlaneBilling},
			{database.GrokFactAutoTopup, proxy.GrokControlPlaneAutoTopup},
		} {
			fact, ok := state.Facts[item.persisted]
			if !ok || fact.CredentialGeneration != state.CredentialGeneration || !now.Before(fact.ExpiresAt) {
				selection.FactKinds[item.kind] = struct{}{}
			}
		}
	}
	origin, _ := account.GrokCredentials()
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	for _, catalog := range state.Catalogs {
		snapshot := catalog.Snapshot
		if snapshot.CredentialGeneration == state.CredentialGeneration &&
			strings.EqualFold(strings.TrimRight(strings.TrimSpace(snapshot.Origin), "/"), origin) &&
			snapshot.Status == "ok" && now.Before(snapshot.ExpiresAt) {
			return selection
		}
	}
	selection.Catalog = true
	return selection
}

func grokPersistedStateNeedsRefresh(account *auth.Account, state *database.GrokAccountState, now time.Time) bool {
	return !grokPersistedStateRefreshSelection(account, state, now).empty()
}

// refreshStaleGrokControlPlane keeps user/settings/billing/catalog snapshots at
// their declared freshness without consuming account reservations or dispatch
// counters. It is intentionally independent from GrokProbeEnabled.
func (h *Handler) refreshStaleGrokControlPlane(ctx context.Context, accounts []*auth.Account) (refreshed, failed int) {
	if h == nil || h.db == nil {
		return 0, 0
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, account := range accounts {
		if account == nil || account.ID() <= 0 {
			continue
		}
		state, err := h.db.GetGrokAccountState(ctx, account.ID())
		selection := grokPersistedStateRefreshSelection(account, state, time.Now())
		if err == nil && selection.empty() {
			continue
		}
		if err != nil {
			selection = fullGrokStateSyncSelection()
		}
		accountID := account.ID()
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case grokImportProbeSlots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-grokImportProbeSlots }()
			syncCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			_, syncErr := h.syncGrokAccountStateSelected(syncCtx, accountID, selection)
			cancel()
			mu.Lock()
			if syncErr != nil {
				failed++
			} else {
				refreshed++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return refreshed, failed
}

func (h *Handler) runGrokFreshnessScan(ctx context.Context) {
	accounts := h.store.EnabledGrokAccounts()
	if len(accounts) == 0 {
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, grokProbeRunGuard)
	defer cancel()
	start := time.Now()
	refreshed, refreshFailed := h.refreshStaleGrokControlPlane(probeCtx, accounts)
	capabilityRebuilt, capabilityFailed := h.rebuildMissingGrokCapabilities(probeCtx, accounts)
	if refreshed > 0 || refreshFailed > 0 {
		log.Printf("[grok-freshness] 只读状态刷新完成: refreshed=%d failed=%d 耗时=%s",
			refreshed, refreshFailed, time.Since(start).Round(time.Millisecond))
	}
	if capabilityRebuilt > 0 || capabilityFailed > 0 {
		log.Printf("[grok-capabilities] generation 能力重建完成: rebuilt=%d failed=%d 耗时=%s",
			capabilityRebuilt, capabilityFailed, time.Since(start).Round(time.Millisecond))
	}
}

// rebuildMissingGrokCapabilities is independent from GrokProbeEnabled. The
// operator switch controls recurring connectivity tests, not the one-time
// three-protocol facts required to route a newly imported/rotated generation.
// runGrokCapabilityProbe supplies global concurrency 4, per-account
// serialization and per-(generation,model,origin,protocol) deduplication.
func (h *Handler) rebuildMissingGrokCapabilities(ctx context.Context, accounts []*auth.Account) (rebuilt, failed int) {
	if h == nil || h.db == nil {
		return 0, 0
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, account := range accounts {
		if account == nil || account.ID() <= 0 {
			continue
		}
		generation := account.GetCredentialGeneration()
		if generation <= 0 {
			generation = 1
		}
		state, err := h.db.GetGrokAccountState(ctx, account.ID())
		if err == nil && !grokGenerationNeedsCapabilityProbe(account, state, generation, time.Now()) {
			continue
		}
		accountID := account.ID()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, probeErr := h.runGrokCapabilityProbe(ctx, accountID, false)
			mu.Lock()
			if probeErr != nil {
				failed++
			} else {
				rebuilt++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return rebuilt, failed
}

// runGrokStatusProbe performs only the optional generation connectivity check.
func (h *Handler) runGrokStatusProbe(ctx context.Context) {
	accounts := h.store.EnabledGrokAccounts()
	if len(accounts) == 0 {
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, grokProbeRunGuard)
	defer cancel()
	start := time.Now()
	counts := h.runBatchTest(probeCtx, accounts, 0, h.runSingleBatchTest, nil)
	log.Printf("[grok-probe] 定期生成探测完成: total=%d success=%d rate_limited=%d banned=%d failed=%d 耗时=%s",
		counts.Total, counts.Success, counts.RateLimited, counts.Banned, counts.Failed, time.Since(start).Round(time.Millisecond))
}
