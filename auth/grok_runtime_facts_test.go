package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/codex2api/database"
)

func newGrokRuntimeFactTestAccount(t *testing.T, seedSettings bool) (*database.DB, *Store, *Account, int64, string, time.Time) {
	t.Helper()
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "grok-runtime-facts.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	origin := "https://runtime-facts.example/v1"
	id, err := db.InsertAccountWithUpstream(ctx, "grok-runtime", "xai", UpstreamGrok, map[string]any{
		"upstream_type": UpstreamGrok,
		"api_key":       "xai-runtime-secret",
		"base_url":      origin,
	}, "")
	if err != nil {
		t.Fatalf("InsertAccountWithUpstream: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if seedSettings {
		applied, persistErr := db.UpsertGrokAccountFact(ctx, database.GrokAccountFact{
			AccountID: id, Kind: database.GrokFactSettings, CredentialGeneration: 1,
			Status: "ok", HTTPStatus: 200, Source: "test",
			Payload:       map[string]any{"allow_access": true},
			FieldPresence: map[string]string{"allow_access": "value"},
			ObservedAt:    now, ExpiresAt: now.Add(5 * time.Minute),
		})
		if persistErr != nil || !applied {
			t.Fatalf("seed settings = %v, %v", applied, persistErr)
		}
	}
	if applied, persistErr := db.ReplaceGrokModelCatalog(ctx, database.GrokModelCatalogSnapshot{
		AccountID: id, Origin: origin, CredentialGeneration: 1,
		AuthKind: GrokAuthKindAPIKey, Status: "ok", HTTPETag: `"http-v1"`,
		ObservedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	}, []database.GrokModelCatalogItem{{
		ModelID: "grok-4.5", APIBackend: string(GrokProtocolResponses),
		SupportedInAPI: true, FieldPresence: map[string]string{"supported_in_api": "value"},
	}}); persistErr != nil || !applied {
		t.Fatalf("seed catalog = %v, %v", applied, persistErr)
	}
	if applied, persistErr := db.UpsertGrokModelCapability(ctx, database.GrokModelCapability{
		AccountID: id, ModelID: "grok-4.5", Origin: origin,
		Protocol: string(GrokProtocolResponses), CredentialGeneration: 1,
		Status: GrokCapabilityOK, HTTPStatus: 200, Source: "test",
		ObservedAt: now, ExpiresAt: now.Add(time.Hour),
	}); persistErr != nil || !applied {
		t.Fatalf("seed capability = %v, %v", applied, persistErr)
	}
	store := NewStore(db, nil, nil)
	t.Cleanup(store.Stop)
	if err = store.LoadAccountByID(ctx, id); err != nil {
		t.Fatalf("LoadAccountByID: %v", err)
	}
	account := store.FindByID(id)
	if account == nil {
		t.Fatal("loaded account is nil")
	}
	return db, store, account, id, origin, now
}

func TestGrokRuntimeObservationWithoutPersistentSinkIsNoop(t *testing.T) {
	bare := &Account{DBID: 99, UpstreamType: UpstreamGrok, APIKey: "secret", CredentialGeneration: 1}
	if err := bare.ObserveGrokRuntimeFact(context.Background(), GrokRuntimeFactObservation{
		BillingExhausted: true, ObservedAt: time.Now(),
	}); err != nil {
		t.Fatalf("bare observation: %v", err)
	}

	db, store, _, id, _, _ := newGrokRuntimeFactTestAccount(t, false)
	transient, err := store.BuildTransientAccountByID(context.Background(), id)
	if err != nil {
		t.Fatalf("BuildTransientAccountByID: %v", err)
	}
	if transient.grokRuntimeSink != nil {
		t.Fatal("transient account unexpectedly attached to persistent runtime sink")
	}
	if err = transient.ObserveGrokRuntimeFact(context.Background(), GrokRuntimeFactObservation{
		BillingExhausted: true, HTTPStatus: 402, ObservedAt: time.Now(),
	}); err != nil {
		t.Fatalf("transient observation: %v", err)
	}
	if _, err = db.GetGrokAccountFact(context.Background(), id, database.GrokFactBilling); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("transient observation persisted billing fact: %v", err)
	}
}

func TestGrokRuntimeObservationStaleDatabaseGenerationIsFullyFenced(t *testing.T) {
	db, _, account, id, origin, seededAt := newGrokRuntimeFactTestAccount(t, false)
	newGeneration, applied, err := db.UpdateAccountCredentialsCAS(context.Background(), id, 1, map[string]any{"api_key": "rotated-secret"})
	if err != nil || !applied || newGeneration != 2 {
		t.Fatalf("rotate credentials = generation %d applied %v err %v", newGeneration, applied, err)
	}
	observedAt := seededAt.Add(time.Minute)
	err = account.ObserveGrokRuntimeFact(context.Background(), GrokRuntimeFactObservation{
		Settings: &GrokRuntimeSettingsObservation{
			Payload:       map[string]any{"allow_access": false},
			FieldPresence: map[string]string{"allow_access": "value"},
		},
		ModelsETagHint: "late-hint", BillingExhausted: true,
		ExpireNativeCapability: true, ModelID: "grok-4.5", Origin: origin,
		Protocol: GrokProtocolResponses, HTTPStatus: 402, ObservedAt: observedAt,
	})
	if err != nil {
		t.Fatalf("stale observation should be silently fenced: %v", err)
	}
	for _, kind := range []string{database.GrokFactSettings, database.GrokFactBilling} {
		if _, factErr := db.GetGrokAccountFact(context.Background(), id, kind); !errors.Is(factErr, sql.ErrNoRows) {
			t.Fatalf("stale %s fact persisted: %v", kind, factErr)
		}
	}
	snapshot, _, err := db.GetGrokModelCatalog(context.Background(), id, origin)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ETagHint != "" {
		t.Fatalf("stale hint persisted: %q", snapshot.ETagHint)
	}
	caps, err := db.GetGrokModelCapabilities(context.Background(), id)
	if err != nil || len(caps) != 1 {
		t.Fatalf("capabilities = %#v, err %v", caps, err)
	}
	if !caps[0].ExpiresAt.Equal(seededAt.Add(time.Hour)) {
		t.Fatalf("stale failure changed capability expiry to %v", caps[0].ExpiresAt)
	}
	if account.GrokBillingExhausted || account.GrokAccessAllowed != nil {
		t.Fatalf("stale observation changed memory: billing=%v access=%v", account.GrokBillingExhausted, account.GrokAccessAllowed)
	}
}

