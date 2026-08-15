package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/database"
	"github.com/gin-gonic/gin"
)

func newPagedAccountsHandler(t *testing.T) (*Handler, []int64, []int64) {
	t.Helper()
	db := newTestAdminDB(t)
	ctx := context.Background()
	codexIDs := make([]int64, 0, 3)
	for index, plan := range []string{"plus", "team", "free"} {
		id, err := db.InsertAccountWithCredentials(ctx, "codex-"+strconv.Itoa(index+1), map[string]interface{}{
			"refresh_token": "secret-codex-" + strconv.Itoa(index+1),
			"email":         "codex" + strconv.Itoa(index+1) + "@example.com",
			"plan_type":     plan,
		}, "")
		if err != nil {
			t.Fatalf("insert codex account: %v", err)
		}
		codexIDs = append(codexIDs, id)
	}
	grokIDs := make([]int64, 0, 2)
	for index := 0; index < 2; index++ {
		id, err := db.InsertAccountWithUpstream(ctx, "grok-"+strconv.Itoa(index+1), "xai", "oauth", map[string]interface{}{
			"upstream_type": "grok",
			"refresh_token": "secret-grok-" + strconv.Itoa(index+1),
			"email":         "grok" + strconv.Itoa(index+1) + "@example.net",
			"plan_type":     "supergrok",
		}, "")
		if err != nil {
			t.Fatalf("insert grok account: %v", err)
		}
		grokIDs = append(grokIDs, id)
	}
	store := auth.NewStore(db, nil, nil)
	store.SetLazyMode(true)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	tokenCache := cache.NewMemory(1)
	t.Cleanup(func() { _ = tokenCache.Close() })
	return NewHandler(store, db, tokenCache, nil, ""), codexIDs, grokIDs
}

func invokeListAccounts(t *testing.T, handler *Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodGet, target, nil)
	handler.ListAccounts(ginContext)
	return recorder
}

func TestListAccountsPageIsChannelScopedStableAndClamped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, codexIDs, _ := newPagedAccountsHandler(t)

	recorder := invokeListAccounts(t, handler, "/api/admin/accounts?view=page&channel=codex&page=1&page_size=2")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var first accountsPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if first.Total != 3 || first.Page != 1 || first.PageSize != 2 || len(first.Accounts) != 2 {
		t.Fatalf("unexpected page metadata: %+v, rows=%d", first, len(first.Accounts))
	}
	if first.Accounts[0].ID != codexIDs[0] || first.Accounts[1].ID != codexIDs[1] {
		t.Fatalf("default order ids = [%d,%d], want [%d,%d]", first.Accounts[0].ID, first.Accounts[1].ID, codexIDs[0], codexIDs[1])
	}
	for _, account := range first.Accounts {
		if account.GrokAPI {
			t.Fatalf("codex page leaked grok account %d", account.ID)
		}
		if account.DetailLoaded || account.Usage5hDetail != nil || account.Usage7dDetail != nil || account.Billed5h != nil || account.Billed7d != nil || len(account.ModelCooldowns) != 0 {
			t.Fatalf("paged account %d leaked detail-only fields", account.ID)
		}
	}

	recorder = invokeListAccounts(t, handler, "/api/admin/accounts?view=page&channel=codex&page=99&page_size=2")
	var last accountsPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &last); err != nil {
		t.Fatalf("decode clamped page: %v", err)
	}
	if last.Page != 2 || len(last.Accounts) != 1 || last.Accounts[0].ID != codexIDs[2] {
		t.Fatalf("clamped page = %d rows=%+v", last.Page, last.Accounts)
	}
}

func TestListAccountsPageFiltersAndLegacyCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _, grokIDs := newPagedAccountsHandler(t)

	recorder := invokeListAccounts(t, handler, "/api/admin/accounts?view=page&channel=grok&page=1&page_size=500&search=grok2&auth_kind=oauth")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var page accountsPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if page.Total != 1 || len(page.Accounts) != 1 || page.Accounts[0].ID != grokIDs[1] {
		t.Fatalf("filtered rows = %+v", page.Accounts)
	}
	if page.SnapshotAt == "" || page.StatsState == "" {
		t.Fatalf("missing snapshot metadata: %+v", page)
	}

	legacyRecorder := invokeListAccounts(t, handler, "/api/admin/accounts")
	var legacy accountsResponse
	if err := json.Unmarshal(legacyRecorder.Body.Bytes(), &legacy); err != nil {
		t.Fatalf("decode legacy response: %v", err)
	}
	if len(legacy.Accounts) != 5 {
		t.Fatalf("legacy account count = %d, want 5", len(legacy.Accounts))
	}

	emptyRecorder := invokeListAccounts(t, handler, "/api/admin/accounts?view=page&channel=codex&page=4&page_size=20&search=does-not-exist")
	var empty accountsPageResponse
	if err := json.Unmarshal(emptyRecorder.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode empty page: %v", err)
	}
	if emptyRecorder.Code != http.StatusOK || empty.Total != 0 || empty.Page != 1 || len(empty.Accounts) != 0 {
		t.Fatalf("empty page status=%d response=%+v", emptyRecorder.Code, empty)
	}

	for _, target := range []string{
		"/api/admin/accounts?view=page&channel=codex&page_size=501",
		"/api/admin/accounts?view=page&channel=codex&status=typo",
		"/api/admin/accounts?view=page&channel=codex&proxy_filter=this",
	} {
		invalidRecorder := invokeListAccounts(t, handler, target)
		if invalidRecorder.Code != http.StatusBadRequest {
			t.Fatalf("target %q status=%d body=%s", target, invalidRecorder.Code, invalidRecorder.Body.String())
		}
	}
}

func TestAccountListItemMatchesEveryFilter(t *testing.T) {
	item := &accountListSnapshotItem{
		Row:              &database.AccountRow{ID: 7, Name: "Alice", ProxyURL: "http://proxy.example"},
		ID:               7,
		Status:           "active",
		Enabled:          true,
		PlanType:         "supergrok",
		GrokPlanCategory: "supergrok",
		GrokAuthKind:     auth.GrokAuthKindOAuth,
		EmailDomain:      "example.com",
		Tags:             []string{"blue", "paid"},
		GroupIDs:         []int64{10, 20},
		HealthTier:       "risky",
		SearchText:       "7 alice alice@example.com",
	}
	query := accountPageQuery{
		Search: "alice", Status: "active", Plan: "supergrok", AuthKind: auth.GrokAuthKindOAuth,
		Tag: "blue", EmailDomain: "example.com", GroupInclude: []int64{20}, GroupExclude: []int64{30},
		HealthTier: "attention", ProxyURL: item.Row.ProxyURL, ProxyFilter: "this",
	}
	if !accountListItemMatches(item, query, database.UpstreamChannelGrok) {
		t.Fatal("item should match the complete filter set")
	}

	failingQueries := []accountPageQuery{
		{Search: "bob"},
		{Status: "disabled"},
		{Plan: "free"},
		{AuthKind: auth.GrokAuthKindAPIKey},
		{Tag: "missing"},
		{EmailDomain: "other.example"},
		{GroupInclude: []int64{99}},
		{GroupExclude: []int64{10}},
		{Ungrouped: true},
		{HealthTier: "healthy"},
		{ProxyFilter: "unbound"},
	}
	for index, failing := range failingQueries {
		if accountListItemMatches(item, failing, database.UpstreamChannelGrok) {
			t.Fatalf("failing filter %d unexpectedly matched: %+v", index, failing)
		}
	}
}

