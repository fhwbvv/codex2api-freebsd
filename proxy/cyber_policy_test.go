package proxy

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

// TestUpstreamCyberPolicyCodeDetectsResponseFailed 覆盖 #258：cyber_policy 封禁在
// 流式响应里以 response.failed (HTTP 200) 事件下发，必须能被
// upstreamCyberPolicyCode(responseFailedErrorBody(payload)) 识别。
func TestUpstreamCyberPolicyCodeDetectsResponseFailed(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "response.error.code",
			payload: `{"type":"response.failed","response":{"error":{"code":"cyber_policy","message":"blocked"}}}`,
			want:    "cyber_policy",
		},
		{
			name:    "response.status_details.error.code",
			payload: `{"type":"response.failed","response":{"status_details":{"error":{"code":"cyber_policy"}}}}`,
			want:    "cyber_policy",
		},
		{
			name:    "codex_error_info under response.error",
			payload: `{"type":"response.failed","response":{"error":{"codex_error_info":"cyber_policy"}}}`,
			want:    "cyber_policy",
		},
		{
			name:    "substring fallback (cyber security risk)",
			payload: `{"type":"response.failed","response":{"error":{"message":"detected cyber security risk in prompt"}}}`,
			want:    "cyber_policy",
		},
		{
			name:    "unrelated failure is not cyber_policy",
			payload: `{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded"}}}`,
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := upstreamCyberPolicyCode(responseFailedErrorBody([]byte(tc.payload)))
			if got != tc.want {
				t.Fatalf("upstreamCyberPolicyCode = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLogUpstreamCyberPolicyRecordsStreamingFailure 端到端验证：流式 response.failed
// 里的 cyber_policy 会被写入 prompt_filter_logs，且记录完整内容（#258 + #259）。
func TestLogUpstreamCyberPolicyRecordsStreamingFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := database.New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("database.New(sqlite) error: %v", err)
	}
	defer db.Close()

	store := auth.NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency:               2,
		PromptFilterMode:             promptfilter.ModeBlock,
		PromptFilterThreshold:        50,
		PromptFilterMaxTextLength:    promptfilter.DefaultMaxTextLength,
		PromptFilterCustomPatterns:   "[]",
		PromptFilterDisabledPatterns: "[]",
	})
	handler := NewHandler(store, db, nil, nil)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"cyber_policy","message":"cyber security risk detected"}}}`)
	handler.logUpstreamCyberPolicy(ctx, "/v1/responses", "gpt-5.4", responseFailedErrorBody(payload))

	logs, err := db.ListPromptFilterLogs(ctx.Request.Context(), 10)
	if err != nil {
		t.Fatalf("ListPromptFilterLogs error: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("prompt_filter_logs rows = %d, want 1", len(logs))
	}
	got := logs[0]
	if got.Source != "upstream_cyber_policy" {
		t.Fatalf("source = %q, want upstream_cyber_policy", got.Source)
	}
	if got.ErrorCode != "cyber_policy" {
		t.Fatalf("error_code = %q, want cyber_policy", got.ErrorCode)
	}
	if got.Action != string(promptfilter.ActionBlock) {
		t.Fatalf("action = %q, want %q", got.Action, promptfilter.ActionBlock)
	}
	if !strings.Contains(got.FullText, "cyber_policy") {
		t.Fatalf("full_text = %q, want it to contain the upstream error body", got.FullText)
	}
}
