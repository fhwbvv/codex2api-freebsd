package proxy

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
)

func newProxyPremiumTestStore() *auth.Store {
	return auth.NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency:                   4,
		TestConcurrency:                  1,
		TestModel:                        "gpt-5.4",
		BackgroundRefreshIntervalMinutes: 2,
		UsageProbeMaxAgeMinutes:          10,
		RecoveryProbeIntervalMinutes:     30,
	})
}

func TestApply429CooldownPremium5hWindowMarksRateLimited(t *testing.T) {
	store := newProxyPremiumTestStore()
	acc := &auth.Account{
		DBID:        1,
		AccessToken: "token",
		PlanType:    "plus",
		Status:      auth.StatusReady,
	}
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("x-codex-primary-used-percent", "100")
	resp.Header.Set("x-codex-primary-window-minutes", "300")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "1800")

	decision := Apply429Cooldown(store, acc, nil, resp, "gpt-5.4")

	if decision.Scope != rateLimitScopeAccount || decision.Reason != "rate_limited_5h" {
		t.Fatalf("Apply429Cooldown() = %#v, want premium 5h account cooldown", decision)
	}
	if !acc.IsPremium5hRateLimited() {
		t.Fatal("account should enter premium 5h rate_limited state")
	}
	if got := acc.RuntimeStatus(); got != "rate_limited" {
		t.Fatalf("RuntimeStatus() = %q, want %q", got, "rate_limited")
	}
	if acc.IsAvailable() {
		t.Fatal("IsAvailable() = true, want false while premium 5h limit is active")
	}
	if got := acc.GetDynamicConcurrencyLimit(); got != 1 {
		t.Fatalf("GetDynamicConcurrencyLimit() = %d, want 1", got)
	}
}

func TestApply429CooldownUnknownRateLimitSetsModelCooldown(t *testing.T) {
	store := newProxyPremiumTestStore()
	acc := &auth.Account{
		DBID:        1,
		AccessToken: "token",
		PlanType:    "pro",
		Status:      auth.StatusReady,
	}

	start := time.Now()
	decision := Apply429Cooldown(store, acc, []byte(`{"error":{"type":"rate_limit_error"}}`), nil, "gpt-5.4")

	if decision.Scope != rateLimitScopeModel {
		t.Fatalf("Apply429Cooldown().Scope = %q, want model", decision.Scope)
	}
	if decision.ResetAt.Before(start.Add(4*time.Minute)) || decision.ResetAt.After(start.Add(6*time.Minute)) {
		t.Fatalf("ResetAt = %v, want about 5m from now", decision.ResetAt)
	}
	if !acc.IsModelRateLimited("gpt-5.4") {
		t.Fatal("account model should enter short cooldown")
	}
}

func TestApply429CooldownUsageLimitWithoutResetStaysAccountScoped(t *testing.T) {
	store := newProxyPremiumTestStore()
	acc := &auth.Account{
		DBID:        1,
		AccessToken: "token",
		PlanType:    "pro",
		Status:      auth.StatusReady,
	}

	start := time.Now()
	decision := Apply429Cooldown(store, acc, []byte(`{"error":{"type":"usage_limit_reached"}}`), nil, "gpt-5.4")

	if decision.Scope != rateLimitScopeAccount || decision.Reason != "usage_limit" {
		t.Fatalf("Apply429Cooldown() = %#v, want account usage_limit", decision)
	}
	if decision.ResetAt.Before(start.Add(4*time.Hour)) || decision.ResetAt.After(start.Add(6*time.Hour)) {
		t.Fatalf("ResetAt = %v, want about 5h from now", decision.ResetAt)
	}
	if acc.IsModelRateLimited("gpt-5.4") {
		t.Fatal("usage_limit_reached should not be stored as a model cooldown")
	}
}

func TestApply429CooldownUsageLimitTriggersImmediateUsageProbe(t *testing.T) {
	store := newProxyPremiumTestStore()
	acc := &auth.Account{
		DBID:        2,
		AccessToken: "token",
		PlanType:    "plus",
		Status:      auth.StatusReady,
	}
	store.AddAccount(acc)
	probed := make(chan *auth.Account, 1)
	store.SetUsageProbeFunc(func(_ context.Context, account *auth.Account) error {
		probed <- account
		return nil
	})

	Apply429Cooldown(
		store,
		acc,
		[]byte(`{"error":{"type":"usage_limit_reached"}}`),
		nil,
		"gpt-5.4",
	)

	select {
	case got := <-probed:
		if got != acc {
			t.Fatalf("usage probe account = %p, want %p", got, acc)
		}
		if !got.InLimitedState() {
			t.Fatal("usage probe started before the Responses cooldown was visible")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("usage probe was not triggered immediately after Responses limit")
	}
}

func TestSyncCodexUsageStatePremium5hOnlyHeadersMarksRateLimited(t *testing.T) {
	store := newProxyPremiumTestStore()
	acc := &auth.Account{
		DBID:        1,
		AccessToken: "token",
		PlanType:    "team",
		Status:      auth.StatusReady,
	}
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("x-codex-primary-used-percent", "100")
	resp.Header.Set("x-codex-primary-window-minutes", "300")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "900")

	result := SyncCodexUsageState(store, acc, resp)

	if result.HasUsage7d {
		t.Fatal("HasUsage7d = true, want false for 5h-only headers")
	}
	if !result.HasUsage5h {
		t.Fatal("HasUsage5h = false, want true")
	}
	if !result.Persisted5hOnly {
		t.Fatal("Persisted5hOnly = false, want true")
	}
	if !result.Premium5hRateLimited {
		t.Fatal("Premium5hRateLimited = false, want true")
	}
	if !acc.IsPremium5hRateLimited() {
		t.Fatal("account should enter premium 5h rate_limited state from headers alone")
	}
}

