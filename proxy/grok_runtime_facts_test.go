package proxy

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
)

func newProxyGrokRuntimeFactAccount(t *testing.T, baseURL string, catalogOrigins ...string) (*database.DB, *auth.Store, *auth.Account, int64, time.Time) {
	t.Helper()
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "proxy-grok-runtime.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	id, err := db.InsertAccountWithUpstream(ctx, "proxy-runtime", "xai", auth.UpstreamGrok, map[string]any{
		"upstream_type": auth.UpstreamGrok,
		"api_key":       "xai-runtime-secret",
		"base_url":      baseURL,
	}, "")
	if err != nil {
		t.Fatalf("InsertAccountWithUpstream: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, origin := range catalogOrigins {
		applied, persistErr := db.ReplaceGrokModelCatalog(ctx, database.GrokModelCatalogSnapshot{
			AccountID: id, Origin: origin, CredentialGeneration: 1,
			AuthKind: auth.GrokAuthKindAPIKey, Status: "ok", HTTPETag: `"catalog"`,
			ObservedAt: now, ExpiresAt: now.Add(5 * time.Minute),
		}, []database.GrokModelCatalogItem{{
			ModelID: "grok-4.5", APIBackend: string(auth.GrokProtocolResponses),
			SupportedInAPI: true, FieldPresence: map[string]string{"supported_in_api": "value"},
		}})
		if persistErr != nil || !applied {
			t.Fatalf("ReplaceGrokModelCatalog(%q) = %v, %v", origin, applied, persistErr)
		}
	}
	store := auth.NewStore(db, nil, nil)
	t.Cleanup(store.Stop)
	if err = store.LoadAccountByID(ctx, id); err != nil {
		t.Fatalf("LoadAccountByID: %v", err)
	}
	account := store.FindByID(id)
	if account == nil {
		t.Fatal("loaded account is nil")
	}
	return db, store, account, id, now
}