func TestAccountListSortAlwaysFallsBackToIDAscending(t *testing.T) {
	items := []*accountListSnapshotItem{
		{ID: 3, RequestCount: 5},
		{ID: 1, RequestCount: 5},
		{ID: 2, RequestCount: 5},
	}
	sortAccountListItems(items, "requests", "desc")
	for index, want := range []int64{1, 2, 3} {
		if items[index].ID != want {
			t.Fatalf("tie order ids=%v, want [1 2 3]", []int64{items[0].ID, items[1].ID, items[2].ID})
		}
	}
	items[0].RequestCount = 1
	items[1].RequestCount = 3
	items[2].RequestCount = 3
	sortAccountListItems(items, "requests", "desc")
	if items[0].RequestCount != 3 || items[1].RequestCount != 3 || items[0].ID > items[1].ID {
		t.Fatalf("descending sort lost stable id fallback: %+v", items)
	}
}

func TestPagedAccountRuntimeStatusOverridesDatabaseRow(t *testing.T) {
	handler, codexIDs, _ := newPagedAccountsHandler(t)
	runtimeAccount := handler.store.FindByID(codexIDs[0])
	if runtimeAccount == nil {
		t.Fatal("runtime account missing")
	}
	runtimeAccount.Mu().Lock()
	runtimeAccount.Status = auth.StatusError
	runtimeAccount.ErrorMsg = "runtime failure"
	runtimeAccount.Mu().Unlock()

	recorder := invokeListAccounts(t, handler, "/api/admin/accounts?view=page&channel=codex&page=1&page_size=20&status=error")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page accountsPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if page.Total != 1 || len(page.Accounts) != 1 || page.Accounts[0].ID != codexIDs[0] || page.Accounts[0].Status != "error" {
		t.Fatalf("runtime override missing: %+v", page)
	}
	if page.Summary.Error != 1 {
		t.Fatalf("summary error=%d, want 1", page.Summary.Error)
	}
}

func TestAccountStatsCachesNeverBlockColdOrStaleReads(t *testing.T) {
	handler, _, _ := newPagedAccountsHandler(t)
	started := time.Now()
	_, state := handler.getCachedRequestCountsNonBlocking(database.UpstreamChannelCodex, nil)
	if state != "warming" || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("cold request stats state=%q elapsed=%s", state, time.Since(started))
	}
	// 刷新在后台 goroutine 里完成,轮询等它把本渠道缓存转为 ready。
	warmDeadline := time.Now().Add(2 * time.Second)
	for {
		if _, state = handler.getCachedRequestCountsNonBlocking(database.UpstreamChannelCodex, nil); state == "ready" {
			break
		}
		if time.Now().After(warmDeadline) {
			t.Fatalf("warmed request stats state=%q, want ready", state)
		}
		time.Sleep(10 * time.Millisecond)
	}

	snapshot, err := handler.getAccountListSnapshot(context.Background(), database.UpstreamChannelCodex)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	snapshot.ExpiresAt = time.Now().Add(-time.Second)
	handler.accountListBuildMu.Lock()
	started = time.Now()
	stale, err := handler.getAccountListSnapshot(context.Background(), database.UpstreamChannelCodex)
	elapsed := time.Since(started)
	handler.accountListBuildMu.Unlock()
	if err != nil || stale != snapshot || elapsed > 100*time.Millisecond {
		t.Fatalf("stale snapshot blocked or changed: err=%v elapsed=%s", err, elapsed)
	}
}

func TestCombineAccountStatsState(t *testing.T) {
	if got := combineAccountStatsState("ready", "stale"); got != "stale" {
		t.Fatalf("ready+stale=%q", got)
	}
	if got := combineAccountStatsState("stale", "warming"); got != "warming" {
		t.Fatalf("stale+warming=%q", got)
	}
}

