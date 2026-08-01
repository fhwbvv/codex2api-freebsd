package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/database"
	"github.com/codex2api/proxy"
	"github.com/gin-gonic/gin"
)

func TestSettingsResponseCacheGetPartialUpdateAndUnrelatedUpdate(t *testing.T) {
	handler, db, baseline := newResponseCacheSettingsAdminHandler(t)

	get := invokeResponseCacheSettingsAdmin(t, handler, http.MethodGet, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%s", get.Code, get.Body.String())
	}
	assertSettingsResponseCache(t, decodeResponseCacheSettingsResponse(t, get), baseline)

	total := baseline.LocalMaxBytes + 8<<20
	update := invokeResponseCacheSettingsAdmin(t, handler, http.MethodPut, map[string]any{
		"response_cache_local_max_bytes": total,
	})
	if update.Code != http.StatusOK {
		t.Fatalf("partial PUT status = %d, body=%s", update.Code, update.Body.String())
	}
	want := baseline
	want.LocalMaxBytes = total
	want.Generation++
	assertSettingsResponseCache(t, decodeResponseCacheSettingsResponse(t, update), want)
	persisted, err := db.GetResponseCacheSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted != want {
		t.Fatalf("persisted cache settings = %+v, want %+v", persisted, want)
	}
	applied := proxy.GetResponseCacheAppliedConfig()
	if applied.Generation != want.Generation ||
		applied.LocalMaxBytes != want.LocalMaxBytes ||
		applied.LocalMaxEntryBytes != want.LocalMaxEntryBytes ||
		applied.ReconstructMaxBytes != want.ReconstructMaxBytes {
		t.Fatalf("runtime config = %+v, want committed %+v", applied, want)
	}

	unrelated := invokeResponseCacheSettingsAdmin(t, handler, http.MethodPut, map[string]any{
		"site_name": "unrelated update",
	})
	if unrelated.Code != http.StatusOK {
		t.Fatalf("unrelated PUT status = %d, body=%s", unrelated.Code, unrelated.Body.String())
	}
	assertSettingsResponseCache(t, decodeResponseCacheSettingsResponse(t, unrelated), want)
	afterUnrelated, err := db.GetResponseCacheSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if afterUnrelated != want {
		t.Fatalf("unrelated PUT changed cache settings: got=%+v want=%+v", afterUnrelated, want)
	}
}

func TestSettingsResponseCacheBoundariesAndMergedConstraint(t *testing.T) {
	valid := []struct {
		name  string
		patch map[string]any
	}{
		{name: "total lower", patch: map[string]any{"response_cache_local_max_bytes": int64(8 << 20)}},
		{name: "total upper", patch: map[string]any{"response_cache_local_max_bytes": int64(4 << 30)}},
		{name: "entry lower", patch: map[string]any{"response_cache_local_max_entry_bytes": int64(1 << 20)}},
		{name: "entry upper", patch: map[string]any{
			"response_cache_local_max_bytes":       int64(512 << 20),
			"response_cache_local_max_entry_bytes": int64(256 << 20),
		}},
		{name: "reconstruct lower", patch: map[string]any{"response_cache_reconstruct_max_bytes": int64(8 << 20)}},
		{name: "reconstruct upper", patch: map[string]any{"response_cache_reconstruct_max_bytes": int64(512 << 20)}},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			handler, db, _ := newResponseCacheSettingsAdminHandler(t)
			recorder := invokeResponseCacheSettingsAdmin(t, handler, http.MethodPut, tt.patch)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			response := decodeResponseCacheSettingsResponse(t, recorder)
			persisted, err := db.GetResponseCacheSettings(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			assertSettingsResponseCache(t, response, persisted)
		})
	}

	invalid := []struct {
		name  string
		patch map[string]any
	}{
		{name: "total below", patch: map[string]any{"response_cache_local_max_bytes": int64(8<<20 - 1)}},
		{name: "total above", patch: map[string]any{"response_cache_local_max_bytes": int64(4<<30 + 1)}},
		{name: "entry below", patch: map[string]any{"response_cache_local_max_entry_bytes": int64(1<<20 - 1)}},
		{name: "entry above", patch: map[string]any{"response_cache_local_max_entry_bytes": int64(256<<20 + 1)}},
		{name: "reconstruct below", patch: map[string]any{"response_cache_reconstruct_max_bytes": int64(8<<20 - 1)}},
		{name: "reconstruct above", patch: map[string]any{"response_cache_reconstruct_max_bytes": int64(512<<20 + 1)}},
		{name: "entry above merged total", patch: map[string]any{
			"response_cache_local_max_bytes":       int64(8 << 20),
			"response_cache_local_max_entry_bytes": int64(9 << 20),
		}},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			handler, db, baseline := newResponseCacheSettingsAdminHandler(t)
			appliedBefore := proxy.GetResponseCacheAppliedConfig()
			recorder := invokeResponseCacheSettingsAdmin(t, handler, http.MethodPut, tt.patch)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			persisted, err := db.GetResponseCacheSettings(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if persisted != baseline {
				t.Fatalf("invalid PUT changed persisted settings: got=%+v want=%+v", persisted, baseline)
			}
			if applied := proxy.GetResponseCacheAppliedConfig(); applied != appliedBefore {
				t.Fatalf("invalid PUT changed runtime: before=%+v after=%+v", appliedBefore, applied)
			}
		})
	}
}