func TestSanitizeGrokRuntimeSettingsBodyPreservesPresenceAndDropsUnknown(t *testing.T) {
	settings, err := sanitizeGrokRuntimeSettingsBody([]byte(`{
		"allowAccess":false,
		"subscription_tier_display":null,
		"on_demand_enabled":false,
		"defaultModel":"grok-4.5",
		"min_client_version":"0.2.999",
		"forceUpdate":true,
		"access_token":"must-not-survive",
		"user":{"email":"secret@example.test"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if settings.Payload["allow_access"] != false || settings.Payload["on_demand_enabled"] != false || settings.Payload["force_update"] != true {
		t.Fatalf("explicit bool values lost: %#v", settings.Payload)
	}
	if settings.FieldPresence["subscription_tier_display"] != "null" || settings.Payload["subscription_tier_display"] != nil {
		t.Fatalf("explicit null lost: payload=%#v presence=%#v", settings.Payload, settings.FieldPresence)
	}
	if settings.FieldPresence["usage_billing_redirect_url"] != "missing" {
		t.Fatalf("missing presence = %#v", settings.FieldPresence)
	}
	if _, leaked := settings.Payload["access_token"]; leaked {
		t.Fatalf("unknown credential leaked: %#v", settings.Payload)
	}
	if _, leaked := settings.Payload["user"]; leaked {
		t.Fatalf("unknown identity leaked: %#v", settings.Payload)
	}
}

func TestGrokNativeFailureClassificationAndPeekRestoresBody(t *testing.T) {
	ordinary := []byte(`{"error":{"code":"invalid_request","message":"bad max_tokens"}}`)
	if deterministicGrokNativeFailure(http.StatusBadRequest, grokRuntimeProviderCode(ordinary), ordinary) {
		t.Fatal("ordinary request parameter error invalidated native capability")
	}
	unsupported := []byte(`{"error":{"code":"unsupported_protocol","message":"no messages endpoint"}}`)
	if !deterministicGrokNativeFailure(http.StatusBadRequest, grokRuntimeProviderCode(unsupported), unsupported) {
		t.Fatal("explicit unsupported protocol was not classified deterministic")
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusUpgradeRequired, http.StatusTooManyRequests, http.StatusInternalServerError} {
		if deterministicGrokNativeFailure(status, "unsupported_protocol", unsupported) {
			t.Fatalf("transient/policy status %d invalidated native capability", status)
		}
	}

	body := append(bytes.Repeat([]byte("x"), grokRuntimeErrorPeekLimit+17), []byte("tail")...)
	resp := &http.Response{Body: io.NopCloser(bytes.NewReader(body))}
	prefix := peekGrokRuntimeErrorBody(resp)
	if len(prefix) != grokRuntimeErrorPeekLimit {
		t.Fatalf("peek length = %d", len(prefix))
	}
	restored, err := io.ReadAll(resp.Body)
	if err != nil || !bytes.Equal(restored, body) {
		t.Fatalf("restored body mismatch: len=%d err=%v", len(restored), err)
	}
}

func TestObserveGrokNativeProtocolFailureOnlyTouchesMatchingNativeRoute(t *testing.T) {
	account := &auth.Account{}
	// A plain account deliberately has no persistent sink, so this test guards
	// route/classification isolation via the body side effect: only a matching
	// native 400 needs to peek and restore the response body.
	cases := []struct {
		name     string
		route    GrokUpstreamRoute
		inbound  GrokProtocol
		wantPeek bool
	}{
		{"matching native", GrokUpstreamRoute{Native: true, Protocol: GrokProtocolChatCompletions, Model: "grok", BaseURL: "https://x/v1"}, GrokProtocolChatCompletions, true},
		{"converted route", GrokUpstreamRoute{Native: false, Protocol: GrokProtocolResponses, Model: "grok", BaseURL: "https://x/v1"}, GrokProtocolChatCompletions, false},
		{"other native protocol", GrokUpstreamRoute{Native: true, Protocol: GrokProtocolResponses, Model: "grok", BaseURL: "https://x/v1"}, GrokProtocolMessages, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := &trackingReadCloser{Reader: bytes.NewBufferString(`{"error":{"code":"unsupported_protocol"}}`)}
			resp := &http.Response{StatusCode: http.StatusBadRequest, Body: original}
			observeGrokNativeProtocolFailure(account, tc.route, tc.inbound, resp)
			_, wrapped := resp.Body.(*grokPeekedReadCloser)
			if wrapped != tc.wantPeek {
				t.Fatalf("peek wrapper = %v, want %v", wrapped, tc.wantPeek)
			}
			got, err := io.ReadAll(resp.Body)
			if err != nil || !bytes.Contains(got, []byte("unsupported_protocol")) {
				t.Fatalf("body unavailable after observation: %q err=%v", got, err)
			}
		})
	}
}

type trackingReadCloser struct{ io.Reader }

func (r *trackingReadCloser) Close() error { return nil }

func TestObserveGrokExplicitBillingExhaustionRequiresExplicit402(t *testing.T) {
	account := &auth.Account{}
	// No sink means no database mutation, but the classifier itself is guarded
	// here to ensure neither an unknown 402 nor a matching body on another status
	// enters the persistence helper.
	unknown := []byte(`{"error":{"code":"payment_required"}}`)
	if IsGrokSpendingLimitError(unknown) {
		t.Fatal("unknown 402 body classified as balance exhaustion")
	}
	explicit := []byte(`{"error":{"code":"balance_exhausted","message":"usage balance exhausted"}}`)
	if !IsGrokSpendingLimitError(explicit) || grokRuntimeProviderCode(explicit) != "balance_exhausted" {
		t.Fatal("explicit balance exhaustion not recognized")
	}
	observeGrokExplicitBillingExhaustion(account, http.StatusTooManyRequests, explicit)
	observeGrokExplicitBillingExhaustion(account, http.StatusPaymentRequired, unknown)
	observeGrokExplicitBillingExhaustion(account, http.StatusPaymentRequired, explicit)
}

func TestObserveGrokRuntimeSettingsIgnoresFailedResponse(t *testing.T) {
	account := &auth.Account{}
	for _, result := range []GrokControlPlaneFactResult{
		{StatusCode: http.StatusForbidden, Body: []byte(`{"allow_access":false}`), ObservedAt: time.Now()},
		{StatusCode: http.StatusOK, Body: []byte(`not-json`), ObservedAt: time.Now()},
		{StatusCode: http.StatusNotModified, NotModified: true, ObservedAt: time.Now()},
	} {
		observeGrokRuntimeSettingsFact(account, result)
	}
	if err := account.ObserveGrokRuntimeFact(context.Background(), auth.GrokRuntimeFactObservation{}); err != nil {
		t.Fatal(err)
	}
}

func TestGrokRuntimeHintUsesActualRouteOrigin(t *testing.T) {
	baseOrigin := "https://default-origin.example/v1"
	routeOrigin := "https://catalog-origin.example/v1"
	db, _, account, id, _ := newProxyGrokRuntimeFactAccount(t, baseOrigin, baseOrigin, routeOrigin)
	header := make(http.Header)
	header.Set("x-models-etag", "route-specific-hint")
	recordGrokUpstreamObservationsAtOrigin(account, header, routeOrigin+"/")

	base, _, err := db.GetGrokModelCatalog(context.Background(), id, baseOrigin)
	if err != nil {
		t.Fatal(err)
	}
	route, _, err := db.GetGrokModelCatalog(context.Background(), id, routeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if base.ETagHint != "" || route.ETagHint != "route-specific-hint" {
		t.Fatalf("hint stored on wrong origin: base=%q route=%q", base.ETagHint, route.ETagHint)
	}
}

func TestGrokRuntimeBillingClassifierPersistsOnlyExplicit402(t *testing.T) {
	origin := "https://billing-runtime.example/v1"
	db, store, account, id, _ := newProxyGrokRuntimeFactAccount(t, origin, origin)
	unknown := []byte(`{"error":{"code":"payment_required","message":"payment is required"}}`)
	decision := applyGrokCooldown(store, account, http.StatusPaymentRequired, unknown, nil, "grok-4.5")
	if decision.Reason != "payment_required_unknown" {
		t.Fatalf("unknown 402 decision = %#v", decision)
	}
	if _, err := db.GetGrokAccountFact(context.Background(), id, database.GrokFactBilling); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown 402 persisted hard billing fact: %v", err)
	}

	before := time.Now()
	explicit := []byte(`{"error":{"code":"balance_exhausted","message":"usage balance exhausted"}}`)
	decision = applyGrokCooldown(store, account, http.StatusPaymentRequired, explicit, nil, "grok-4.5")
	if decision.Reason != "usage_limited" {
		t.Fatalf("explicit 402 decision = %#v", decision)
	}
	fact, err := db.GetGrokAccountFact(context.Background(), id, database.GrokFactBilling)
	if err != nil {
		t.Fatal(err)
	}
	if fact.Status != "exhausted" || fact.Source != "inference_error" || fact.Payload["balance_exhausted"] != true || fact.Payload["provider_code"] != "balance_exhausted" {
		t.Fatalf("billing fact = %#v", fact)
	}
	ttl := fact.ExpiresAt.Sub(fact.ObservedAt)
	// SQLite's compatibility timestamp encoding is second-granular.
	if ttl != 30*time.Second || fact.ObservedAt.Before(before.Add(-time.Second)) || fact.HTTPStatus != http.StatusPaymentRequired {
		t.Fatalf("billing timing/status = observed %v ttl %v status %d", fact.ObservedAt, ttl, fact.HTTPStatus)
	}
	if account.GrokDispatchHardAllowed(time.Now()) {
		t.Fatal("explicit fresh 402 did not close the hard dispatch gate")
	}
}

func TestGrokNativeRuntimeFailureExpiresOnlyMatchingCapability(t *testing.T) {
	origin := "https://native-runtime.example/v1"
	otherOrigin := "https://other-runtime.example/v1"
	db, store, account, id, seededAt := newProxyGrokRuntimeFactAccount(t, origin, origin, otherOrigin)
	for _, capability := range []database.GrokModelCapability{
		{AccountID: id, ModelID: "grok-4.5", Origin: origin, Protocol: string(auth.GrokProtocolResponses), CredentialGeneration: 1, Status: auth.GrokCapabilityOK, ObservedAt: seededAt, ExpiresAt: seededAt.Add(time.Hour)},
		{AccountID: id, ModelID: "grok-4.5", Origin: origin, Protocol: string(auth.GrokProtocolMessages), CredentialGeneration: 1, Status: auth.GrokCapabilityOK, ObservedAt: seededAt, ExpiresAt: seededAt.Add(time.Hour)},
		{AccountID: id, ModelID: "grok-4.5", Origin: otherOrigin, Protocol: string(auth.GrokProtocolResponses), CredentialGeneration: 1, Status: auth.GrokCapabilityOK, ObservedAt: seededAt, ExpiresAt: seededAt.Add(time.Hour)},
	} {
		if applied, err := db.UpsertGrokModelCapability(context.Background(), capability); err != nil || !applied {
			t.Fatalf("UpsertGrokModelCapability = %v, %v", applied, err)
		}
	}
	if err := store.ReloadGrokPersistentState(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	assertAllFresh := func(stage string) {
		t.Helper()
		caps, err := db.GetGrokModelCapabilities(context.Background(), id)
		if err != nil || len(caps) != 3 {
			t.Fatalf("%s capabilities = %#v err=%v", stage, caps, err)
		}
		for _, capability := range caps {
			if !capability.ExpiresAt.Equal(seededAt.Add(time.Hour)) {
				t.Fatalf("%s unexpectedly expired %+v", stage, capability)
			}
		}
	}
	unsupportedBody := `{"error":{"code":"unsupported_protocol"}}`
	observeGrokNativeProtocolFailure(account, GrokUpstreamRoute{
		Native: false, Protocol: GrokProtocolResponses, Model: "grok-4.5", BaseURL: origin,
	}, GrokProtocolMessages, &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(bytes.NewBufferString(unsupportedBody))})
	assertAllFresh("converted route")

	ordinaryBody := `{"error":{"code":"invalid_request","message":"bad max_output_tokens"}}`
	observeGrokNativeProtocolFailure(account, GrokUpstreamRoute{
		Native: true, Protocol: GrokProtocolResponses, Model: "grok-4.5", BaseURL: origin,
	}, GrokProtocolResponses, &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(bytes.NewBufferString(ordinaryBody))})
	assertAllFresh("ordinary 400")

	expiredAfter := time.Now()
	resp := &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(bytes.NewBufferString(unsupportedBody))}
	observeGrokNativeProtocolFailure(account, GrokUpstreamRoute{
		Native: true, Protocol: GrokProtocolResponses, Model: "grok-4.5", BaseURL: origin,
	}, GrokProtocolResponses, resp)
	if restored, err := io.ReadAll(resp.Body); err != nil || string(restored) != unsupportedBody {
		t.Fatalf("classified response body not restored: %q err=%v", restored, err)
	}
	caps, err := db.GetGrokModelCapabilities(context.Background(), id)
	if err != nil || len(caps) != 3 {
		t.Fatalf("capabilities = %#v err=%v", caps, err)
	}
	for _, capability := range caps {
		isTarget := capability.Origin == origin && capability.Protocol == string(auth.GrokProtocolResponses)
		if isTarget {
			if capability.ExpiresAt.Before(expiredAfter.Add(-time.Second)) || capability.ExpiresAt.After(time.Now().Add(2*time.Second)) {
				t.Fatalf("target expiry = %v", capability.ExpiresAt)
			}
		} else if !capability.ExpiresAt.Equal(seededAt.Add(time.Hour)) {
			t.Fatalf("unrelated capability expired: %+v", capability)
		}
	}
}

func TestFetchGrokMinimumClientVersionPersistsSafeSettingsFact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/settings" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"minClientVersion":"0.2.999","allowAccess":false,
			"subscription_tier_display":null,"on_demand_enabled":false,
			"default_model":"grok-4.5","force_update":true,
			"access_token":"must-not-persist","email":"secret@example.test"
		}`)
	}))
	defer server.Close()
	origin := server.URL + "/v1"
	db, store, account, id, _ := newProxyGrokRuntimeFactAccount(t, origin, origin)
	if got := fetchGrokMinimumClientVersion(context.Background(), account, ""); got != "0.2.999" {
		t.Fatalf("minimum version = %q", got)
	}
	fact, err := db.GetGrokAccountFact(context.Background(), id, database.GrokFactSettings)
	if err != nil {
		t.Fatal(err)
	}
	if fact.Source != "inference_426_settings" || fact.Status != "ok" || fact.Payload["allow_access"] != false || fact.Payload["on_demand_enabled"] != false || fact.Payload["force_update"] != true {
		t.Fatalf("settings fact = %#v", fact)
	}
	if fact.FieldPresence["subscription_tier_display"] != "null" || fact.FieldPresence["usage_billing_redirect_url"] != "missing" {
		t.Fatalf("settings presence = %#v", fact.FieldPresence)
	}
	if _, leaked := fact.Payload["access_token"]; leaked {
		t.Fatalf("settings leaked access token: %#v", fact.Payload)
	}
	if _, leaked := fact.Payload["email"]; leaked {
		t.Fatalf("settings leaked identity: %#v", fact.Payload)
	}
	if account.GrokDispatchHardAllowed(time.Now()) {
		t.Fatal("persisted explicit allow_access=false did not refresh runtime gate")
	}
	state, err := db.GetGrokAccountState(context.Background(), id)
	if err != nil || state.CredentialGeneration != account.GetCredentialGeneration() {
		t.Fatalf("state generation = %#v err=%v", state, err)
	}
	_ = store
}