func TestAccountOperationSelectorNeverCrossesChannel(t *testing.T) {
	handler, codexIDs, grokIDs := newPagedAccountsHandler(t)
	ctx := context.Background()
	selected, err := handler.resolveAccountOperationSelector(ctx, &accountOperationSelector{Channel: database.UpstreamChannelGrok})
	if err != nil {
		t.Fatalf("resolve selector: %v", err)
	}
	if len(selected) != len(grokIDs) {
		t.Fatalf("grok selector ids = %v, want %v", selected, grokIDs)
	}
	for _, id := range selected {
		for _, codexID := range codexIDs {
			if id == codexID {
				t.Fatalf("selector leaked codex account %d", id)
			}
		}
	}
}

func TestGetAccountReturnsOneEnrichedAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, codexIDs, _ := newPagedAccountsHandler(t)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(codexIDs[0], 10)}}
	ginContext.Request = httptest.NewRequest(http.MethodGet, "/api/admin/accounts/1", nil)
	handler.GetAccount(ginContext)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var account accountResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &account); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	if account.ID != codexIDs[0] || account.Email != "codex1@example.com" {
		t.Fatalf("account = %+v", account)
	}
	if !account.DetailLoaded {
		t.Fatal("detail response must be marked detail_loaded")
	}
}

func TestAccountAnalysisHasFixedSizeForFortyThousandAccounts(t *testing.T) {
	now := time.Date(2026, time.August, 7, 1, 0, 0, 0, time.UTC)
	items := make([]*accountListSnapshotItem, 40_000)
	for index := range items {
		usage := float64(index % 101)
		items[index] = &accountListSnapshotItem{
			ID:                 int64(index + 1),
			Status:             "active",
			Enabled:            true,
			PlanType:           "plus",
			UsagePercent5h:     usage,
			UsagePercent5hOK:   true,
			UsagePercent7d:     usage,
			UsagePercent7dOK:   true,
			Reset5hAt:          now.Add(5 * time.Hour),
			Reset7dAt:          now.Add(7 * 24 * time.Hour),
			DynamicConcurrency: 2,
		}
	}
	snapshot := &accountListSnapshot{
		Channel: database.UpstreamChannelCodex,
		Items:   items,
		BuiltAt: now,
	}
	analysis := buildAccountAnalysis(snapshot, accountAnalysisTraffic{rpm: 120, rpmLimit: 1000, avgDurationMs: 5000}, now)
	if analysis.Quota["5h"].Sampled != len(items) || analysis.Quota["7d"].Sampled != len(items) {
		t.Fatalf("sampled quota = 5h:%d 7d:%d", analysis.Quota["5h"].Sampled, analysis.Quota["7d"].Sampled)
	}
	if len(analysis.Quota["5h"].Buckets) != 10 || len(analysis.Recovery["5h"].Buckets) != 5 || len(analysis.Recovery["7d"].Buckets) != 7 || len(analysis.Reset.Buckets) != 7 {
		t.Fatalf("unexpected fixed bucket shape: %+v", analysis)
	}
	payload, err := json.Marshal(analysis)
	if err != nil {
		t.Fatalf("marshal analysis: %v", err)
	}
	if len(payload) > 16*1024 {
		t.Fatalf("analysis payload grew with pool size: %d bytes", len(payload))
	}
}

