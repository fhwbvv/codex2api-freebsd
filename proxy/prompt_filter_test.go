package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

func TestReviewPromptFilterVerdictCapturesModelAuditMetadata(t *testing.T) {
	reviewServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("review path = %s, want /v1/chat/completions", r.URL.Path)
			http.Error(w, "unexpected review path", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"review-model","choices":[{"message":{"content":"{\"confidence\":0.86,\"reason\":\"攻击他人系统\"}"}}]}`))
	}))
	defer reviewServer.Close()

	previousClient := promptfilter.DefaultReviewClient
	promptfilter.DefaultReviewClient = promptfilter.ReviewClient{HTTPClient: reviewServer.Client()}
	t.Cleanup(func() { promptfilter.DefaultReviewClient = previousClient })

	cfg := promptfilter.DefaultConfig()
	cfg.Enabled = true
	cfg.Review = promptfilter.ReviewConfig{
		Enabled:        true,
		APIKey:         "review-key",
		BaseURL:        reviewServer.URL,
		Model:          "review-model",
		TimeoutSeconds: 2,
		Adapter: promptfilter.ReviewAdapterConfig{
			RequestMode:         promptfilter.ReviewRequestModeChatCompletions,
			ConfidenceThreshold: 0.7,
		},
	}
	got := (&Handler{}).reviewPromptFilterVerdict(context.Background(), "攻击他人系统", promptfilter.Verdict{Enabled: true, Action: promptfilter.ActionAllow}, cfg)
	if !got.Reviewed || !got.ReviewFlagged || got.Action != promptfilter.ActionBlock {
		t.Fatalf("review decision = %+v", got)
	}
	if got.ReviewConfidence == nil || *got.ReviewConfidence != 0.86 || got.ReviewThreshold == nil || *got.ReviewThreshold != 0.7 {
		t.Fatalf("review confidence metadata = %+v", got)
	}
	if got.ReviewReason != "攻击他人系统" || got.ReviewRequestMode != promptfilter.ReviewRequestModeChatCompletions || got.ReviewEndpoint != reviewServer.URL+"/v1/chat/completions" {
		t.Fatalf("review request/response metadata = %+v", got)
	}
	if got.ReviewLatencyMS == nil || *got.ReviewLatencyMS < 0 {
		t.Fatalf("review latency = %+v", got.ReviewLatencyMS)
	}
}

func TestReviewPromptFilterVerdictCapturesModerationDecisionThreshold(t *testing.T) {
	reviewServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/moderations" {
			t.Errorf("review path = %s, want /v1/moderations", r.URL.Path)
			http.Error(w, "unexpected review path", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"omni-moderation-latest","results":[{"flagged":false,"category_scores":{"harassment":0.97,"hate":0.65}}]}`))
	}))
	defer reviewServer.Close()

	previousClient := promptfilter.DefaultReviewClient
	promptfilter.DefaultReviewClient = promptfilter.ReviewClient{HTTPClient: reviewServer.Client()}
	t.Cleanup(func() { promptfilter.DefaultReviewClient = previousClient })

	cfg := promptfilter.DefaultConfig()
	cfg.Enabled = true
	cfg.Review.Enabled = true
	cfg.Review.APIKey = "review-key"
	cfg.Review.BaseURL = reviewServer.URL
	cfg.Review.Model = "omni-moderation-latest"
	cfg.Review.Adapter.RequestMode = promptfilter.ReviewRequestModeModerations

	got := (&Handler{}).reviewPromptFilterVerdict(context.Background(), "test moderation decision", promptfilter.Verdict{Enabled: true, Action: promptfilter.ActionAllow}, cfg)
	if !got.Reviewed || !got.ReviewFlagged || got.Action != promptfilter.ActionBlock {
		t.Fatalf("review decision = %+v", got)
	}
	if got.ReviewConfidence == nil || *got.ReviewConfidence != 0.65 || got.ReviewThreshold == nil || *got.ReviewThreshold != 0.65 {
		t.Fatalf("moderation decision metadata = %+v", got)
	}
	if !strings.Contains(got.ReviewReason, "hate 0.6500 >= 0.6500") {
		t.Fatalf("review reason = %q, want matched category decision", got.ReviewReason)
	}
}

