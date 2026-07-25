package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/database"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

func TestPromptGuardAsyncShadowAuxiliaryLoadNeverFallsBackToRequestPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalDispatcher := defaultPromptGuardShadowDispatcher
	dispatcher := newPromptGuardShadowDispatcher()
	defaultPromptGuardShadowDispatcher = dispatcher
	t.Cleanup(func() {
		defaultPromptGuardShadowDispatcher = originalDispatcher
	})

	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "codex2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := promptGuardTestConfig()
	cfg.Mode = promptfilter.ModeWarn
	cfg.LogMatches = true
	cfg.Advanced.Guard.Mode = promptfilter.GuardModeWarn
	cfg.Advanced.Guard.Layers.ToolOutput.Mode = promptfilter.GuardModeShadow
	cfg.Advanced.Guard.Performance.AsyncShadowAuxiliaryEnabled = true
	cfg.Advanced.Guard.Performance.ShadowWorkers = 4
	cfg.Advanced.Guard.Performance.ShadowQueueSize = 4096
	cfg.Advanced.Guard.Performance.ShadowOverflowMode = promptfilter.GuardShadowOverflowDrop
	cfg = promptfilter.NormalizeConfig(cfg)
	store := auth.NewStore(nil, nil, &database.SystemSettings{MaxConcurrency: 64, TestConcurrency: 1})
	store.SetPromptFilterConfig(cfg)
	handler := NewHandler(store, db, nil, nil)
	handler.SetRuntimeCache(cache.NewMemory(1))

	line := `{"level":"info","component":"builder","message":"compiled package successfully","tests":256}` + "\n"
	input := make([]map[string]any, 0, 9)
	for index := 0; index < 8; index++ {
		input = append(input, map[string]any{
			"type":    "function_call_output",
			"call_id": fmt.Sprintf("call_%d", index),
			"output":  fmt.Sprintf("tool_call=%d\n%s", index, strings.Repeat(line, 32*1024/len(line)+1)[:32*1024]),
		})
	}
	input = append(input, map[string]any{"role": "user", "content": "请总结构建结果并继续正常开发任务。"})
	body, err := json.Marshal(map[string]any{"model": "gpt-5.5", "input": input})
	if err != nil {
		t.Fatal(err)
	}
	// Warm the exact cache once. The load phase models normal Codex turns that
	// resend accumulated tool history; cold-queue overload is covered separately
	// by the saturation/drop test.
	warmContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	warmContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	warmEvaluation := handler.evaluatePromptGuard(warmContext, body, body, "/v1/responses", "gpt-5.5", promptfilter.TransportHTTP)
	warmAudit, ok := warmEvaluation.Decision.DeferredAudit()
	if !ok {
		t.Fatalf("warmup did not produce deferred audit: %+v", warmEvaluation.Decision)
	}
	if warmDecision := defaultPromptGuardPipeline.EvaluateDeferred(context.Background(), warmAudit); warmDecision.Action != promptfilter.ActionAllow {
		t.Fatalf("warmup decision=%+v", warmDecision)
	}

	const (
		requests    = 300
		concurrency = 50
	)
	jobs := make(chan int, requests)
	errs := make(chan error, requests)
	var workers sync.WaitGroup
	beforeFallback := promptGuardShadowFallbackSync.Load()
	beforeDropped := promptGuardShadowDropped.Load()
	beforeEnqueued := promptGuardShadowEnqueued.Load()
	beforeCompleted := promptGuardShadowCompleted.Load()
	started := time.Now()
	for range concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				requestStarted := time.Now()
				evaluation := handler.evaluatePromptGuard(c, body, body, "/v1/responses", "gpt-5.5", promptfilter.TransportHTTP)
				if evaluation.Decision.Action != promptfilter.ActionAllow || evaluation.Decision.Score != 0 {
					errs <- fmt.Errorf("request %d decision=%+v", index, evaluation.Decision)
					continue
				}
				handler.logPromptGuardEvaluation(c, "/v1/responses", "gpt-5.5", "local_filter", "", evaluation)
				if elapsed := time.Since(requestStarted); elapsed > 5*time.Second {
					errs <- fmt.Errorf("request %d prompt guard path took %s", index, elapsed)
				}
			}
		}()
	}
	for index := 0; index < requests; index++ {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("300-request async load took %s", elapsed)
	}
	waitPromptGuardShadowDispatcherIdle(t, dispatcher)
	if got := promptGuardShadowFallbackSync.Load(); got != beforeFallback {
		t.Fatalf("load test entered synchronous overflow fallback: got=%d want=%d", got, beforeFallback)
	}
	enqueued := promptGuardShadowEnqueued.Load() - beforeEnqueued
	dropped := promptGuardShadowDropped.Load() - beforeDropped
	if enqueued+dropped != requests {
		t.Fatalf("enqueued+dropped=%d+%d want=%d", enqueued, dropped, requests)
	}
	if completed := promptGuardShadowCompleted.Load() - beforeCompleted; completed != enqueued {
		t.Fatalf("completed=%d want accepted=%d", completed, enqueued)
	}
	close(dispatcher.queue)
}