func TestBatchOperationsRejectIDsTogetherWithSelector(t *testing.T) {
	handler, _, _ := newPagedAccountsHandler(t)
	payload := `{"ids":[],"selector":{"channel":"codex"},"enabled":true}`
	tests := []struct {
		name   string
		handle func(*gin.Context)
	}{
		{name: "test", handle: handler.BatchTest},
		{name: "delete", handle: handler.BatchDeleteAccounts},
		{name: "refresh", handle: handler.BatchRefreshAccounts},
		{name: "update", handle: handler.BatchUpdateAccounts},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(recorder)
			ginContext.Request = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/batch-"+test.name, strings.NewReader(payload))
			ginContext.Request.Header.Set("Content-Type", "application/json")
			test.handle(ginContext)
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "ids 与 selector") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

// 删除/封禁后统计卡曾因快照缓存(5s TTL + stale-while-revalidate)不失效而
// 显示变更前数字;失效后同一 TTL 窗口内必须立刻拿到重建的新 summary。
func TestAccountMutationInvalidationServesFreshSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, codexIDs, _ := newPagedAccountsHandler(t)
	ctx := context.Background()

	before, err := handler.getAccountListSnapshot(ctx, database.UpstreamChannelCodex)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if before.Summary.Total != len(codexIDs) {
		t.Fatalf("summary total = %d, want %d", before.Summary.Total, len(codexIDs))
	}

	if err := handler.db.SoftDeleteAccount(ctx, codexIDs[0]); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	// 未失效时,新鲜窗口内仍返回变更前快照 —— 这是 bug 曾经的表现,
	// 也证明修复必须依赖显式失效而非 TTL。
	stale, err := handler.getAccountListSnapshot(ctx, database.UpstreamChannelCodex)
	if err != nil {
		t.Fatalf("stale read: %v", err)
	}
	if stale.Summary.Total != len(codexIDs) {
		t.Fatalf("pre-invalidation total = %d, want stale %d", stale.Summary.Total, len(codexIDs))
	}

	handler.invalidateAccountSnapshotCaches()
	fresh, err := handler.getAccountListSnapshot(ctx, database.UpstreamChannelCodex)
	if err != nil {
		t.Fatalf("fresh read: %v", err)
	}
	if fresh.Summary.Total != len(codexIDs)-1 {
		t.Fatalf("post-invalidation total = %d, want %d", fresh.Summary.Total, len(codexIDs)-1)
	}
}

// 在途重建若跨越了失效点(读库早于账号变更),不得把旧快照写回缓存。
func TestInstallSkipsCacheWhenGenerationMoved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _, _ := newPagedAccountsHandler(t)
	staleGen := handler.accountCachesGen.Load()
	handler.invalidateAccountSnapshotCaches() // 模拟重建读库之后才提交的账号变更
	snapshot := &accountListSnapshot{Channel: database.UpstreamChannelCodex}
	handler.installAccountListSnapshot(database.UpstreamChannelCodex, snapshot, staleGen)
	handler.accountListCacheMu.RLock()
	cached := handler.accountListCache[database.UpstreamChannelCodex]
	handler.accountListCacheMu.RUnlock()
	if cached != nil {
		t.Fatalf("stale-generation snapshot was cached")
	}
	handler.installAccountListSnapshot(database.UpstreamChannelCodex, snapshot, handler.accountCachesGen.Load())
	handler.accountListCacheMu.RLock()
	cached = handler.accountListCache[database.UpstreamChannelCodex]
	handler.accountListCacheMu.RUnlock()
	if cached != snapshot {
		t.Fatalf("current-generation snapshot was not cached")
	}
}

func TestShouldInvalidateAccountSnapshotCaches(t *testing.T) {
	cases := []struct {
		method string
		path   string
		status int
		want   bool
	}{
		{http.MethodDelete, "/api/admin/accounts/7", http.StatusOK, true},
		{http.MethodPost, "/api/admin/accounts/batch-delete", http.StatusOK, true},
		{http.MethodPost, "/api/admin/accounts/7/enable", http.StatusOK, true},
		{http.MethodPatch, "/api/admin/accounts/7/note", http.StatusOK, true},
		{http.MethodDelete, "/api/admin/account-groups/3", http.StatusOK, true},
		{http.MethodGet, "/api/admin/accounts", http.StatusOK, false},
		{http.MethodDelete, "/api/admin/accounts/7", http.StatusNotFound, false},
		{http.MethodPost, "/api/admin/keys", http.StatusOK, false},
		{http.MethodPost, "/api/admin/prompt-filter/review/keys/x", http.StatusOK, false},
	}
	for _, tc := range cases {
		if got := shouldInvalidateAccountSnapshotCaches(tc.method, tc.path, tc.status); got != tc.want {
			t.Fatalf("shouldInvalidate(%s %s %d) = %t, want %t", tc.method, tc.path, tc.status, got, tc.want)
		}
	}
}

