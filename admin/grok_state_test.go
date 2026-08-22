package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
	"github.com/codex2api/proxy"
)

func newGrokAdminTestAccount(t *testing.T, serverURL string, disabled bool) (*Handler, *database.DB, int64) {
	t.Helper()
	db := newTestAdminDB(t)
	credentials := map[string]interface{}{
		"upstream_type": auth.UpstreamGrok,
		"api_key":       "xai-secret-test-key",
		"base_url":      serverURL + "/v1",
		"plan_type":     "api",
	}
	id, err := db.InsertAccountWithUpstream(context.Background(), "grok-test", "xai", auth.UpstreamGrok, credentials, "")
	if err != nil {
		t.Fatalf("InsertAccountWithUpstream: %v", err)
	}
	if disabled {
		if err := db.SetAccountEnabled(context.Background(), id, false); err != nil {
			t.Fatalf("SetAccountEnabled: %v", err)
		}
	}
	store := auth.NewStore(db, nil, nil)
	t.Cleanup(store.Stop)
	if err := store.LoadAccountByID(context.Background(), id); err != nil {
		t.Fatalf("LoadAccountByID: %v", err)
	}
	return &Handler{db: db, store: store}, db, id
}

func newGrokOAuthAdminTestAccount(t *testing.T, serverURL string) (*Handler, *database.DB, int64) {
	t.Helper()
	db := newTestAdminDB(t)
	id, err := db.InsertAccountWithUpstream(context.Background(), "grok-oauth-test", "xai", auth.UpstreamGrok, map[string]interface{}{
		"upstream_type": auth.UpstreamGrok,
		"access_token":  "oauth-access-token",
		"base_url":      serverURL + "/v1",
		"plan_type":     "archive-label",
	}, "")
	if err != nil {
		t.Fatalf("InsertAccountWithUpstream: %v", err)
	}
	store := auth.NewStore(db, nil, nil)
	t.Cleanup(store.Stop)
	if err := store.LoadAccountByID(context.Background(), id); err != nil {
		t.Fatalf("LoadAccountByID: %v", err)
	}
	return &Handler{db: db, store: store}, db, id
}

func TestSanitizeGrokFactDropsIdentityAndPreservesPresence(t *testing.T) {
	payload, presence, err := sanitizeGrokFact(proxy.GrokControlPlaneUser, []byte(`{
		"userId":"sensitive-subject","email":"secret@example.com","subscriptionTier":null
	}`))
	if err != nil {
		t.Fatalf("sanitizeGrokFact: %v", err)
	}
	if _, ok := payload["userId"]; ok {
		t.Fatal("sanitized user fact leaked userId")
	}
	if _, ok := payload["email"]; ok {
		t.Fatal("sanitized user fact leaked email")
	}
	if got := presence["subscriptionTier"]; got != "null" {
		t.Fatalf("subscriptionTier presence = %q, want null", got)
	}
	if value, ok := payload["subscriptionTier"]; !ok || value != nil {
		t.Fatalf("subscriptionTier = %#v, present=%v, want explicit null", value, ok)
	}

	billing, billingPresence, err := sanitizeGrokFact(proxy.GrokControlPlaneBilling, []byte(`{
		"config":{"creditUsagePercent":0,"monthlyLimit":{},"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-08T00:00:00Z","end":"2026-08-15T00:00:00Z"}}
	}`))
	if err != nil {
		t.Fatalf("sanitize billing: %v", err)
	}
	config := billing["config"].(map[string]any)
	if got := config["creditUsagePercent"]; fmt.Sprint(got) != "0" {
		t.Fatalf("creditUsagePercent = %#v, want explicit zero", got)
	}
	if got := config["monthlyLimit"].(map[string]any)["val"]; fmt.Sprint(got) != "0" {
		t.Fatalf("monthlyLimit.val = %#v, want proto zero", got)
	}
	if got := billingPresence["currentPeriod.end"]; got != "value" {
		t.Fatalf("currentPeriod.end presence = %q, want value", got)
	}
}

