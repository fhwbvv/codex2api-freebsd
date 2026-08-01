package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codex2api/database"
)

func TestResponseCacheConfigFirstGenerationAppliesAndNewerGenerationShrinksImmediately(t *testing.T) {
	resetResponseCacheStateForTest(defaultResponseCacheConfig())
	t.Cleanup(func() { resetResponseCacheStateForTest(defaultResponseCacheConfig()) })

	large := json.RawMessage(`{"type":"message","content":"` + strings.Repeat("x", (1<<20)+1024) + `"}`)
	setResponseCache("anon", "large", []json.RawMessage{large})
	before := GetResponseCacheStats()
	if before.Entries != 1 {
		t.Fatalf("precondition stats = %+v, want one entry", before)
	}

	applied := ApplyResponseCacheSettings(database.ResponseCacheSettings{
		LocalMaxBytes:       8 << 20,
		LocalMaxEntryBytes:  1 << 20,
		ReconstructMaxBytes: 8 << 20,
		Generation:          1,
	})
	if !applied {
		t.Fatal("generation 1 was not applied")
	}
	snapshot := GetResponseCacheAppliedConfig()
	if snapshot.Generation != 1 ||
		snapshot.LocalMaxBytes != 8<<20 ||
		snapshot.LocalMaxEntryBytes != 1<<20 ||
		snapshot.ReconstructMaxBytes != 8<<20 {
		t.Fatalf("applied config = %+v", snapshot)
	}
	if snapshot.MaxEntries != responseCacheMaxItems ||
		snapshot.TTL != responseCacheTTL ||
		snapshot.MaxItems != responseCacheMaxPerItem {
		t.Fatalf("fixed cache settings changed: %+v", snapshot)
	}
	after := GetResponseCacheStats()
	if after.Entries != 0 || after.ByteEvictions != before.ByteEvictions+1 {
		t.Fatalf("shrink stats = %+v, before=%+v", after, before)
	}
	if after.Hits != before.Hits || after.Misses != before.Misses {
		t.Fatalf("config apply reset stats: before=%+v after=%+v", before, after)
	}

	for _, stale := range []database.ResponseCacheSettings{
		{LocalMaxBytes: 16 << 20, LocalMaxEntryBytes: 2 << 20, ReconstructMaxBytes: 16 << 20, Generation: 1},
		{LocalMaxBytes: 32 << 20, LocalMaxEntryBytes: 4 << 20, ReconstructMaxBytes: 32 << 20, Generation: 0},
	} {
		if ApplyResponseCacheSettings(stale) {
			t.Fatalf("stale/equal generation unexpectedly applied: %+v", stale)
		}
	}
	if got := GetResponseCacheAppliedConfig(); got != snapshot {
		t.Fatalf("stale/equal generation changed config: got=%+v want=%+v", got, snapshot)
	}

	if !ApplyResponseCacheSettings(database.ResponseCacheSettings{
		LocalMaxBytes:       16 << 20,
		LocalMaxEntryBytes:  2 << 20,
		ReconstructMaxBytes: 32 << 20,
		Generation:          2,
	}) {
		t.Fatal("newer generation was not applied")
	}
	if got := GetResponseCacheAppliedConfig(); got.Generation != 2 || got.LocalMaxBytes != 16<<20 {
		t.Fatalf("newer applied config = %+v", got)
	}
}