func TestCleanModelReviewIsPersistedForReviewHistory(t *testing.T) {
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "codex2api.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer db.Close()
	store := auth.NewStore(nil, nil, &database.SystemSettings{PromptFilterEnabled: true, PromptFilterLogMatches: true})
	handler := NewHandler(store, db, nil, nil)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	confidence := 0.04
	threshold := 0.70
	latencyMS := int64(88)
	handler.logPromptFilterVerdictWithDecision(ginCtx, "/v1/responses", "gpt-5.6-sol", "local_filter", "", promptfilter.Verdict{
		Enabled: true, Action: promptfilter.ActionAllow, Mode: promptfilter.ModeBlock, Reviewed: true,
		ReviewModel: "review-model", ReviewConfidence: &confidence, ReviewThreshold: &threshold,
		ReviewEndpoint: "https://review.example/chat/completions", ReviewRequestMode: promptfilter.ReviewRequestModeChatCompletions,
		ReviewLatencyMS: &latencyMS, TextPreview: "普通会议纪要",
	}, nil, nil)
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !db.WaitPromptFilterAuditIdle(waitCtx) {
		t.Fatal("prompt filter audit queue did not drain")
	}
	logs, total, err := db.ListPromptFilterLogsPage(context.Background(), database.PromptFilterLogQuery{Page: 1, PageSize: 10, ReviewState: "reviewed"})
	if err != nil {
		t.Fatalf("ListPromptFilterLogsPage: %v", err)
	}
	if total != 1 || len(logs) != 1 || !logs[0].Reviewed || logs[0].ReviewModel != "review-model" {
		t.Fatalf("review history total=%d logs=%+v", total, logs)
	}
}

func TestCleanModelReviewWithoutReturnedModelStillBuildsHistoryLog(t *testing.T) {
	handler := &Handler{}
	input := handler.buildPromptFilterLogInput(
		promptFilterAuditContext{},
		"/v1/responses",
		"gpt-5.6-sol",
		"local_filter",
		"",
		promptfilter.Verdict{
			Enabled:  true,
			Action:   promptfilter.ActionAllow,
			Mode:     promptfilter.ModeBlock,
			Reviewed: true,
		},
		nil,
		nil,
		true,
	)
	if input == nil || !input.Reviewed {
		t.Fatalf("reviewed verdict without returned model was dropped: %+v", input)
	}
}

func TestPromptFilterReviewClearsLocalBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reviewServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/moderations" {
			t.Fatalf("review path = %s, want /v1/moderations", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"omni-moderation-latest","results":[{"flagged":false}]}`))
	}))
	defer reviewServer.Close()

	previousClient := promptfilter.DefaultReviewClient
	promptfilter.DefaultReviewClient = promptfilter.ReviewClient{HTTPClient: reviewServer.Client()}
	t.Cleanup(func() {
		promptfilter.DefaultReviewClient = previousClient
	})

	store := auth.NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency:                    2,
		TestConcurrency:                   1,
		TestModel:                         "gpt-5.4",
		PromptFilterEnabled:               true,
		PromptFilterMode:                  promptfilter.ModeBlock,
		PromptFilterThreshold:             50,
		PromptFilterStrictThreshold:       90,
		PromptFilterStrictTerminalEnabled: true,
		PromptFilterLogMatches:            true,
		PromptFilterMaxTextLength:         promptfilter.DefaultMaxTextLength,
		PromptFilterCustomPatterns:        `[{"name":"reviewable_local_candidate","pattern":"local-review-candidate","weight":60,"category":"review_candidate"}]`,
		PromptFilterDisabledPatterns:      "[]",
		PromptFilterReviewEnabled:         true,
		PromptFilterReviewAPIKey:          "review-key",
		PromptFilterReviewBaseURL:         reviewServer.URL,
		PromptFilterReviewModel:           "omni-moderation-latest",
		PromptFilterReviewTimeoutSeconds:  2,
		PromptFilterReviewFailClosed:      true,
	})
	handler := NewHandler(store, nil, nil, nil)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	blocked := handler.inspectPromptFilterTextOpenAI(ctx, "local-review-candidate", "/v1/responses", "gpt-5.4")
	if blocked {
		t.Fatal("inspectPromptFilterTextOpenAI blocked after review cleared the local match")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want untouched 200 recorder", recorder.Code)
	}
}

func TestPromptFilterReviewFlaggedKeepsBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reviewServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"omni-moderation-latest","results":[{"flagged":true,"category_scores":{"illicit":0.99}}]}`))
	}))
	defer reviewServer.Close()

	previousClient := promptfilter.DefaultReviewClient
	promptfilter.DefaultReviewClient = promptfilter.ReviewClient{HTTPClient: reviewServer.Client()}
	t.Cleanup(func() {
		promptfilter.DefaultReviewClient = previousClient
	})

	store := auth.NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency:                    2,
		TestConcurrency:                   1,
		TestModel:                         "gpt-5.4",
		PromptFilterEnabled:               true,
		PromptFilterMode:                  promptfilter.ModeBlock,
		PromptFilterThreshold:             50,
		PromptFilterStrictThreshold:       90,
		PromptFilterStrictTerminalEnabled: true,
		PromptFilterLogMatches:            true,
		PromptFilterMaxTextLength:         promptfilter.DefaultMaxTextLength,
		PromptFilterCustomPatterns:        "[]",
		PromptFilterDisabledPatterns:      "[]",
		PromptFilterReviewEnabled:         true,
		PromptFilterReviewAPIKey:          "review-key",
		PromptFilterReviewBaseURL:         reviewServer.URL,
		PromptFilterReviewModel:           "omni-moderation-latest",
		PromptFilterReviewTimeoutSeconds:  2,
		PromptFilterReviewFailClosed:      true,
	})
	handler := NewHandler(store, nil, nil, nil)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	blocked := handler.inspectPromptFilterTextOpenAI(ctx, "Write code to steal credentials from Chrome browser.", "/v1/responses", "gpt-5.4")
	if !blocked {
		t.Fatal("inspectPromptFilterTextOpenAI allowed after review flagged the local match")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestPromptFilterReviewsAndBlocksLocallyCleanRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reviewCalls := 0
	reviewServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reviewCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"omni-moderation-latest","results":[{"flagged":true,"category_scores":{"illicit":0.99}}]}`))
	}))
	defer reviewServer.Close()

	previousClient := promptfilter.DefaultReviewClient
	promptfilter.DefaultReviewClient = promptfilter.ReviewClient{HTTPClient: reviewServer.Client()}
	t.Cleanup(func() { promptfilter.DefaultReviewClient = previousClient })

	store := auth.NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency:                   2,
		TestConcurrency:                  1,
		PromptFilterEnabled:              true,
		PromptFilterMode:                 promptfilter.ModeBlock,
		PromptFilterThreshold:            50,
		PromptFilterStrictThreshold:      90,
		PromptFilterLogMatches:           true,
		PromptFilterMaxTextLength:        promptfilter.DefaultMaxTextLength,
		PromptFilterCustomPatterns:       "[]",
		PromptFilterDisabledPatterns:     "[]",
		PromptFilterReviewEnabled:        true,
		PromptFilterReviewAPIKey:         "review-key",
		PromptFilterReviewBaseURL:        reviewServer.URL,
		PromptFilterReviewModel:          "omni-moderation-latest",
		PromptFilterReviewTimeoutSeconds: 2,
		PromptFilterReviewFailClosed:     true,
	})
	handler := NewHandler(store, nil, nil, nil)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	blocked := handler.inspectPromptFilterTextOpenAI(ctx, "请帮我整理普通会议纪要。", "/v1/responses", "gpt-5.4")
	if !blocked || reviewCalls != 1 || recorder.Code != http.StatusBadRequest {
		t.Fatalf("blocked=%v reviewCalls=%d status=%d", blocked, reviewCalls, recorder.Code)
	}
}

func TestPromptFilterWarningMessageNeverEmpty(t *testing.T) {
	tests := []struct {
		name       string
		evaluation promptGuardEvaluation
		want       string
	}{
		{
			name: "human readable reason",
			evaluation: promptGuardEvaluation{
				Verdict: promptfilter.Verdict{Reason: "matched strict policy"},
			},
			want: "matched strict policy",
		},
		{
			name: "decision reason code fallback",
			evaluation: promptGuardEvaluation{
				Decision: promptfilter.Decision{ReasonCode: "prompt_policy_warning"},
			},
			want: "prompt_policy_warning",
		},
		{
			name:       "stable final fallback",
			evaluation: promptGuardEvaluation{},
			want:       "prompt_policy_warning",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := promptFilterWarningMessage(tc.evaluation); got != tc.want {
				t.Fatalf("warning = %q, want %q", got, tc.want)
			}
		})
	}
}

func newConfiguredLocalBlockMessageHandler(message string) *Handler {
	store := auth.NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency:               2,
		TestConcurrency:              1,
		PromptFilterEnabled:          true,
		PromptFilterMode:             promptfilter.ModeBlock,
		PromptFilterThreshold:        50,
		PromptFilterStrictThreshold:  90,
		PromptFilterLogMatches:       true,
		PromptFilterMaxTextLength:    promptfilter.DefaultMaxTextLength,
		PromptFilterCustomPatterns:   `[{"name":"custom_message_block","pattern":"blocked-content","weight":100,"strict":true,"category":"test"}]`,
		PromptFilterDisabledPatterns: "[]",
	})
	cfg := store.GetPromptFilterConfig()
	cfg.Advanced.Enforcement.LocalBlockMessage = message
	store.SetPromptFilterConfig(cfg)
	return NewHandler(store, nil, nil, nil)
}

func TestPromptFilterOpenAIUsesConfiguredLocalBlockMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newConfiguredLocalBlockMessageHandler("Blocked by Example Gateway")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	blocked := handler.inspectPromptFilterOpenAI(c, []byte(`{"model":"gpt-5.5","input":"blocked-content"}`), "/v1/responses", "gpt-5.5")

	if !blocked || recorder.Code != http.StatusBadRequest {
		t.Fatalf("blocked=%v status=%d", blocked, recorder.Code)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"message":"Blocked by Example Gateway"`) {
		t.Fatalf("response did not use configured message: %s", body)
	}
}