func TestGrokFactFailureAndFreshnessPolicies(t *testing.T) {
	observed := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	account := &auth.Account{UpstreamType: auth.UpstreamGrok, AccessToken: "at", PlanType: "free"}
	userPayload := map[string]any{"subscriptionTier": "GrokPro"}
	userPresence := map[string]string{"subscriptionTier": "value"}
	if got := grokFactExpiresAt(account, proxy.GrokControlPlaneUser, "ok", userPayload, userPresence, observed); !got.Equal(observed.Add(time.Minute)) {
		t.Fatalf("upgrade-pending user expiry = %v, want 60s", got)
	}
	account.PlanType = "supergrok"
	if got := grokFactExpiresAt(account, proxy.GrokControlPlaneUser, "ok", userPayload, userPresence, observed); !got.Equal(observed.Add(grokFactFreshness)) {
		t.Fatalf("settled user expiry = %v, want standard freshness", got)
	}
	billing := map[string]any{"config": map[string]any{"creditUsagePercent": 99.0}}
	if got := grokFactExpiresAt(account, proxy.GrokControlPlaneBilling, "ok", billing, nil, observed); !got.Equal(observed.Add(30 * time.Second)) {
		t.Fatalf("hot billing expiry = %v, want 30s", got)
	}
	if got := grokFactExpiresAt(account, proxy.GrokControlPlaneBilling, "exhausted", nil, nil, observed); !got.Equal(observed.Add(30 * time.Second)) {
		t.Fatalf("exhausted billing expiry = %v, want 30s", got)
	}
	if got := grokControlPlaneFailureStatus(http.StatusPaymentRequired, []byte(`{"error":{"code":"payment_required"}}`)); got != "unknown" {
		t.Fatalf("ambiguous 402 = %q, want unknown", got)
	}
	if got := grokControlPlaneFailureStatus(http.StatusPaymentRequired, []byte(`{"error":{"code":"balance_exhausted"}}`)); got != "exhausted" {
		t.Fatalf("explicit 402 = %q, want exhausted", got)
	}
	if got := grokControlPlaneFailureStatus(http.StatusForbidden, []byte(`{"error":{"code":"access_denied"}}`)); got != "unknown" {
		t.Fatalf("ambiguous 403 = %q, want unknown", got)
	}
	if got := grokControlPlaneFailureStatus(http.StatusForbidden, []byte(`{"error":{"code":"subscription_required"}}`)); got != "subscription_required" {
		t.Fatalf("explicit 403 = %q, want subscription_required", got)
	}

	old := &database.GrokAccountFact{Status: "ok", ExpiresAt: observed.Add(time.Minute)}
	if !preserveFreshGrokFactOnFailure(proxy.GrokControlPlaneSettings, "unavailable", old, observed) {
		t.Fatal("transient failure should preserve a still-fresh success")
	}
	if preserveFreshGrokFactOnFailure(proxy.GrokControlPlaneBilling, "exhausted", old, observed) {
		t.Fatal("explicit exhaustion must replace an older success immediately")
	}
	if preserveFreshGrokFactOnFailure(proxy.GrokControlPlaneSettings, "unavailable", old, old.ExpiresAt) {
		t.Fatal("expired success must not suppress a new failure observation")
	}
	if !grokSubscriptionFactChanged(nil, userPayload, userPresence) {
		t.Fatal("first explicit live tier must invalidate capabilities from an unknown subscription state")
	}
	if grokSubscriptionFactChanged(nil, map[string]any{}, map[string]string{"subscriptionTier": "missing"}) {
		t.Fatal("missing live tier must not be treated as a subscription value change")
	}
}

