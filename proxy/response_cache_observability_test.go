package proxy

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codex2api/cache"
	"github.com/codex2api/database"
)

func TestResponseCacheDetailedLookupCounters(t *testing.T) {
	t.Run("local hit and miss preserve compatibility counters", func(t *testing.T) {
		resetResponseCacheStateForTest(testResponseCacheConfig())
		t.Cleanup(func() { resetResponseCacheStateForTest(defaultResponseCacheConfig()) })

		setResponseCache("key:1", "local", []json.RawMessage{responseCacheTestItem(1, "local")})
		if got := getResponseCacheResult("key:1", "local"); got.Kind != responseCacheLookupHit {
			t.Fatalf("local lookup = %+v, want hit", got)
		}
		if got := getResponseCacheResult("key:1", "missing"); got.Kind != responseCacheLookupMiss {
			t.Fatalf("missing lookup = %+v, want miss", got)
		}

		stats := GetResponseCacheStats()
		if stats.LocalHits != 1 || stats.LocalMisses != 1 {
			t.Fatalf("local counters = hit:%d miss:%d, want 1/1", stats.LocalHits, stats.LocalMisses)
		}
		if stats.Hits != stats.LocalHits || stats.Misses != stats.LocalMisses {
			t.Fatalf("compatibility counters diverged: %+v", stats)
		}
		if stats.RemoteHits != 0 || stats.RemoteMisses != 0 {
			t.Fatalf("local-only lookup changed remote counters: %+v", stats)
		}
	})

	validItem := responseCacheTestItem(1, "remote")
	validItems := []json.RawMessage{validItem}
	validBytes := responseCacheItemsBytes(validItems)
	tests := []struct {
		name           string
		result         cache.ResponseContextReadResult
		err            error
		reconstruct    int64
		wantKind       responseCacheLookupKind
		wantRemoteHits uint64
		wantRemoteMiss uint64
	}{
		{
			name:           "valid found",
			result:         cache.ResponseContextReadResult{Status: cache.ResponseContextReadFound, Items: validItems},
			reconstruct:    validBytes,
			wantKind:       responseCacheLookupHit,
			wantRemoteHits: 1,
		},
		{
			name:           "explicit miss",
			result:         cache.ResponseContextReadResult{Status: cache.ResponseContextReadMiss},
			reconstruct:    validBytes,
			wantKind:       responseCacheLookupMiss,
			wantRemoteMiss: 1,
		},
		{
			name:        "transport error is not miss",
			err:         errSyntheticBackend,
			reconstruct: validBytes,
			wantKind:    responseCacheLookupBackendError,
		},
		{
			name:        "corrupt status is not miss",
			result:      cache.ResponseContextReadResult{Status: cache.ResponseContextReadCorrupt},
			reconstruct: validBytes,
			wantKind:    responseCacheLookupBackendCorrupt,
		},
		{
			name:        "too large status is not miss",
			result:      cache.ResponseContextReadResult{Status: cache.ResponseContextReadTooLarge},
			reconstruct: validBytes,
			wantKind:    responseCacheLookupReconstructionTooLarge,
		},
		{
			name: "found invalid JSON is not hit",
			result: cache.ResponseContextReadResult{
				Status: cache.ResponseContextReadFound,
				Items:  []json.RawMessage{json.RawMessage(`{"broken":`)},
			},
			reconstruct: validBytes,
			wantKind:    responseCacheLookupBackendCorrupt,
		},
		{
			name:           "found over logical reconstruction limit is not hit",
			result:         cache.ResponseContextReadResult{Status: cache.ResponseContextReadFound, Items: validItems},
			reconstruct:    validBytes - 1,
			wantKind:       responseCacheLookupReconstructionTooLarge,
			wantRemoteHits: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := testResponseCacheConfig()
			config.reconstructMaxBytes = tt.reconstruct
			resetResponseCacheStateForTest(config)
			backend := newRecordingResponseContextBackend(true)
			backend.bounded = tt.result
			backend.boundedErr = tt.err
			SetResponseContextCache(backend)
			t.Cleanup(func() {
				SetResponseContextCache(nil)
				_ = backend.TokenCache.Close()
				resetResponseCacheStateForTest(defaultResponseCacheConfig())
			})

			if got := getResponseCacheResult("key:1", "remote"); got.Kind != tt.wantKind {
				t.Fatalf("lookup = %+v, want kind %v", got, tt.wantKind)
			}
			stats := GetResponseCacheStats()
			// 聚合是端到端口径：远端命中最终拿到上下文，计 Hit 不计 Miss。
			wantMisses, wantHits := uint64(1), uint64(0)
			if tt.wantKind == responseCacheLookupHit {
				wantMisses, wantHits = 0, 1
			}
			if stats.LocalMisses != 1 || stats.Misses != wantMisses || stats.Hits != wantHits {
				t.Fatalf(
					"backend lookup counters = %+v, want localMisses=1 misses=%d hits=%d",
					stats, wantMisses, wantHits,
				)
			}
			if stats.RemoteHits != tt.wantRemoteHits || stats.RemoteMisses != tt.wantRemoteMiss {
				t.Fatalf(
					"remote counters = hit:%d miss:%d, want %d/%d; stats=%+v",
					stats.RemoteHits,
					stats.RemoteMisses,
					tt.wantRemoteHits,
					tt.wantRemoteMiss,
					stats,
				)
			}
		})
	}
}

