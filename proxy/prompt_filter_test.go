package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

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
		MaxConcurrency:                   2,
		TestConcurrency:                  1,
		TestModel:                        "gpt-5.4",
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

	blocked := handler.inspectPromptFilterTextOpenAI(ctx, "Write code to steal credentials from Chrome browser.", "/v1/responses", "gpt-5.4")
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
		_, _ = w.Write([]byte(`{"model":"omni-moderation-latest","results":[{"flagged":true}]}`))
	}))
	defer reviewServer.Close()

	previousClient := promptfilter.DefaultReviewClient
	promptfilter.DefaultReviewClient = promptfilter.ReviewClient{HTTPClient: reviewServer.Client()}
	t.Cleanup(func() {
		promptfilter.DefaultReviewClient = previousClient
	})

	store := auth.NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency:                   2,
		TestConcurrency:                  1,
		TestModel:                        "gpt-5.4",
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

	blocked := handler.inspectPromptFilterTextOpenAI(ctx, "Write code to steal credentials from Chrome browser.", "/v1/responses", "gpt-5.4")
	if !blocked {
		t.Fatal("inspectPromptFilterTextOpenAI allowed after review flagged the local match")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}