func TestSyncCodexUsageStateCreditAccountSkipsPremium5hWindowLimit(t *testing.T) {
	store := newProxyPremiumTestStore()
	acc := &auth.Account{
		DBID:                  1,
		AccessToken:           "token",
		PlanType:              "team",
		Status:                auth.StatusReady,
		CreditEnabled:         true,
		CreditSkipUsageWindow: true,
	}
	// 信用开关现在还要求当下确实有积分可花，快照缺失会按「没有积分」处理。
	acc.SetCreditBalance("1000.0000000000", true, false, false)
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("x-codex-primary-used-percent", "100")
	resp.Header.Set("x-codex-primary-window-minutes", "300")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "900")

	result := SyncCodexUsageState(store, acc, resp)

	if !result.HasUsage5h {
		t.Fatal("HasUsage5h = false, want true")
	}
	if !result.Persisted5hOnly {
		t.Fatal("Persisted5hOnly = false, want true")
	}
	if result.Premium5hRateLimited {
		t.Fatal("Premium5hRateLimited = true, want false for credit account")
	}
	if acc.IsPremium5hRateLimited() {
		t.Fatal("credit account should not enter premium 5h rate_limited state from usage-window headers")
	}
	pct5h, _, ok := acc.GetUsageSnapshot5h()
	if !ok || pct5h != 100 {
		t.Fatalf("5h snapshot = (%v, %v), want 100 with valid snapshot", pct5h, ok)
	}
}

func TestSyncCodexUsageStateIgnoredLimitRecordsSnapshotForContinuationOnly(t *testing.T) {
	store := auth.NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency:         4,
		TestConcurrency:        1,
		TestModel:              "gpt-5.4",
		IgnoreUsageLimitStatus: true,
	})
	acc := &auth.Account{
		DBID:        2,
		AccessToken: "token",
		PlanType:    "team",
		Status:      auth.StatusReady,
	}
	store.AddAccount(acc)
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}
	resp.Header.Set("x-codex-primary-used-percent", "100")
	resp.Header.Set("x-codex-primary-window-minutes", "300")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "900")

	result := SyncCodexUsageState(store, acc, resp)

	if !result.UsageWindowLimitsIgnored {
		t.Fatal("UsageWindowLimitsIgnored = false, want true")
	}
	if !result.HasUsage5h || result.UsagePct5h != 100 {
		t.Fatalf("5h snapshot = (%v, %v), want 100 and valid", result.UsagePct5h, result.HasUsage5h)
	}
	if result.Premium5hRateLimited || acc.IsPremium5hRateLimited() {
		t.Fatal("100% usage metadata must not create a premium cooldown when ignored")
	}
	if acc.IsAvailable() {
		t.Fatal("100% usage account must stay out of fresh-session scheduling")
	}
	store.BindSessionAffinity("working-turn", acc, "")
	continued, _ := store.NextForContinuationWithFilter("working-turn", 0, nil, nil)
	if continued != acc {
		t.Fatal("100% usage metadata prevented the existing turn from continuing")
	}
	store.Release(continued)
}

func TestApply429CooldownUsageLimitStillBlocksWhenUsageStatusIgnored(t *testing.T) {
	store := auth.NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency:         4,
		TestConcurrency:        1,
		TestModel:              "gpt-5.4",
		IgnoreUsageLimitStatus: true,
	})
	acc := &auth.Account{DBID: 3, AccessToken: "token", PlanType: "plus", Status: auth.StatusReady}
	store.AddAccount(acc)
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header)}
	body := []byte(`{"error":{"type":"usage_limit_reached","plan_type":"plus","resets_in_seconds":1800}}`)

	decision := Apply429Cooldown(store, acc, body, resp, "gpt-5.4")

	if decision.Scope != rateLimitScopeAccount {
		t.Fatalf("decision.Scope = %q, want account", decision.Scope)
	}
	if acc.IsAvailable() {
		t.Fatal("429 usage_limit_reached must keep the account unavailable")
	}
	if !acc.HasActiveCooldown() {
		t.Fatal("429 usage_limit_reached must create an explicit cooldown")
	}
	if got := acc.GetCooldownReason(); got != auth.ResponsesRateLimitedCooldownReason {
		t.Fatalf("cooldown reason = %q, want authoritative Responses rejection", got)
	}
	store.BindSessionAffinity("working-turn", acc, "")
	if got, _ := store.NextForContinuationWithFilter("working-turn", 0, nil, nil); got != nil {
		store.Release(got)
		t.Fatal("authoritative Responses 429 must stop an existing turn")
	}
}

func TestWebSocketResponseFailedUsageLimitStillBlocksWhenUsageStatusIgnored(t *testing.T) {
	store := auth.NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency:         4,
		TestConcurrency:        1,
		TestModel:              "gpt-5.4",
		IgnoreUsageLimitStatus: true,
	})
	acc := &auth.Account{DBID: 4, AccessToken: "token", PlanType: "plus", Status: auth.StatusReady}
	store.AddAccount(acc)
	handler := &Handler{store: store}
	payload := []byte(`{"type":"response.failed","response":{"error":{"type":"usage_limit_reached","plan_type":"plus","resets_in_seconds":1800}}}`)

	decision := handler.applyResponseFailedCooldown(acc, payload, &http.Response{Header: make(http.Header)}, "gpt-5.4")

	if decision.Scope != rateLimitScopeAccount {
		t.Fatalf("decision.Scope = %q, want account", decision.Scope)
	}
	if acc.IsAvailable() || !acc.HasActiveCooldown() {
		t.Fatal("WebSocket response.failed usage_limit_reached must create an account cooldown")
	}
}
