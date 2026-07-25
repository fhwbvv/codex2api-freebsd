package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codex2api/cache"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

func TestPromptRiskNeverBlocksRepeatedRequests(t *testing.T) {
	h := &Handler{cache: cache.NewMemory(1)}
	cfg := promptfilter.DefaultConfig()
	cfg.Enabled = true
	cfg.Advanced.Risk = promptfilter.RiskConfig{Enabled: true, WindowSeconds: 600, BlockThreshold: 100, UserWeightPercent: 100}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(contextAPIKeyID, int64(42))
	v := promptfilter.Verdict{Enabled: true, Action: promptfilter.ActionWarn, Score: 60, RawScore: 60, Threshold: 50, SensitiveIntent: true, TerminalCategoryHit: true}
	if first := h.applyPromptRisk(c, v, cfg); first.Action == promptfilter.ActionBlock {
		t.Fatalf("first request blocked: %+v", first)
	} else if first.Score != 60 {
		t.Fatalf("cumulative risk changed the current prompt score: %+v", first)
	}
	if second := h.applyPromptRisk(c, v, cfg); second.Action == promptfilter.ActionBlock {
		t.Fatalf("cumulative risk blocked the current request: %+v", second)
	} else if second.Score != 60 || second.RiskScore < cfg.Advanced.Risk.BlockThreshold {
		t.Fatalf("risk score was not separated from prompt score: %+v", second)
	}
}

func TestPromptRiskDoesNotPersistGenericSensitiveKeywordScore(t *testing.T) {
	h := &Handler{cache: cache.NewMemory(1)}
	cfg := promptfilter.DefaultConfig()
	cfg.Enabled = true
	cfg.Advanced.Risk = promptfilter.RiskConfig{Enabled: true, WindowSeconds: 600, BlockThreshold: 100, UserWeightPercent: 100}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(contextAPIKeyID, int64(42))

	v := promptfilter.Verdict{Enabled: true, Action: promptfilter.ActionWarn, Score: 80, RawScore: 80, Threshold: 50, SensitiveIntent: true}
	for range 5 {
		got := h.applyPromptRisk(c, v, cfg)
		if got.RiskScore != 0 || got.Action == promptfilter.ActionBlock {
			t.Fatalf("generic score entered cumulative enforcement: %+v", got)
		}
	}
}

func TestPromptRiskCannotReblockReviewClearedPrompt(t *testing.T) {
	h := &Handler{cache: cache.NewMemory(1)}
	cfg := promptfilter.DefaultConfig()
	cfg.Enabled = true
	cfg.Advanced.Risk = promptfilter.RiskConfig{Enabled: true, WindowSeconds: 600, BlockThreshold: 100, UserWeightPercent: 100}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(contextAPIKeyID, int64(42))

	sensitive := promptfilter.Verdict{Enabled: true, Action: promptfilter.ActionBlock, Score: 100, RawScore: 100, Threshold: 50, SensitiveIntent: true, Reviewed: true, ReviewFlagged: true}
	_ = h.applyPromptRisk(c, sensitive, cfg)
	cleared := sensitive
	cleared.Action = promptfilter.ActionAllow
	cleared.ReviewFlagged = false
	got := h.applyPromptRisk(c, cleared, cfg)
	if got.Action != promptfilter.ActionAllow || got.RiskScore != 0 || got.Score != 100 {
		t.Fatalf("review-cleared prompt was reblocked by historical risk: %+v", got)
	}
}

func TestSidecarCanEscalateButCannotDowngrade(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"action":"allow","score":5,"reason":"classifier allow"}`))
	}))
	defer server.Close()
	cfg := promptfilter.DefaultConfig()
	cfg.Advanced.Sidecar = promptfilter.SidecarConfig{Enabled: true, BaseURL: server.URL, TimeoutSeconds: 2}
	blocked := promptfilter.Verdict{Action: promptfilter.ActionBlock, Score: 80}
	got := applyPromptSidecar(t.Context(), "test", blocked, cfg)
	if got.Action != promptfilter.ActionBlock {
		t.Fatalf("sidecar downgraded block: %+v", got)
	}
}