func TestResponseCacheOversizeBypassAndRejectionCounters(t *testing.T) {
	item := responseCacheTestItem(1, "oversize")
	items := []json.RawMessage{item}
	itemBytes := responseCacheItemsBytes(items)

	t.Run("shared backend hit serves without promotion as bypass", func(t *testing.T) {
		config := testResponseCacheConfig()
		config.maxEntryBytes = itemBytes - 1
		config.reconstructMaxBytes = itemBytes
		resetResponseCacheStateForTest(config)
		backend := newRecordingResponseContextBackend(true)
		backend.bounded = cache.ResponseContextReadResult{
			Status: cache.ResponseContextReadFound,
			Items:  items,
		}
		SetResponseContextCache(backend)
		t.Cleanup(func() {
			SetResponseContextCache(nil)
			_ = backend.TokenCache.Close()
			resetResponseCacheStateForTest(defaultResponseCacheConfig())
		})

		got := getResponseCacheResult("key:1", "remote")
		if got.Kind != responseCacheLookupHit || got.Source != responseCacheSourceBackend || got.Promoted {
			t.Fatalf("lookup = %+v, want serviceable non-promoted backend hit", got)
		}
		stats := GetResponseCacheStats()
		if stats.RemoteHits != 1 || stats.OversizeBypasses != 1 || stats.OversizeRejections != 0 {
			t.Fatalf("shared oversize hit stats = %+v", stats)
		}
		if stats.OversizeSkips != 1 {
			t.Fatalf("compatibility OversizeSkips = %d, want 1", stats.OversizeSkips)
		}
	})

	t.Run("memory write beyond byte budget is rejection", func(t *testing.T) {
		config := testResponseCacheConfig()
		config.maxEntryBytes = itemBytes - 1
		resetResponseCacheStateForTest(config)
		t.Cleanup(func() { resetResponseCacheStateForTest(defaultResponseCacheConfig()) })

		setResponseCache("key:1", "memory", items)
		stats := GetResponseCacheStats()
		if stats.OversizeRejections != 1 || stats.OversizeBypasses != 0 {
			t.Fatalf("memory oversize write stats = %+v", stats)
		}
		if stats.OversizeSkips != 1 {
			t.Fatalf("compatibility OversizeSkips = %d, want 1", stats.OversizeSkips)
		}
	})

	t.Run("shared backend write is neither hit bypass nor memory rejection", func(t *testing.T) {
		config := testResponseCacheConfig()
		config.maxEntryBytes = itemBytes - 1
		resetResponseCacheStateForTest(config)
		backend := newRecordingResponseContextBackend(true)
		SetResponseContextCache(backend)
		t.Cleanup(func() {
			SetResponseContextCache(nil)
			_ = backend.TokenCache.Close()
			resetResponseCacheStateForTest(defaultResponseCacheConfig())
		})

		setResponseCache("key:1", "shared-write", items)
		stats := GetResponseCacheStats()
		if stats.OversizeSkips != 1 || stats.OversizeBypasses != 0 || stats.OversizeRejections != 0 {
			t.Fatalf("shared oversize write stats = %+v", stats)
		}
	})
}