// 回归:调度优先级排序依赖列表投影带出 scheduler_priority;投影缺字段时
// 快照全员按 0 打平,排序退化成 ID 序(账号页"排序不生效"反馈)。
func TestListAccountsPageSortsBySchedulerPriority(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, codexIDs, _ := newPagedAccountsHandler(t)
	ctx := context.Background()
	// codexIDs[0] 不设置(视同 0),其余两个分别 50 / 40。
	if err := handler.db.UpdateCredentials(ctx, codexIDs[1], map[string]interface{}{"scheduler_priority": int64(50)}); err != nil {
		t.Fatalf("set priority 50: %v", err)
	}
	if err := handler.db.UpdateCredentials(ctx, codexIDs[2], map[string]interface{}{"scheduler_priority": int64(40)}); err != nil {
		t.Fatalf("set priority 40: %v", err)
	}

	assertOrder := func(order string, want []int64) {
		t.Helper()
		recorder := invokeListAccounts(t, handler,
			"/api/admin/accounts?view=page&channel=codex&page=1&page_size=10&sort=scheduler_priority&order="+order)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
		var page accountsPageResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode page: %v", err)
		}
		got := make([]int64, 0, len(page.Accounts))
		for _, account := range page.Accounts {
			got = append(got, account.ID)
		}
		if len(got) != len(want) {
			t.Fatalf("order=%s got %v, want %v", order, got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("order=%s got %v, want %v", order, got, want)
			}
		}
	}

	assertOrder("desc", []int64{codexIDs[1], codexIDs[2], codexIDs[0]})
	assertOrder("asc", []int64{codexIDs[0], codexIDs[2], codexIDs[1]})
}

func TestListAccountsPageKeepsGrokQuotaBars(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	ctx := context.Background()
	id, err := db.InsertAccountWithUpstream(ctx, "grok-quota", "xai", "oauth", map[string]interface{}{
		"upstream_type":       "grok",
		"refresh_token":       "rt-grok",
		"email":               "grok@example.net",
		"plan_type":           "supergrok",
		"grok_billing_detail": `{"weekly_percent":42.5,"weekly_period_end":"2026-08-15T00:00:00Z"}`,
	}, "")
	if err != nil {
		t.Fatalf("insert grok: %v", err)
	}
	store := auth.NewStore(db, nil, nil)
	store.SetLazyMode(true)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	account := store.FindByID(id)
	if account == nil {
		t.Fatal("runtime account missing")
	}
	account.SetGrokRateLimitSnapshot(auth.GrokRateLimitSnapshot{
		LimitTokens: 1000, RemainingTokens: 400, UpdatedAt: time.Now(),
	})
	tokenCache := cache.NewMemory(1)
	t.Cleanup(func() { _ = tokenCache.Close() })
	handler := NewHandler(store, db, tokenCache, nil, "")

	recorder := invokeListAccounts(t, handler, "/api/admin/accounts?view=page&channel=grok&page=1&page_size=20")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var page accountsPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(page.Accounts) != 1 || page.Accounts[0].ID != id {
		t.Fatalf("page rows = %+v", page.Accounts)
	}
	row := page.Accounts[0]
	if row.DetailLoaded || row.Usage5hDetail != nil || row.ModelMapping != "" {
		t.Fatalf("paged grok row leaked expensive details: %+v", row)
	}
	if len(row.GrokBilling) == 0 || !strings.Contains(string(row.GrokBilling), `"weekly_percent":42.5`) {
		t.Fatalf("paged grok row dropped quota bar payload: %s", row.GrokBilling)
	}
	if row.GrokRateLimit == nil || row.GrokRateLimit.LimitTokens != 1000 || row.GrokRateLimit.RemainingTokens != 400 {
		t.Fatalf("paged grok row dropped rate-limit snapshot: %+v", row.GrokRateLimit)
	}
}