func TestClassifyProbeStatusRequiresExplicitBalanceEvidence(t *testing.T) {
	if got := classifyProbeStatus(http.StatusPaymentRequired, ""); got != "payment_required" {
		t.Fatalf("bare 402 probe status = %q, want payment_required", got)
	}
	if got := classifyProbeStatus(http.StatusPaymentRequired, "payment_required"); got != "payment_required" {
		t.Fatalf("ambiguous payment code status = %q, want payment_required", got)
	}
	if got := classifyProbeStatus(http.StatusPaymentRequired, "credit_card_required"); got != "payment_required" {
		t.Fatalf("credit-card requirement status = %q, want payment_required", got)
	}
	for _, code := range []string{"balance_exhausted", "credit_balance_depleted"} {
		if got := classifyProbeStatus(http.StatusPaymentRequired, code); got != "exhausted" {
			t.Fatalf("explicit %q probe status = %q, want exhausted", code, got)
		}
	}
}

func TestSyncGrokAccountStatePersistsRichCatalogAndETags(t *testing.T) {
	var modelCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			modelCalls.Add(1)
			w.Header().Set("ETag", `"catalog-http-v1"`)
			w.Header().Set("x-models-etag", "async-hint-v9")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"grok-test","displayName":"Grok Test","baseUrl":"` + r.Host + `","contextWindow":131072,"maxCompletionTokens":4096,"apiBackend":"responses","supportedInApi":true,"hidden":false,"extraHeaders":{"x-safe":"ok","authorization":"must-drop"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	h, db, id := newGrokAdminTestAccount(t, server.URL, false)
	result, err := h.syncGrokAccountState(context.Background(), id)
	if err != nil {
		t.Fatalf("syncGrokAccountState: %v", err)
	}
	if modelCalls.Load() != 1 || len(result.Models) != 1 || result.Models[0] != "grok-test" {
		t.Fatalf("calls=%d models=%v", modelCalls.Load(), result.Models)
	}
	state, err := db.GetGrokAccountState(context.Background(), id)
	if err != nil {
		t.Fatalf("GetGrokAccountState: %v", err)
	}
	if result.capabilityGeneration != state.CredentialGeneration {
		t.Fatalf("capability generation = %d, want current generation %d after catalog replacement", result.capabilityGeneration, state.CredentialGeneration)
	}
	if len(state.Catalogs) != 1 || len(state.Catalogs[0].Items) != 1 {
		t.Fatalf("catalog state = %+v", state.Catalogs)
	}
	snapshot := state.Catalogs[0].Snapshot
	if snapshot.HTTPETag != `"catalog-http-v1"` || snapshot.ETagHint != "async-hint-v9" {
		t.Fatalf("snapshot etags = HTTP %q hint %q", snapshot.HTTPETag, snapshot.ETagHint)
	}
	item := state.Catalogs[0].Items[0]
	if item.APIBackend != "responses" || item.ContextWindow != 131072 || item.MaxOutputTokens != 4096 {
		t.Fatalf("rich item = %+v", item)
	}
	if item.ExtraHeaders["X-Safe"] != "ok" || item.ExtraHeaders["Authorization"] != "" {
		t.Fatalf("sanitized extra headers = %#v", item.ExtraHeaders)
	}
	row, err := db.GetAccountByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetAccountByID: %v", err)
	}
	if got := row.GetCredentialStringSlice("models"); len(got) != 0 {
		t.Fatalf("catalog sync must not write visible IDs into credentials.models: %#v", got)
	}
	credentialsJSON, _ := json.Marshal(row.Credentials)
	if strings.Contains(string(credentialsJSON), "must-drop") {
		t.Fatalf("legacy model projection leaked rich catalog header: %s", credentialsJSON)
	}
	for _, forbidden := range []string{"extra_headers", "api_backend", "api_base_url", "context_window", "max_output_tokens"} {
		if _, ok := row.Credentials[forbidden]; ok {
			t.Fatalf("legacy model projection persisted rich catalog field %q", forbidden)
		}
	}
}

