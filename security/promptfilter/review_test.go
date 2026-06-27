package promptfilter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReviewTextAllowsWhenNotFlagged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "omni-moderation-latest",
			"results": []map[string]any{
				{"flagged": false},
			},
		})
	}))
	defer server.Close()

	client := ReviewClient{HTTPClient: server.Client()}
	flagged, model, err := client.ReviewText(context.Background(), "hello", ReviewConfig{
		Enabled:        true,
		APIKey:         "test-key",
		BaseURL:        server.URL,
		Model:          "omni-moderation-latest",
		TimeoutSeconds: 2,
	})
	if err != nil {
		t.Fatalf("ReviewText returned error: %v", err)
	}
	if flagged {
		t.Fatal("flagged = true, want false")
	}
	if model != "omni-moderation-latest" {
		t.Fatalf("model = %q, want omni-moderation-latest", model)
	}
}

func TestReviewTextReturnsErrorWhenResultsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"omni-moderation-latest","results":[]}`))
	}))
	defer server.Close()

	client := ReviewClient{HTTPClient: server.Client()}
	_, _, err := client.ReviewText(context.Background(), "hello", ReviewConfig{
		Enabled:        true,
		APIKey:         "test-key",
		BaseURL:        server.URL,
		Model:          "omni-moderation-latest",
		TimeoutSeconds: 2,
	})
	if err == nil {
		t.Fatal("ReviewText returned nil error, want missing results error")
	}
}

func TestApplyReviewResultClearsLocalBlockWhenCleared(t *testing.T) {
	verdict := Verdict{Action: ActionBlock, Reason: "local block"}
	got := ApplyReviewResult(verdict, false, "omni-moderation-latest", nil, ReviewConfig{FailClosed: true, Model: "omni-moderation-latest"})
	if got.Action != ActionAllow {
		t.Fatalf("action = %s, want allow", got.Action)
	}
	if !got.Reviewed || got.ReviewFlagged {
		t.Fatalf("review metadata = %+v, want reviewed and not flagged", got)
	}
}

func TestApplyReviewResultBlocksWhenReviewFailsClosed(t *testing.T) {
	verdict := Verdict{Action: ActionAllow}
	got := ApplyReviewResult(verdict, false, "omni-moderation-latest", context.DeadlineExceeded, ReviewConfig{FailClosed: true, Model: "omni-moderation-latest"})
	if got.Action != ActionBlock {
		t.Fatalf("action = %s, want block", got.Action)
	}
	if got.ReviewError == "" {
		t.Fatal("expected review_error to be recorded")
	}
}

func TestApplyReviewResultAllowsWhenReviewFailsOpen(t *testing.T) {
	verdict := Verdict{Action: ActionBlock}
	got := ApplyReviewResult(verdict, false, "omni-moderation-latest", context.DeadlineExceeded, ReviewConfig{FailClosed: false, Model: "omni-moderation-latest"})
	if got.Action != ActionAllow {
		t.Fatalf("action = %s, want allow", got.Action)
	}
	if got.ReviewError == "" {
		t.Fatal("expected review_error to be recorded")
	}
}