func TestCodexNormalIncludesDisabledButSchedulingExcludesIt(t *testing.T) {
	enabled := &accountListSnapshotItem{Status: "active", Enabled: true}
	disabled := &accountListSnapshotItem{Status: "active", Enabled: false}
	codex := database.UpstreamChannelCodex
	if !accountListStatusMatches(enabled, "normal", codex) {
		t.Fatal("enabled healthy account should match normal")
	}
	if !accountListStatusMatches(disabled, "normal", codex) {
		t.Fatal("disabled account should classify as normal")
	}
	if accountListStatusMatches(disabled, "scheduling", codex) || accountListStatusMatches(disabled, "active", codex) {
		t.Fatal("disabled account must be excluded from scheduling")
	}
	if !accountListStatusMatches(disabled, "disabled", codex) {
		t.Fatal("disabled account should match disabled filter")
	}
	summary, _ := summarizeAccountList([]*accountListSnapshotItem{enabled, disabled}, codex)
	if summary.Normal != 2 || summary.Active != 1 || summary.Disabled != 1 || summary.Total != 2 {
		t.Fatalf("summary = %+v, want Normal=2 Active=1 Disabled=1 Total=2", summary)
	}
}

func TestOverloadPausedCountsAsNormalNotRateLimitedOrScheduling(t *testing.T) {
	item := &accountListSnapshotItem{
		Status:         "overload_paused",
		Enabled:        true,
		CooldownReason: "overload_paused",
	}
	codex := database.UpstreamChannelCodex
	if accountListRateLimited(item) {
		t.Fatal("overload_paused must not count as rate limited")
	}
	if !accountListStatusMatches(item, "normal", codex) {
		t.Fatal("overload_paused should classify as normal")
	}
	if accountListStatusMatches(item, "scheduling", codex) || accountListStatusMatches(item, "active", codex) {
		t.Fatal("overload_paused must be excluded from scheduling")
	}
	if accountListStatusMatches(item, "rate_limited", codex) {
		t.Fatal("overload_paused must not match rate_limited")
	}
	summary, _ := summarizeAccountList([]*accountListSnapshotItem{item}, codex)
	if summary.Normal != 1 || summary.Active != 0 || summary.RateLimited != 0 || summary.OverloadPaused != 1 {
		t.Fatalf("summary = %+v, want Normal=1 Active=0 RateLimited=0 OverloadPaused=1", summary)
	}
}

func TestCodexAuthKindFilterSplitsOAuthAndResponsesAPI(t *testing.T) {
	oauthItem := &accountListSnapshotItem{Status: "active", Enabled: true}
	apiItem := &accountListSnapshotItem{Status: "active", Enabled: true, OpenAIResponses: true}
	codex := database.UpstreamChannelCodex
	if !accountListItemMatches(oauthItem, accountPageQuery{AuthKind: "oauth"}, codex) ||
		accountListItemMatches(apiItem, accountPageQuery{AuthKind: "oauth"}, codex) {
		t.Fatal("auth_kind=oauth should match only non-Responses accounts on codex channel")
	}
	if !accountListItemMatches(apiItem, accountPageQuery{AuthKind: "api_key"}, codex) ||
		accountListItemMatches(oauthItem, accountPageQuery{AuthKind: "api_key"}, codex) {
		t.Fatal("auth_kind=api_key should match only Responses API accounts on codex channel")
	}
	summary, _ := summarizeAccountList([]*accountListSnapshotItem{oauthItem, apiItem}, codex)
	if summary.OAuth != 1 || summary.APIKey != 1 {
		t.Fatalf("summary = %+v, want OAuth=1 APIKey=1", summary)
	}
}