func TestResponseCacheConfigPollConvergesRetainsLastGoodAndRecovers(t *testing.T) {
	resetResponseCacheStateForTest(defaultResponseCacheConfig())
	t.Cleanup(func() { resetResponseCacheStateForTest(defaultResponseCacheConfig()) })
	if !ApplyResponseCacheSettings(database.DefaultResponseCacheSettings()) {
		t.Fatal("failed to apply startup generation")
	}

	reader := newControlledResponseCacheSettingsReader()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runResponseCacheSettingsPoller(ctx, reader, 2*time.Millisecond, 100*time.Millisecond)
	}()

	reader.respond(t, responseCacheSettingsReadResult{
		settings: database.ResponseCacheSettings{
			LocalMaxBytes:       128 << 20,
			LocalMaxEntryBytes:  16 << 20,
			ReconstructMaxBytes: 96 << 20,
			Generation:          2,
		},
	})
	waitForResponseCacheConfig(t, 2)
	firstStatus := GetResponseCacheConfigSyncStatus()
	if firstStatus.LastSuccessfulSyncAt.IsZero() || firstStatus.LastSyncError != "" {
		t.Fatalf("first sync status = %+v", firstStatus)
	}

	reader.respond(t, responseCacheSettingsReadResult{err: errors.New("synthetic database outage")})
	waitForResponseCacheSyncError(t, "synthetic database outage")
	if got := GetResponseCacheAppliedConfig(); got.Generation != 2 {
		t.Fatalf("poll failure changed last-good config: %+v", got)
	}

	reader.respond(t, responseCacheSettingsReadResult{
		settings: database.ResponseCacheSettings{
			LocalMaxBytes:       192 << 20,
			LocalMaxEntryBytes:  24 << 20,
			ReconstructMaxBytes: 128 << 20,
			Generation:          3,
		},
	})
	waitForResponseCacheConfig(t, 3)
	recovered := GetResponseCacheConfigSyncStatus()
	if recovered.LastSyncError != "" || !recovered.LastSuccessfulSyncAt.After(firstStatus.LastSuccessfulSyncAt) {
		t.Fatalf("recovered sync status = %+v, first=%+v", recovered, firstStatus)
	}

	reader.waitForCall(t)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("poller did not stop after application context cancellation")
	}
	calls := reader.callCount.Load()
	time.Sleep(15 * time.Millisecond)
	if got := reader.callCount.Load(); got != calls {
		t.Fatalf("poller read after cancellation: before=%d after=%d", calls, got)
	}
	if status := GetResponseCacheConfigSyncStatus(); status.LastSyncError != "" {
		t.Fatalf("context cancellation was recorded as sync error: %+v", status)
	}
}

func TestResponseCacheConfigPollReadHasBoundedTimeout(t *testing.T) {
	resetResponseCacheStateForTest(defaultResponseCacheConfig())
	t.Cleanup(func() { resetResponseCacheStateForTest(defaultResponseCacheConfig()) })
	reader := newControlledResponseCacheSettingsReader()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		runResponseCacheSettingsPoller(ctx, reader, time.Millisecond, 20*time.Millisecond)
	}()
	reader.waitForCall(t)
	waitForResponseCacheSyncError(t, context.DeadlineExceeded.Error())
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("bounded poll read took %s", elapsed)
	}
	cancel()
	<-done
}