// TestSyncGrokAccountStatePreservesDeclaredModels 守护批量/编辑设置的模型白名单
// 不会被 5 分钟目录刷新盖成上游当前可见集（免费 OAuth 常见只剩 grok-4.6）。
func TestSyncGrokAccountStatePreservesDeclaredModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"grok-4.6","supportedInApi":true,"hidden":false}]}`))
	}))
	defer server.Close()

	h, db, id := newGrokAdminTestAccount(t, server.URL, false)
	ctx := context.Background()
	declared := []string{"grok-4.5", "grok-4.6"}
	if err := db.UpdateCredentials(ctx, id, map[string]interface{}{"models": declared}); err != nil {
		t.Fatalf("UpdateCredentials: %v", err)
	}
	if !h.store.ApplyAccountModels(id, declared) {
		t.Fatal("ApplyAccountModels failed")
	}

	result, err := h.syncGrokAccountState(ctx, id)
	if err != nil {
		t.Fatalf("syncGrokAccountState: %v", err)
	}
	if len(result.Models) != 1 || result.Models[0] != "grok-4.6" {
		t.Fatalf("sync response models = %#v, want visible catalog [grok-4.6]", result.Models)
	}

	row, err := db.GetAccountByID(ctx, id)
	if err != nil {
		t.Fatalf("GetAccountByID: %v", err)
	}
	if got := row.GetCredentialStringSlice("models"); !reflect.DeepEqual(got, declared) {
		t.Fatalf("declared models overwritten: %#v, want %#v", got, declared)
	}
	account := h.store.FindByID(id)
	if account == nil {
		t.Fatal("runtime account missing")
	}
	if got := append([]string(nil), account.Models...); !reflect.DeepEqual(got, declared) {
		t.Fatalf("runtime models overwritten: %#v, want %#v", got, declared)
	}
}

func TestSyncGrokAccountStateDualWritesSingleRealBillingPeriod(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/user":
			_, _ = w.Write([]byte(`{"subscriptionTier":"GrokPro","email":"secret@example.test"}`))
		case "/v1/settings":
			_, _ = w.Write([]byte(`{"allow_access":true,"private_token":"settings-secret"}`))
		case "/v1/billing":
			_, _ = w.Write([]byte(`{"config":{"creditUsagePercent":42.5,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-08T00:00:00Z","end":"2026-08-15T00:00:00Z"},"monthlyLimit":{"val":2000},"used":{"val":1234},"onDemandCap":{"val":500},"onDemandUsed":{"val":0},"prepaidBalance":{"val":1250}},"accountEmail":"billing-secret@example.test"}`))
		case "/v1/auto-topup-rule":
			_, _ = w.Write([]byte(`{"rule":{"enabled":false,"topupAmount":{"val":500}},"paymentToken":"topup-secret"}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"grok-safe","apiBackend":"responses","supportedInApi":true}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	h, db, id := newGrokOAuthAdminTestAccount(t, server.URL)
	if _, err := h.syncGrokAccountState(context.Background(), id); err != nil {
		t.Fatalf("syncGrokAccountState: %v", err)
	}
	row, err := db.GetAccountByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetAccountByID: %v", err)
	}
	detailRaw := row.GetCredential("grok_billing_detail")
	var detail proxy.GrokBillingDetail
	if err := json.Unmarshal([]byte(detailRaw), &detail); err != nil {
		t.Fatalf("legacy grok_billing_detail = %q: %v", detailRaw, err)
	}
	if detail.WeeklyPercent == nil || *detail.WeeklyPercent != 42.5 || detail.WeeklyPeriodEnd != "2026-08-15T00:00:00Z" {
		t.Fatalf("legacy weekly detail = %+v", detail)
	}
	if detail.MonthlyPercent != nil || detail.MonthlyPeriodStart != "" || detail.MonthlyPeriodEnd != "" {
		t.Fatalf("weekly current period was duplicated as monthly/30D: %+v", detail)
	}
	if _, ok := row.Credentials["grok_monthly_usage_percent"].(float64); ok {
		t.Fatal("weekly current period populated legacy monthly percentage")
	}
	for _, forbidden := range []string{"codex_5h_used_percent", "codex_7d_used_percent", "codex_5h_reset_at", "codex_7d_reset_at"} {
		if _, ok := row.Credentials[forbidden]; ok {
			t.Fatalf("billing sync populated generic usage field %q", forbidden)
		}
	}
	if row.GetCredential("plan_type") != "archive-label" || detail.Plan != "" {
		t.Fatalf("billing inferred/overwrote plan: archive=%q detail=%q", row.GetCredential("plan_type"), detail.Plan)
	}
	credentialsJSON, _ := json.Marshal(row.Credentials)
	for _, secret := range []string{"secret@example.test", "settings-secret", "billing-secret@example.test", "topup-secret"} {
		if strings.Contains(string(credentialsJSON), secret) {
			t.Fatalf("legacy projection leaked sensitive field %q: %s", secret, credentialsJSON)
		}
	}
}

func TestRunGrokCapabilityProbeThreeProtocolsForDisabledAccount(t *testing.T) {
	var responsesCalls, chatCalls, messagesCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch r.URL.Path {
		case "/v1/responses":
			responsesCalls.Add(1)
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
		case "/v1/chat/completions":
			chatCalls.Add(1)
			_, _ = w.Write([]byte("data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n"))
		case "/v1/messages":
			messagesCalls.Add(1)
			_, _ = w.Write([]byte("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\ndata: {\"type\":\"message_stop\"}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	h, db, id := newGrokAdminTestAccount(t, server.URL, true)
	row, err := db.GetAccountByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetAccountByID: %v", err)
	}
	origin := server.URL + "/v1"
	if applied, err := db.ReplaceGrokModelCatalog(context.Background(), database.GrokModelCatalogSnapshot{
		AccountID: id, Origin: origin, CredentialGeneration: row.CredentialGeneration,
		AuthKind: auth.GrokAuthKindAPIKey, Status: "ok",
	}, []database.GrokModelCatalogItem{{ModelID: "grok-test", APIBackend: "responses", SupportedInAPI: true, FieldPresence: map[string]string{"supported_in_api": "value"}}}); err != nil || !applied {
		t.Fatalf("ReplaceGrokModelCatalog applied=%v err=%v", applied, err)
	}

	result, err := h.runGrokCapabilityProbe(context.Background(), id, true)
	if err != nil {
		t.Fatalf("runGrokCapabilityProbe: %v", err)
	}
	if responsesCalls.Load() != 1 || chatCalls.Load() != 1 || messagesCalls.Load() != 1 {
		t.Fatalf("probe calls responses/chat/messages = %d/%d/%d", responsesCalls.Load(), chatCalls.Load(), messagesCalls.Load())
	}
	if len(result.Results) != 3 {
		t.Fatalf("probe results = %+v", result.Results)
	}
	for _, item := range result.Results {
		if item.Status != "ok" {
			t.Fatalf("probe result = %+v, want ok", item)
		}
	}
	caps, err := db.GetGrokModelCapabilities(context.Background(), id)
	if err != nil || len(caps) != 3 {
		t.Fatalf("persisted capabilities len=%d err=%v", len(caps), err)
	}
	for _, capability := range caps {
		if capability.Status != "ok" || !strings.EqualFold(capability.ModelID, "grok-test") {
			t.Fatalf("persisted capability = %+v", capability)
		}
	}
}

func TestRunGrokCapabilityProbeKnownEmptyCatalogDoesNotUseDefaults(t *testing.T) {
	var inferenceCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inferenceCalls.Add(1)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer server.Close()
	h, db, id := newGrokAdminTestAccount(t, server.URL, false)
	row, err := db.GetAccountByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetAccountByID: %v", err)
	}
	if applied, err := db.ReplaceGrokModelCatalog(context.Background(), database.GrokModelCatalogSnapshot{
		AccountID: id, Origin: server.URL + "/v1", CredentialGeneration: row.CredentialGeneration,
		AuthKind: auth.GrokAuthKindAPIKey, Status: "ok",
	}, nil); err != nil || !applied {
		t.Fatalf("ReplaceGrokModelCatalog applied=%v err=%v", applied, err)
	}
	result, err := h.runGrokCapabilityProbe(context.Background(), id, true)
	if err != nil {
		t.Fatalf("runGrokCapabilityProbe: %v", err)
	}
	if len(result.Results) != 0 || inferenceCalls.Load() != 0 {
		t.Fatalf("known empty catalog produced results=%v calls=%d", result.Results, inferenceCalls.Load())
	}
}

func TestInspectGrokProbeResponseRecordsFirstTokenFromDelta(t *testing.T) {
	started := time.Now().Add(-80 * time.Millisecond)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n" +
				"data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n")),
	}
	observation := inspectGrokProbeResponse(context.Background(), proxy.GrokProtocolChatCompletions, resp, nil, started)
	if observation.status != "ok" || observation.firstTokenMs < 80 {
		t.Fatalf("delta first token = %+v", observation)
	}
}

func TestInspectGrokProbeResponseUsesCompletionWhenNoDelta(t *testing.T) {
	started := time.Now().Add(-50 * time.Millisecond)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")),
	}
	observation := inspectGrokProbeResponse(context.Background(), proxy.GrokProtocolResponses, resp, nil, started)
	if observation.status != "ok" || observation.firstTokenMs < 50 {
		t.Fatalf("completed-only first token = %+v", observation)
	}
}

// 探针体带 max_output_tokens:1，思考型模型的正常收尾是 response.incomplete。
// 只认 completed 会让每个通的 Responses 口被判成 unavailable（http_status=200
// 却 status=unavailable），进而永久关闭 Native 直通。
func TestInspectGrokProbeResponseAcceptsIncompleteTerminal(t *testing.T) {
	started := time.Now().Add(-50 * time.Millisecond)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n" +
				"data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")),
	}
	observation := inspectGrokProbeResponse(context.Background(), proxy.GrokProtocolResponses, resp, nil, started)
	if observation.status != "ok" {
		t.Fatalf("truncated probe must count as ok: %+v", observation)
	}
	if observation.httpStatus != http.StatusOK {
		t.Fatalf("http status = %d, want 200", observation.httpStatus)
	}
}

func TestInspectGrokProbeResponseAcceptsIncompleteNonStreaming(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":1,"output_tokens":1}}`)),
	}
	observation := inspectGrokProbeResponse(context.Background(), proxy.GrokProtocolResponses, resp, nil, time.Now())
	if observation.status != "ok" {
		t.Fatalf("truncated non-streaming probe must count as ok: %+v", observation)
	}
}

// response.failed 仍然是失败，不能被截断放行规则带偏。
func TestInspectGrokProbeResponseStillRejectsFailedTerminal(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"x\"}}}\n\n")),
	}
	observation := inspectGrokProbeResponse(context.Background(), proxy.GrokProtocolResponses, resp, nil, time.Now())
	if observation.status == "ok" {
		t.Fatalf("failed terminal must not be ok: %+v", observation)
	}
}

func TestGrokProbeResponsesTerminalOK(t *testing.T) {
	for _, tc := range []struct {
		status, eventType string
		want              bool
	}{
		{"completed", "", true},
		{"incomplete", "", true},
		{"failed", "", false},
		{"", "response.completed", true},
		{"", "response.incomplete", true},
		{"", "response.failed", false},
		{"", "response.output_text.delta", false},
	} {
		if got := grokProbeResponsesTerminalOK(tc.status, tc.eventType); got != tc.want {
			t.Errorf("grokProbeResponsesTerminalOK(%q,%q) = %v, want %v", tc.status, tc.eventType, got, tc.want)
		}
	}
}