func TestGrokRuntimeRepeatedHintDoesNotSwallowBillingFact(t *testing.T) {
	db, _, account, id, origin, seededAt := newGrokRuntimeFactTestAccount(t, false)
	first := seededAt.Add(time.Minute)
	if err := account.ObserveGrokRuntimeFact(context.Background(), GrokRuntimeFactObservation{
		ModelsETagHint: "opaque-hint", Origin: origin, ObservedAt: first,
	}); err != nil {
		t.Fatal(err)
	}
	second := first.Add(time.Second)
	if err := account.ObserveGrokRuntimeFact(context.Background(), GrokRuntimeFactObservation{
		ModelsETagHint: "opaque-hint", Origin: origin,
		BillingExhausted: true, HTTPStatus: 402, ProviderCode: "balance_exhausted",
		ObservedAt: second,
	}); err != nil {
		t.Fatal(err)
	}
	fact, err := db.GetGrokAccountFact(context.Background(), id, database.GrokFactBilling)
	if err != nil {
		t.Fatal(err)
	}
	if !fact.ExpiresAt.Equal(second.Add(30*time.Second)) || fact.Status != "exhausted" || fact.Payload["balance_exhausted"] != true {
		t.Fatalf("billing fact = %#v", fact)
	}
	if account.GrokDispatchHardAllowed(second.Add(time.Second)) {
		t.Fatal("fresh explicit exhausted fact did not close hard dispatch gate")
	}
}

func TestGrokRuntimeDatabaseFailureDoesNotPublishMemory(t *testing.T) {
	db, _, account, id, origin, seededAt := newGrokRuntimeFactTestAccount(t, true)
	before, ok := account.GetGrokRoutingState()
	if !ok || len(before.Capabilities) != 1 || !account.GrokDispatchHardAllowed(time.Now()) {
		t.Fatalf("invalid fixture routing=%#v allowed=%v", before, account.GrokDispatchHardAllowed(time.Now()))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := account.ObserveGrokRuntimeFact(ctx, GrokRuntimeFactObservation{
		Settings: &GrokRuntimeSettingsObservation{
			Payload:       map[string]any{"allow_access": false},
			FieldPresence: map[string]string{"allow_access": "value"},
		},
		BillingExhausted: true, ExpireNativeCapability: true,
		ModelID: "grok-4.5", Origin: origin, Protocol: GrokProtocolResponses,
		HTTPStatus: 402, ObservedAt: seededAt.Add(time.Minute),
	})
	if err == nil {
		t.Fatal("canceled database write unexpectedly succeeded")
	}
	if !account.GrokDispatchHardAllowed(time.Now()) || account.GrokBillingExhausted || account.GrokAccessAllowed == nil || !*account.GrokAccessAllowed {
		t.Fatalf("failed write published hard gate: access=%v billing=%v", account.GrokAccessAllowed, account.GrokBillingExhausted)
	}
	after, ok := account.GetGrokRoutingState()
	if !ok || len(after.Capabilities) != 1 || !after.Capabilities[0].ExpiresAt.Equal(before.Capabilities[0].ExpiresAt) {
		t.Fatalf("failed write published capability expiry: before=%#v after=%#v", before.Capabilities, after.Capabilities)
	}
	fact, err := db.GetGrokAccountFact(context.Background(), id, database.GrokFactSettings)
	if err != nil || fact.Payload["allow_access"] != true {
		t.Fatalf("persisted settings changed after failed write: %#v err %v", fact, err)
	}
}

func TestSanitizeGrokRuntimeSettingsIsAllowlistedAndPresenceAware(t *testing.T) {
	payload, presence := sanitizeGrokRuntimeSettings(&GrokRuntimeSettingsObservation{
		Payload: map[string]any{
			"allow_access": false, "on_demand_enabled": nil,
			"default_model": "grok-4.5", "min_client_version": true,
			"access_token": "must-not-persist",
		},
		FieldPresence: map[string]string{
			"allow_access": "value", "on_demand_enabled": "null",
			"default_model": "value", "min_client_version": "value",
		},
	})
	if payload["allow_access"] != false || payload["on_demand_enabled"] != nil || payload["default_model"] != "grok-4.5" {
		t.Fatalf("safe settings lost: %#v", payload)
	}
	if presence["allow_access"] != "value" || presence["on_demand_enabled"] != "null" || presence["min_client_version"] != "invalid" {
		t.Fatalf("presence = %#v", presence)
	}
	if _, leaked := payload["access_token"]; leaked {
		t.Fatalf("unknown settings field leaked: %#v", payload)
	}
}