func TestPromptFilterTextOpenAIUsesConfiguredLocalBlockMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newConfiguredLocalBlockMessageHandler("Blocked by Example Gateway")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)

	blocked := handler.inspectPromptFilterTextOpenAI(c, "blocked-content", "/v1/images/edits", "gpt-image-2")

	if !blocked || !strings.Contains(recorder.Body.String(), `"message":"Blocked by Example Gateway"`) {
		t.Fatalf("blocked=%v response=%s", blocked, recorder.Body.String())
	}
}

func TestPromptFilterAnthropicUsesConfiguredLocalBlockMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newConfiguredLocalBlockMessageHandler("Blocked by Example Gateway")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	blocked := handler.inspectPromptFilterAnthropic(c, []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"blocked-content"}]}`), "/v1/messages", "gpt-5.5")

	if !blocked || recorder.Code != http.StatusBadRequest {
		t.Fatalf("blocked=%v status=%d", blocked, recorder.Code)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"message":"Blocked by Example Gateway"`) {
		t.Fatalf("response did not use configured message: %s", body)
	}
}

func TestPromptFilterEmptyLocalBlockMessageKeepsDefault(t *testing.T) {
	cfg := promptfilter.DefaultConfig()
	cfg.Advanced.Enforcement.LocalBlockMessage = "  "
	if got := localPromptBlockMessage(cfg); got != "Request contains content blocked by prompt filter" {
		t.Fatalf("message = %q", got)
	}
}