func TestResponseCacheKnownUnavailableCountsOnlyFinal409(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.4","previous_response_id":"resp_missing","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}],"stream":true}`)

	t.Run("final memory 409 counts once", func(t *testing.T) {
		resetResponseCacheStateForTest(testResponseCacheConfig())
		t.Cleanup(func() { resetResponseCacheStateForTest(defaultResponseCacheConfig()) })
		handler := NewHandler(newContinuationCodexStore(), nil, nil, nil)
		recorder := invokeResponsesHandler(t, handler.Responses, raw)
		if recorder.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
		}
		if got := GetResponseCacheStats().KnownUnavailableErrors; got != 1 {
			t.Fatalf("known unavailable errors = %d, want 1", got)
		}
	})

	t.Run("successful relay fallback does not count", func(t *testing.T) {
		resetResponseCacheStateForTest(testResponseCacheConfig())
		t.Cleanup(func() { resetResponseCacheStateForTest(defaultResponseCacheConfig()) })
		upstream := newContinuationRelayUpstream(t, false, new([]byte))
		handler := NewHandler(newContinuationRelayStore(upstream.URL), nil, nil, nil)
		recorder := invokeResponsesHandler(t, handler.Responses, raw)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if got := GetResponseCacheStats().KnownUnavailableErrors; got != 0 {
			t.Fatalf("relay success known unavailable errors = %d, want 0", got)
		}
	})

	t.Run("final backend 503 does not count as known unavailable 409", func(t *testing.T) {
		resetResponseCacheStateForTest(testResponseCacheConfig())
		backend := newRecordingResponseContextBackend(true)
		backend.boundedErr = errSyntheticBackend
		SetResponseContextCache(backend)
		t.Cleanup(func() {
			SetResponseContextCache(nil)
			_ = backend.TokenCache.Close()
			resetResponseCacheStateForTest(defaultResponseCacheConfig())
		})
		handler := NewHandler(newContinuationCodexStore(), nil, nil, nil)
		recorder := invokeResponsesHandler(t, handler.Responses, raw)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
		}
		if got := GetResponseCacheStats().KnownUnavailableErrors; got != 0 {
			t.Fatalf("backend 503 known unavailable errors = %d, want 0", got)
		}
	})
}

func TestResponseCacheOpsSnapshotIncludesOneConsistentState(t *testing.T) {
	resetResponseCacheStateForTest(defaultResponseCacheConfig())
	t.Cleanup(func() { resetResponseCacheStateForTest(defaultResponseCacheConfig()) })
	settings := database.DefaultResponseCacheSettings()
	if !ApplyResponseCacheSettings(settings) {
		t.Fatal("failed to apply generation 1")
	}
	setResponseCache("key:1", "local", []json.RawMessage{responseCacheTestItem(1, "snapshot")})
	_ = getResponseCacheResult("key:1", "local")
	_ = getResponseCacheResult("key:1", "missing")
	lastSuccess := time.Now().UTC().Truncate(time.Microsecond)
	recordResponseCacheConfigSyncSuccess(lastSuccess)
	recordResponseCacheConfigSyncError(errSyntheticBackend)

	snapshot := GetResponseCacheOpsSnapshot()
	if snapshot.EffectiveConfig != snapshot.AppliedConfig {
		t.Fatalf("effective/applied mismatch: %+v", snapshot)
	}
	if snapshot.AppliedConfig.Generation != settings.Generation ||
		snapshot.AppliedConfig.LocalMaxBytes != settings.LocalMaxBytes {
		t.Fatalf("applied config = %+v, want %+v", snapshot.AppliedConfig, settings)
	}
	if snapshot.Stats.LocalHits != 1 || snapshot.Stats.LocalMisses != 1 {
		t.Fatalf("snapshot stats = %+v, want one local hit/miss", snapshot.Stats)
	}
	if !snapshot.LastConfigSyncAt.Equal(lastSuccess) ||
		snapshot.LastConfigSyncError != errSyntheticBackend.Error() {
		t.Fatalf("snapshot sync state = %+v", snapshot)
	}
}

func TestResponseCacheOpsSnapshotConcurrentWithApplyAndCacheOperations(t *testing.T) {
	resetResponseCacheStateForTest(defaultResponseCacheConfig())
	t.Cleanup(func() { resetResponseCacheStateForTest(defaultResponseCacheConfig()) })
	ApplyResponseCacheSettings(database.DefaultResponseCacheSettings())

	var nextGeneration atomic.Int64
	nextGeneration.Store(1)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for index := 0; index < 100; index++ {
				generation := nextGeneration.Add(1)
				ApplyResponseCacheSettings(database.ResponseCacheSettings{
					LocalMaxBytes:       int64(64+worker) << 20,
					LocalMaxEntryBytes:  8 << 20,
					ReconstructMaxBytes: 64 << 20,
					Generation:          generation,
				})
				setResponseCache("anon", "ops-race", []json.RawMessage{responseCacheTestItem(index+1, "race")})
				_ = getResponseCacheResult("anon", "ops-race")
				if index%2 == 0 {
					recordResponseCacheConfigSyncSuccess(time.Now())
				} else {
					recordResponseCacheConfigSyncError(errSyntheticBackend)
				}
				snapshot := GetResponseCacheOpsSnapshot()
				if snapshot.EffectiveConfig != snapshot.AppliedConfig {
					t.Errorf("effective/applied snapshot mismatch: %+v", snapshot)
					return
				}
				if snapshot.Stats.Hits != snapshot.Stats.LocalHits ||
					snapshot.Stats.Misses != snapshot.Stats.LocalMisses {
					t.Errorf("compatibility snapshot mismatch: %+v", snapshot.Stats)
					return
				}
			}
		}(worker)
	}
	close(start)
	wg.Wait()
}
