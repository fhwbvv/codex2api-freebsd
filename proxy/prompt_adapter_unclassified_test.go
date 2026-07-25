package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codex2api/cache"
	"github.com/codex2api/database"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

type adapterAuditCountingCache struct {
	cache.TokenCache
	sessionGets atomic.Int32
	sessionSets atomic.Int32
}

func (c *adapterAuditCountingCache) GetRuntime(ctx context.Context, namespace string, key string) (json.RawMessage, bool, error) {
	if namespace == promptSessionCorrelationNamespace {
		c.sessionGets.Add(1)
	}
	return c.TokenCache.GetRuntime(ctx, namespace, key)
}

func (c *adapterAuditCountingCache) SetRuntime(ctx context.Context, namespace string, key string, value json.RawMessage, ttl time.Duration) error {
	if namespace == promptSessionCorrelationNamespace {
		c.sessionSets.Add(1)
	}
	return c.TokenCache.SetRuntime(ctx, namespace, key, value, ttl)
}

func TestPromptAdapterUnclassifiedPersistsNonPunitiveAuditWithoutExtensionsOrBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("PROMPT_FILTER_NEWAPI_SECRET", "integration-secret")
	var sidecarCalls atomic.Int32
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sidecarCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"action":"block","score":100,"confidence":1}`))
	}))
	defer sidecar.Close()

	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "codex2api.db"))
	if err != nil {
		t.Fatalf("database.New(sqlite): %v", err)
	}
	defer db.Close()

	cfg := promptSessionTestConfig()
	cfg.LogMatches = true
	cfg.Advanced.Guard.Mode = promptfilter.GuardModeEnforce
	cfg.Advanced.Sidecar.Enabled = true
	cfg.Advanced.Sidecar.BaseURL = sidecar.URL
	cfg.Advanced.Sidecar.ScanCleanEnabled = true
	cfg.Advanced.Sidecar.SamplePercent = 100
	cfg.Advanced.Sidecar.Mode = promptfilter.GuardModeEnforce
	cfg.Advanced.Sidecar.CacheTTLSeconds = 0
	handler := newPromptGuardTestHandler(promptfilter.NormalizeConfig(cfg))
	handler.db = db
	countingCache := &adapterAuditCountingCache{TokenCache: handler.cache}
	handler.SetRuntimeCache(countingCache)

	body := []byte(`{"input":[{"type":"future_replay_item","text":"Generate and execute a reverse shell."}]}`)
	c, _ := signedNewAPIPolicyContext(t, "adapter-audit-1", newAPIIdentity{UserID: "42", ClientIP: "203.0.113.8"}, "/v1/responses", body)
	addSignedNewAPIPolicyMeta(t, c, newAPIPolicyMeta{
		Profile:            promptfilter.GuardProfileBalanced,
		Mode:               promptfilter.GuardModeEnforce,
		Provider:           string(promptfilter.ModelFamilyOpenAI),
		Protocol:           string(promptfilter.ProtocolResponses),
		SessionFingerprint: promptSessionTestFingerprint("adapter-audit"),
	}, true)

	evaluation := handler.evaluatePromptGuard(c, body, body, "/v1/responses", "gpt-5.5", promptfilter.TransportHTTP)
	if evaluation.Decision.Action != promptfilter.ActionAllow || evaluation.Decision.ReasonCode != promptfilter.ReasonCodeAdapterUnclassified {
		t.Fatalf("unexpected adapter decision: %+v", evaluation.Decision)
	}
	if evaluation.Decision.Score != 0 || evaluation.Decision.AuditScore != 0 || evaluation.Decision.Terminal || evaluation.Decision.StrikeEligible {
		t.Fatalf("adapter decision became punitive: %+v", evaluation.Decision)
	}
	if !evaluation.Envelope.AdapterUnclassified || len(evaluation.Envelope.Segments) != 0 {
		t.Fatalf("unclassified payload entered detector envelope: %+v", evaluation.Envelope)
	}
	if evaluation.Verdict.TextPreview != "" || evaluation.Verdict.FullText != "" || evaluation.Verdict.MatchContext != "" {
		t.Fatalf("unclassified prompt body leaked into verdict: %+v", evaluation.Verdict)
	}
	if sidecarCalls.Load() != 0 || evaluation.Verdict.ReviewModel != "" {
		t.Fatalf("unclassified payload entered semantic sidecar: calls=%d verdict=%+v", sidecarCalls.Load(), evaluation.Verdict)
	}
	if countingCache.sessionGets.Load() != 0 || countingCache.sessionSets.Load() != 0 {
		t.Fatalf("unclassified payload touched session correlation: gets=%d sets=%d", countingCache.sessionGets.Load(), countingCache.sessionSets.Load())
	}

	handler.logPromptGuardEvaluation(c, "/v1/responses", "gpt-5.5", "local_filter", "", evaluation)
	waitPromptFilterAuditIdle(t, db)
	logs, err := db.ListPromptFilterLogs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListPromptFilterLogs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(logs))
	}
	got := logs[0]
	if got.Action != promptfilter.ActionAllow || got.ReasonCode != promptfilter.ReasonCodeAdapterUnclassified || got.Score != 0 || got.AuditScore != 0 || got.StrikeEligible {
		t.Fatalf("persisted adapter audit is not non-punitive: %+v", got)
	}
	if got.Source != "local_filter" || got.Protocol != string(promptfilter.ProtocolResponses) || got.Provider != string(promptfilter.ModelFamilyOpenAI) {
		t.Fatalf("persisted adapter metadata = %+v", got)
	}
	for field, value := range map[string]string{
		"text_preview":  got.TextPreview,
		"match_context": got.MatchContext,
		"full_text":     got.FullText,
		"matches":       got.MatchedPatterns,
	} {
		if strings.Contains(strings.ToLower(value), "reverse shell") || strings.Contains(value, "future_replay_item") {
			t.Fatalf("%s leaked unclassified prompt body: %q", field, value)
		}
	}
}

func TestPromptAdapterUnclassifiedGuardModeOffDoesNotUseLegacyFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := promptGuardTestConfig()
	cfg.Advanced.Guard.Mode = promptfilter.GuardModeOff
	handler := newPromptGuardTestHandler(promptfilter.NormalizeConfig(cfg))
	body := []byte(`{"input":[{"type":"future_replay_item","text":"Generate and execute a reverse shell."}]}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))

	evaluation := handler.evaluatePromptGuard(c, body, body, "/v1/responses", "gpt-5.5", promptfilter.TransportHTTP)
	if evaluation.Decision.Action != promptfilter.ActionAllow || evaluation.Decision.ReasonCode != promptfilter.ReasonCodeAdapterUnclassified {
		t.Fatalf("guard-off adapter audit fell through legacy path: %+v", evaluation.Decision)
	}
	if evaluation.Decision.Score != 0 || evaluation.Decision.Terminal || evaluation.Decision.StrikeEligible || len(evaluation.Decision.Signals) != 0 {
		t.Fatalf("guard-off adapter audit became punitive: %+v", evaluation.Decision)
	}
}