func TestResponseCacheConfigStartupLoadAndDatabaseLifecycle(t *testing.T) {
	resetResponseCacheStateForTest(defaultResponseCacheConfig())
	t.Cleanup(func() { resetResponseCacheStateForTest(defaultResponseCacheConfig()) })
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "response-cache-poller.db"))
	if err != nil {
		t.Fatalf("database.New(sqlite) error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	total := int64(128 << 20)
	committed, err := db.UpdateResponseCacheSettings(
		context.Background(),
		database.ResponseCacheSettingsUpdate{LocalMaxBytes: &total},
	)
	if err != nil {
		t.Fatalf("seed response cache settings: %v", err)
	}

	if err := LoadResponseCacheSettings(context.Background(), db); err != nil {
		t.Fatalf("LoadResponseCacheSettings() error = %v", err)
	}
	if got := GetResponseCacheAppliedConfig(); got.Generation != committed.Generation || got.LocalMaxBytes != total {
		t.Fatalf("startup applied config = %+v, want committed %+v", got, committed)
	}

	parent, cancel := context.WithCancel(context.Background())
	if !startResponseCacheSettingsPoller(parent, db, 2*time.Millisecond, 50*time.Millisecond) {
		t.Fatal("failed to register response cache poller with database lifecycle")
	}
	reconstruct := int64(96 << 20)
	newer, err := db.UpdateResponseCacheSettings(
		context.Background(),
		database.ResponseCacheSettingsUpdate{ReconstructMaxBytes: &reconstruct},
	)
	if err != nil {
		t.Fatalf("update for poll convergence: %v", err)
	}
	waitForResponseCacheConfig(t, newer.Generation)

	cancel()
	if !db.DrainBackgroundTasks(time.Second) {
		t.Fatal("poller did not stop cleanly after parent cancellation")
	}
	total = 192 << 20
	latest, err := db.UpdateResponseCacheSettings(
		context.Background(),
		database.ResponseCacheSettingsUpdate{LocalMaxBytes: &total},
	)
	if err != nil {
		t.Fatalf("update after poller cancellation: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	if got := GetResponseCacheAppliedConfig(); got.Generation == latest.Generation {
		t.Fatalf("cancelled poller applied later generation: %+v", got)
	}
	if startResponseCacheSettingsPoller(context.Background(), db, time.Millisecond, 20*time.Millisecond) {
		t.Fatal("poller started after database stopped accepting background tasks")
	}
}

func TestResponseCacheConfigOldPollResultCannotRollbackNewerAdminApply(t *testing.T) {
	resetResponseCacheStateForTest(defaultResponseCacheConfig())
	t.Cleanup(func() { resetResponseCacheStateForTest(defaultResponseCacheConfig()) })
	if !ApplyResponseCacheSettings(database.DefaultResponseCacheSettings()) {
		t.Fatal("failed to apply startup generation")
	}

	reader := newControlledResponseCacheSettingsReader()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runResponseCacheSettingsPoller(ctx, reader, time.Millisecond, 100*time.Millisecond)
	}()
	reader.waitForCall(t)

	admin := database.ResponseCacheSettings{
		LocalMaxBytes:       192 << 20,
		LocalMaxEntryBytes:  24 << 20,
		ReconstructMaxBytes: 128 << 20,
		Generation:          3,
	}
	if !ApplyResponseCacheSettings(admin) {
		t.Fatal("admin generation 3 was not applied")
	}
	reader.results <- responseCacheSettingsReadResult{
		settings: database.ResponseCacheSettings{
			LocalMaxBytes:       128 << 20,
			LocalMaxEntryBytes:  16 << 20,
			ReconstructMaxBytes: 96 << 20,
			Generation:          2,
		},
	}
	time.Sleep(10 * time.Millisecond)
	if got := GetResponseCacheAppliedConfig(); got.Generation != 3 || got.LocalMaxBytes != admin.LocalMaxBytes {
		t.Fatalf("old poll result rolled back admin apply: %+v", got)
	}
	cancel()
	<-done
}

func TestResponseCacheConfigConcurrentPollAdminAndCacheOperations(t *testing.T) {
	resetResponseCacheStateForTest(defaultResponseCacheConfig())
	t.Cleanup(func() { resetResponseCacheStateForTest(defaultResponseCacheConfig()) })
	ApplyResponseCacheSettings(database.DefaultResponseCacheSettings())

	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < 4; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for index := 0; index < 50; index++ {
				generation := int64(2 + worker*50 + index)
				ApplyResponseCacheSettings(database.ResponseCacheSettings{
					LocalMaxBytes:       int64(64+worker) << 20,
					LocalMaxEntryBytes:  8 << 20,
					ReconstructMaxBytes: 64 << 20,
					Generation:          generation,
				})
				setResponseCache("anon", "race", []json.RawMessage{json.RawMessage(`{"type":"message"}`)})
				_ = getResponseCache("anon", "race")
				_ = GetResponseCacheStats()
				_ = GetResponseCacheAppliedConfig()
				_ = GetResponseCacheConfigSyncStatus()
			}
		}()
	}
	close(start)
	wg.Wait()
}

type responseCacheSettingsReadResult struct {
	settings database.ResponseCacheSettings
	err      error
}

type controlledResponseCacheSettingsReader struct {
	calls     chan context.Context
	results   chan responseCacheSettingsReadResult
	callCount atomic.Int64
}

func newControlledResponseCacheSettingsReader() *controlledResponseCacheSettingsReader {
	return &controlledResponseCacheSettingsReader{
		calls:   make(chan context.Context, 8),
		results: make(chan responseCacheSettingsReadResult, 8),
	}
}

func (r *controlledResponseCacheSettingsReader) GetResponseCacheSettings(ctx context.Context) (database.ResponseCacheSettings, error) {
	r.callCount.Add(1)
	select {
	case r.calls <- ctx:
	case <-ctx.Done():
		return database.ResponseCacheSettings{}, ctx.Err()
	}
	select {
	case result := <-r.results:
		return result.settings, result.err
	case <-ctx.Done():
		return database.ResponseCacheSettings{}, ctx.Err()
	}
}

func (r *controlledResponseCacheSettingsReader) waitForCall(t *testing.T) context.Context {
	t.Helper()
	select {
	case ctx := <-r.calls:
		return ctx
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for settings read")
		return nil
	}
}

func (r *controlledResponseCacheSettingsReader) respond(t *testing.T, result responseCacheSettingsReadResult) {
	t.Helper()
	r.waitForCall(t)
	r.results <- result
}

func waitForResponseCacheConfig(t *testing.T, generation int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if GetResponseCacheAppliedConfig().Generation == generation {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for response cache generation %d; got %+v", generation, GetResponseCacheAppliedConfig())
}

func waitForResponseCacheSyncError(t *testing.T, contains string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(GetResponseCacheConfigSyncStatus().LastSyncError, contains) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for sync error %q; got %+v", contains, GetResponseCacheConfigSyncStatus())
}