func TestSettingsResponseCacheGenerationWriteIsRejectedForEveryJSONType(t *testing.T) {
	for _, value := range []string{"2", "null", `"2"`} {
		t.Run(value, func(t *testing.T) {
			handler, db, baseline := newResponseCacheSettingsAdminHandler(t)
			appliedBefore := proxy.GetResponseCacheAppliedConfig()
			body := []byte(`{"response_cache_config_generation":` + value + `}`)
			recorder := invokeRawResponseCacheSettingsAdmin(t, handler, http.MethodPut, body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			persisted, err := db.GetResponseCacheSettings(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if persisted != baseline || proxy.GetResponseCacheAppliedConfig() != appliedBefore {
				t.Fatalf("generation write changed state: persisted=%+v baseline=%+v", persisted, baseline)
			}
		})
	}
}

func TestResponseCacheSettingsInvalidExplicitValueIsRejectedBeforeStoreRead(t *testing.T) {
	handler := &Handler{}
	recorder := invokeResponseCacheSettingsAdmin(t, handler, http.MethodPut, map[string]any{
		"response_cache_local_max_bytes": int64(database.MinResponseCacheLocalMaxBytes - 1),
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestResponseCacheSettingsNarrowPersistCompletesBeforeRuntimeApply(t *testing.T) {
	t.Run("success applies committed snapshot after persistence", func(t *testing.T) {
		handler, _, baseline := newResponseCacheSettingsAdminHandler(t)
		appliedBefore := proxy.GetResponseCacheAppliedConfig()
		committed := baseline
		committed.LocalMaxBytes += 8 << 20
		committed.Generation++
		persisted := false
		handler.cacheCfgStore = &stubResponseCacheSettingsStore{
			get: func(context.Context) (database.ResponseCacheSettings, error) {
				return baseline, nil
			},
			update: func(context.Context, database.ResponseCacheSettingsUpdate) (database.ResponseCacheSettings, error) {
				if got := proxy.GetResponseCacheAppliedConfig(); got != appliedBefore {
					t.Fatalf("runtime changed before persistence completed: before=%+v got=%+v", appliedBefore, got)
				}
				persisted = true
				return committed, nil
			},
		}

		recorder := invokeResponseCacheSettingsAdmin(t, handler, http.MethodPut, map[string]any{
			"response_cache_local_max_bytes": committed.LocalMaxBytes,
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if !persisted {
			t.Fatal("narrow persistence was not called")
		}
		if got := proxy.GetResponseCacheAppliedConfig(); got.Generation != committed.Generation ||
			got.LocalMaxBytes != committed.LocalMaxBytes {
			t.Fatalf("runtime did not apply committed snapshot: %+v", got)
		}
	})

	t.Run("narrow failure never applies", func(t *testing.T) {
		handler, _, baseline := newResponseCacheSettingsAdminHandler(t)
		appliedBefore := proxy.GetResponseCacheAppliedConfig()
		handler.cacheCfgStore = &stubResponseCacheSettingsStore{
			get: func(context.Context) (database.ResponseCacheSettings, error) {
				return baseline, nil
			},
			update: func(context.Context, database.ResponseCacheSettingsUpdate) (database.ResponseCacheSettings, error) {
				if got := proxy.GetResponseCacheAppliedConfig(); got != appliedBefore {
					t.Fatalf("runtime changed before failed persistence: before=%+v got=%+v", appliedBefore, got)
				}
				return database.ResponseCacheSettings{}, errors.New("synthetic narrow persistence failure")
			},
		}
		recorder := invokeResponseCacheSettingsAdmin(t, handler, http.MethodPut, map[string]any{
			"response_cache_local_max_bytes": baseline.LocalMaxBytes + 8<<20,
		})
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
		}
		if got := proxy.GetResponseCacheAppliedConfig(); got != appliedBefore {
			t.Fatalf("narrow failure changed runtime: before=%+v after=%+v", appliedBefore, got)
		}
	})
}

type stubResponseCacheSettingsStore struct {
	get    func(context.Context) (database.ResponseCacheSettings, error)
	update func(context.Context, database.ResponseCacheSettingsUpdate) (database.ResponseCacheSettings, error)
}

func (s *stubResponseCacheSettingsStore) GetResponseCacheSettings(ctx context.Context) (database.ResponseCacheSettings, error) {
	return s.get(ctx)
}

func (s *stubResponseCacheSettingsStore) UpdateResponseCacheSettings(
	ctx context.Context,
	update database.ResponseCacheSettingsUpdate,
) (database.ResponseCacheSettings, error) {
	return s.update(ctx, update)
}

func newResponseCacheSettingsAdminHandler(
	t *testing.T,
) (*Handler, *database.DB, database.ResponseCacheSettings) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	systemSettings := defaultBootstrapSettings()
	if err := db.UpdateSystemSettings(context.Background(), systemSettings); err != nil {
		t.Fatalf("seed system settings: %v", err)
	}

	appliedGeneration := proxy.GetResponseCacheAppliedConfig().Generation
	cacheSettings, err := db.GetResponseCacheSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for cacheSettings.Generation <= appliedGeneration {
		next := int64(65 << 20)
		if cacheSettings.ReconstructMaxBytes == next {
			next = 64 << 20
		}
		cacheSettings, err = db.UpdateResponseCacheSettings(
			context.Background(),
			database.ResponseCacheSettingsUpdate{ReconstructMaxBytes: &next},
		)
		if err != nil {
			t.Fatalf("advance cache generation: %v", err)
		}
	}
	if !proxy.ApplyResponseCacheSettings(cacheSettings) {
		t.Fatalf("apply test cache settings %+v", cacheSettings)
	}

	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	store := auth.NewStore(db, tc, systemSettings)
	t.Cleanup(store.Stop)
	handler := NewHandler(
		store,
		db,
		tc,
		proxy.NewRateLimiter(systemSettings.GlobalRPM),
		"admin-secret",
	)
	return handler, db, cacheSettings
}

func invokeResponseCacheSettingsAdmin(
	t *testing.T,
	handler *Handler,
	method string,
	payload map[string]any,
) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			t.Fatalf("json.Marshal(payload): %v", err)
		}
	}
	return invokeRawResponseCacheSettingsAdmin(t, handler, method, body)
}

func invokeRawResponseCacheSettingsAdmin(
	t *testing.T,
	handler *Handler,
	method string,
	body []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, "/api/admin/settings", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	if method == http.MethodGet {
		handler.GetSettings(ctx)
	} else {
		handler.UpdateSettings(ctx)
	}
	return recorder
}

func decodeResponseCacheSettingsResponse(t *testing.T, recorder *httptest.ResponseRecorder) settingsResponse {
	t.Helper()
	var response settingsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode settings response: %v; body=%s", err, recorder.Body.String())
	}
	return response
}

func assertSettingsResponseCache(
	t *testing.T,
	got settingsResponse,
	want database.ResponseCacheSettings,
) {
	t.Helper()
	if got.ResponseCacheLocalMaxBytes != want.LocalMaxBytes ||
		got.ResponseCacheLocalMaxEntryBytes != want.LocalMaxEntryBytes ||
		got.ResponseCacheReconstructMaxBytes != want.ReconstructMaxBytes ||
		got.ResponseCacheConfigGeneration != want.Generation {
		t.Fatalf(
			"settings response cache = total:%d entry:%d reconstruct:%d generation:%d, want %+v",
			got.ResponseCacheLocalMaxBytes,
			got.ResponseCacheLocalMaxEntryBytes,
			got.ResponseCacheReconstructMaxBytes,
			got.ResponseCacheConfigGeneration,
			want,
		)
	}
}
