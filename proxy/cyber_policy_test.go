package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/codex2api/auth"
	"github.com/codex2api/database"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
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
			name:    "message alone is not explicit CYB",
			payload: `{"type":"response.failed","response":{"error":{"message":"detected cyber security risk in prompt"}}}`,
			want:    "",
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

func TestExplicitCyberPolicyReturnsChineseBanWarningOnlyForCYB(t *testing.T) {
	cybPayload := []byte(`{"type":"response.failed","response":{"error":{"code":"cyber_policy","message":"This content was flagged for possible cybersecurity risk. If this seems wrong, try rephrasing your request"}}}`)
	if !isExplicitUpstreamCyberPolicy(cybPayload) {
		t.Fatal("explicit cyber_policy response was not recognized")
	}
	outcome := classifyResponseFailedOutcome(cybPayload)
	if outcome.failureMessage != upstreamCyberPolicyUserMessage {
		t.Fatalf("CYB stream message = %q, want %q", outcome.failureMessage, upstreamCyberPolicyUserMessage)
	}
	wsErr := responsesWSUpstreamAPIError(http.StatusBadRequest, responseFailedErrorBody(cybPayload))
	if wsErr.Message != upstreamCyberPolicyUserMessage {
		t.Fatalf("CYB websocket message = %q, want %q", wsErr.Message, upstreamCyberPolicyUserMessage)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	(&Handler{}).sendUpstreamError(ctx, http.StatusBadRequest, responseFailedErrorBody(cybPayload))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("CYB HTTP status = %d, want 400", recorder.Code)
	}
	if got := gjson.GetBytes(recorder.Body.Bytes(), "error.message").String(); got != upstreamCyberPolicyUserMessage {
		t.Fatalf("CYB HTTP message = %q, want %q", got, upstreamCyberPolicyUserMessage)
	}
	if got := gjson.GetBytes(recorder.Body.Bytes(), "error.code").String(); got != newAPIUpstreamCyberPolicyReasonCode {
		t.Fatalf("CYB HTTP error code = %q", got)
	}

	messageOnly := []byte(`{"error":{"message":"This content was flagged for possible cybersecurity risk"}}`)
	if isExplicitUpstreamCyberPolicy(messageOnly) {
		t.Fatal("message-only upstream error was treated as explicit CYB")
	}
	ordinary := responsesWSUpstreamAPIError(http.StatusBadRequest, messageOnly)
	if ordinary.Message == upstreamCyberPolicyUserMessage {
		t.Fatal("ordinary upstream error received the CYB ban warning")
	}
}

func assertCyberUsageIncidentLinks(t *testing.T, db *database.DB, endpoint string) {
	t.Helper()
	db.FlushUsageLogs()
	ctx := context.Background()
	incidents, _, err := db.ListPromptPolicyIncidentsPage(ctx, database.PromptPolicyIncidentQuery{Page: 1, PageSize: 500, Endpoint: endpoint})
	if err != nil {
		t.Fatalf("ListPromptPolicyIncidentsPage: %v", err)
	}
	byID := make(map[string]*database.PromptPolicyIncident, len(incidents))
	for _, incident := range incidents {
		byID[incident.IncidentID] = incident
	}
	page, err := db.ListUsageLogsByTimeRangePaged(ctx, database.UsageLogFilter{
		Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute), Page: 1, PageSize: 500,
		Endpoint: endpoint, ErrorOnly: true, IncludeCanceled: true,
	})
	if err != nil {
		t.Fatalf("ListUsageLogsByTimeRangePaged: %v", err)
	}
	linked := 0
	for _, usage := range page.Logs {
		if usage.UpstreamErrorKind != "cyber_policy" {
			continue
		}
		linked++
		incident := byID[usage.PromptPolicyIncidentID]
		if usage.PromptPolicyIncidentID == "" || incident == nil {
			t.Fatalf("CY usage missing exact incident link: %#v incidents=%#v", usage, incidents)
		}
		if incident.AccountID != usage.AccountID || incident.AttemptIndex != usage.AttemptIndex {
			t.Fatalf("usage/incident attempt mismatch: usage=%#v incident=%#v", usage, incident)
		}
	}
	if linked == 0 {
		t.Fatalf("no CY usage rows found for %s: %#v", endpoint, page.Logs)
	}
}

func TestPromptPolicyIncidentProtocolMatrixKeepsExactUsageLinks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "cyber-protocol-matrix.db"))
	if err != nil {
		t.Fatalf("database.New(sqlite) error: %v", err)
	}
	defer db.Close()
	store := auth.NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency: 2, PromptFilterEnabled: true, PromptFilterMode: promptfilter.ModeBlock,
		PromptFilterThreshold: 50, PromptFilterMaxTextLength: promptfilter.DefaultMaxTextLength,
		PromptFilterCustomPatterns: "[]", PromptFilterDisabledPatterns: "[]",
	})
	handler := NewHandler(store, db, nil, nil)
	cases := []struct {
		name      string
		endpoint  string
		transport string
		protocol  promptfilter.Protocol
	}{
		{"responses_http", "/v1/responses", "http", promptfilter.ProtocolResponses},
		{"responses_sse", "/v1/responses", "sse", promptfilter.ProtocolResponses},
		{"responses_websocket", "/v1/responses", "websocket", promptfilter.ProtocolResponses},
		{"responses_compact", "/v1/responses/compact", "http", promptfilter.ProtocolResponses},
		{"chat_http", "/v1/chat/completions", "http", promptfilter.ProtocolChat},
		{"chat_sse", "/v1/chat/completions", "sse", promptfilter.ProtocolChat},
		{"chat_websocket", "/v1/chat/completions", "websocket", promptfilter.ProtocolChat},
		{"messages_http", "/v1/messages", "http", promptfilter.ProtocolMessages},
		{"messages_sse", "/v1/messages", "sse", promptfilter.ProtocolMessages},
		{"images_generations_http", "/v1/images/generations", "http", promptfilter.ProtocolImages},
		{"images_generations_sse", "/v1/images/generations", "sse", promptfilter.ProtocolImages},
		{"images_edits_http", "/v1/images/edits", "http", promptfilter.ProtocolImages},
		{"images_edits_sse", "/v1/images/edits", "sse", promptfilter.ProtocolImages},
		{"realtime_websocket", "/v1/realtime", "websocket", promptfilter.ProtocolResponses},
	}
	wantByIncident := make(map[string]struct {
		endpoint, transport, protocol string
	}, len(cases))
	for index, tc := range cases {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, tc.endpoint, nil)
		ctx.Request.Header.Set("X-Request-ID", "matrix-"+tc.name)
		text := "redacted protocol matrix prompt " + tc.name
		handler.capturePromptRuleLearningEvidence(ctx, tc.endpoint, "gpt-5.4", promptGuardEvaluation{
			Envelope: promptfilter.RequestEnvelope{
				Endpoint: tc.endpoint, Protocol: tc.protocol, ModelFamily: promptfilter.ModelFamilyOpenAI,
				Segments: []promptfilter.Segment{{Origin: promptfilter.OriginCurrentUser, Role: "user", Text: text}},
			},
			Decision: promptfilter.Decision{Action: promptfilter.ActionAllow},
			Verdict:  promptfilter.Verdict{Enabled: true, Action: promptfilter.ActionAllow},
		})
		incidentID, accepted := handler.logUpstreamCyberPolicy(ctx, tc.endpoint, "gpt-5.4", []byte(`{"error":{"code":"cyber_policy"}}`), upstreamCyberPolicyAttempt{
			Transport: tc.transport, StatusCode: http.StatusBadRequest, AccountID: int64(index + 1), AttemptIndex: 1,
		})
		if !accepted || incidentID == "" {
			t.Fatalf("%s incident enqueue accepted=%t id=%q", tc.name, accepted, incidentID)
		}
		handler.logUsageForRequest(ctx, &database.UsageLogInput{
			AccountID: int64(index + 1), Endpoint: tc.endpoint, Model: "gpt-5.4", StatusCode: http.StatusBadRequest,
			AttemptIndex: 1, UpstreamErrorKind: "cyber_policy", PromptPolicyIncidentID: incidentID,
		})
		wantByIncident[incidentID] = struct {
			endpoint, transport, protocol string
		}{tc.endpoint, tc.transport, string(tc.protocol)}
	}
	waitPromptFilterAuditIdle(t, db)
	db.FlushUsageLogs()
	page, err := db.ListUsageLogsByTimeRangePaged(context.Background(), database.UsageLogFilter{
		Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Minute), Page: 1, PageSize: 500, ErrorOnly: true, IncludeCanceled: true,
	})
	if err != nil {
		t.Fatalf("ListUsageLogsByTimeRangePaged: %v", err)
	}
	usageByIncident := make(map[string]*database.UsageLog, len(page.Logs))
	for _, usage := range page.Logs {
		if usage.PromptPolicyIncidentID != "" {
			usageByIncident[usage.PromptPolicyIncidentID] = usage
		}
	}
	for incidentID, want := range wantByIncident {
		incident, err := db.GetPromptPolicyIncident(context.Background(), incidentID)
		if err != nil {
			t.Fatalf("GetPromptPolicyIncident(%s): %v", incidentID, err)
		}
		usage := usageByIncident[incidentID]
		if usage == nil || usage.AccountID != incident.AccountID || usage.AttemptIndex != incident.AttemptIndex {
			t.Fatalf("incident/usage exact link mismatch incident=%#v usage=%#v", incident, usage)
		}
		if incident.Endpoint != want.endpoint || incident.Transport != want.transport || incident.Protocol != want.protocol {
			t.Fatalf("protocol matrix context incident=%#v want=%#v", incident, want)
		}
		if !incident.PromptAvailable || incident.LocalComparison != database.PromptPolicyComparisonConfirmedMiss || !incident.LocalMiss {
			t.Fatalf("exact completed/no-hit evidence was not classified as a confirmed miss: %#v", incident)
		}
	}
}

func TestPromptPolicyLocalOutcomeSemantics(t *testing.T) {
	cases := []struct {
		name string
		in   promptRuleLearningEvidence
		want string
	}{
		{"completed_no_hit", promptRuleLearningEvidence{EvaluationState: database.PromptPolicyEvaluationCompleted, Action: promptfilter.ActionAllow}, database.PromptPolicyOutcomeNoHit},
		{"audit_score", promptRuleLearningEvidence{EvaluationState: database.PromptPolicyEvaluationCompleted, Action: promptfilter.ActionAllow, AuditScore: 1}, database.PromptPolicyOutcomeAuditHit},
		{"matched_rule", promptRuleLearningEvidence{EvaluationState: database.PromptPolicyEvaluationCompleted, Action: promptfilter.ActionAllow, Matches: []promptfilter.Match{{Name: "signal"}}}, database.PromptPolicyOutcomeAuditHit},
		{"warn", promptRuleLearningEvidence{EvaluationState: database.PromptPolicyEvaluationCompleted, Action: promptfilter.ActionWarn}, database.PromptPolicyOutcomeWarn},
		{"block", promptRuleLearningEvidence{EvaluationState: database.PromptPolicyEvaluationCompleted, Action: promptfilter.ActionBlock}, database.PromptPolicyOutcomeBlock},
		{"not_run", promptRuleLearningEvidence{EvaluationState: database.PromptPolicyEvaluationNotRun}, database.PromptPolicyOutcomeNoHit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := promptPolicyLocalOutcome(tc.in); got != tc.want {
				t.Fatalf("promptPolicyLocalOutcome() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPromptPolicyIncidentRedactsAndBoundsSeparatedFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "cyber-redaction.db"))
	if err != nil {
		t.Fatalf("database.New(sqlite) error: %v", err)
	}
	defer db.Close()
	store := auth.NewStore(nil, nil, &database.SystemSettings{
		MaxConcurrency: 2, PromptFilterEnabled: true, PromptFilterMode: promptfilter.ModeBlock,
		PromptFilterThreshold: 50, PromptFilterMaxTextLength: promptfilter.DefaultMaxTextLength,
		PromptFilterCustomPatterns: "[]", PromptFilterDisabledPatterns: "[]",
	})
	handler := NewHandler(store, db, nil, nil)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	promptSecret := "prompt-secret-value"
	errorSecret := "error-secret-value"
	text := "Authorization: Bearer " + promptSecret + "\nCookie: sid=" + promptSecret + "\nsk-1234567890 " + strings.Repeat("界", 40000)
	handler.capturePromptRuleLearningEvidence(ctx, "/v1/responses", "gpt-5.4", promptGuardEvaluation{
		Envelope: promptfilter.RequestEnvelope{
			Endpoint: "/v1/responses", Protocol: promptfilter.ProtocolResponses, ModelFamily: promptfilter.ModelFamilyOpenAI,
			Segments: []promptfilter.Segment{{Origin: promptfilter.OriginCurrentUser, Role: "user", Text: text}},
		},
		Decision: promptfilter.Decision{Action: promptfilter.ActionAllow},
		Verdict:  promptfilter.Verdict{Enabled: true, Action: promptfilter.ActionAllow},
	})
	incidentID, accepted := handler.logUpstreamCyberPolicy(ctx, "/v1/responses", "gpt-5.4", []byte(`{"error":{"code":"cyber_policy","message":"api_key=`+errorSecret+` `+strings.Repeat("x", 10000)+`"}}`))
	if !accepted || incidentID == "" {
		t.Fatalf("incident enqueue accepted=%t id=%q", accepted, incidentID)
	}
	waitPromptFilterAuditIdle(t, db)
	incident, err := db.GetPromptPolicyIncident(context.Background(), incidentID)
	if err != nil {
		t.Fatalf("GetPromptPolicyIncident: %v", err)
	}
	if incident.PromptFingerprint == "" || utf8.RuneCountInString(incident.PromptPreview) > 2000 || utf8.RuneCountInString(incident.PromptText) > 32000 || utf8.RuneCountInString(incident.UpstreamError) > 8192 {
		t.Fatalf("incident bounds/fingerprint = %#v", incident)
	}
	for name, value := range map[string]string{"prompt_preview": incident.PromptPreview, "prompt_text": incident.PromptText} {
		if strings.Contains(value, promptSecret) || strings.Contains(value, "sk-1234567890") || !strings.Contains(value, "[REDACTED") {
			t.Fatalf("%s was not redacted: %q", name, value)
		}
		if strings.Contains(value, errorSecret) {
			t.Fatalf("%s contains isolated upstream error data", name)
		}
	}
	if strings.Contains(incident.UpstreamError, errorSecret) || strings.Contains(incident.UpstreamError, promptSecret) || !strings.Contains(incident.UpstreamError, "[REDACTED]") {
		t.Fatalf("upstream error redaction/isolation failed: %q", incident.UpstreamError)
	}
}

func TestPromptPolicyIncidentUsesStableFingerprintWhenPromptUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "cyber-unavailable.db"))
	if err != nil {
		t.Fatalf("database.New(sqlite) error: %v", err)
	}
	defer db.Close()
	handler := NewHandler(auth.NewStore(nil, nil, &database.SystemSettings{PromptFilterEnabled: true}), db, nil, nil)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	incidentID, accepted := handler.logUpstreamCyberPolicy(ctx, "/v1/responses", "gpt-5.6-sol", []byte(`{"error":{"code":"cyber_policy"}}`))
	if !accepted || incidentID == "" {
		t.Fatalf("incident enqueue accepted=%t id=%q", accepted, incidentID)
	}
	waitPromptFilterAuditIdle(t, db)
	incident, err := db.GetPromptPolicyIncident(context.Background(), incidentID)
	if err != nil {
		t.Fatalf("GetPromptPolicyIncident: %v", err)
	}
	want := promptfilter.StableEvidenceFingerprint("cyber-unavailable", incident.RequestCorrelationID+"\x00/v1/responses\x00gpt-5.6-sol")
	if incident.PromptFingerprint != want {
		t.Fatalf("unavailable prompt fingerprint = %q, want %q", incident.PromptFingerprint, want)
	}
}

// TestLogUpstreamCyberPolicyRecordsStreamingFailure verifies that a streaming
// response.failed creates an independent incident without a synthetic local log.
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
	incidentID, accepted := handler.logUpstreamCyberPolicy(ctx, "/v1/responses", "gpt-5.4", responseFailedErrorBody(payload), upstreamCyberPolicyAttempt{
		Transport: "sse", StatusCode: http.StatusBadRequest, AccountID: 7, AttemptIndex: 2,
	})
	if !accepted || incidentID == "" {
		t.Fatalf("incident enqueue accepted=%t id=%q", accepted, incidentID)
	}
	waitPromptFilterAuditIdle(t, db)

	logs, err := db.ListPromptFilterLogs(ctx.Request.Context(), 10)
	if err != nil {
		t.Fatalf("ListPromptFilterLogs error: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("synthetic prompt_filter_logs rows = %d, want 0", len(logs))
	}
	got, err := db.GetPromptPolicyIncident(ctx.Request.Context(), incidentID)
	if err != nil {
		t.Fatalf("GetPromptPolicyIncident error: %v", err)
	}
	if got.UpstreamErrorCode != "cyber_policy" || got.Transport != "sse" || got.StatusCode != http.StatusBadRequest || got.AccountID != 7 || got.AttemptIndex != 2 {
		t.Fatalf("incident context = %#v", got)
	}
	if got.LocalEvaluationState != database.PromptPolicyEvaluationUnavailable || got.LocalScore != nil || got.LocalAuditScore != nil {
		t.Fatalf("unavailable local evaluation = %#v", got)
	}
	if !strings.Contains(got.UpstreamError, "cyber_policy") {
		t.Fatalf("upstream_error = %q, want it to contain the upstream error body", got.UpstreamError)
	}
}

func TestUpstreamCyberPolicyStagesGlobalEvidenceWithoutChangingRuntimeRules(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "cyber-learning.db"))
	if err != nil {
		t.Fatalf("database.New(sqlite) error: %v", err)
	}
	defer db.Close()
	settings := &database.SystemSettings{
		MaxConcurrency: 2, PromptFilterEnabled: true, PromptFilterMode: promptfilter.ModeBlock,
		PromptFilterThreshold: 50, PromptFilterMaxTextLength: promptfilter.DefaultMaxTextLength,
		PromptFilterCustomPatterns: "[]", PromptFilterDisabledPatterns: "[]",
	}
	store := auth.NewStore(nil, nil, settings)
	handler := NewHandler(store, db, nil, nil)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("X-NewAPI-Request-ID", "cyber-learning-request-1")
	ctx.Set(contextAPIKeyID, int64(9))
	ctx.Set(contextAPIKeyName, "test-platform")

	text := "请分析这段复杂请求为何触发上游安全策略，但不要执行任何操作。"
	evaluation := promptGuardEvaluation{
		Envelope: promptfilter.RequestEnvelope{
			Endpoint: "/v1/responses", Protocol: promptfilter.ProtocolResponses, ModelFamily: promptfilter.ModelFamilyOpenAI,
			Segments: []promptfilter.Segment{{Origin: promptfilter.OriginCurrentUser, Role: "user", Text: text}},
		},
		Decision: promptfilter.Decision{Action: promptfilter.ActionAllow, AuditScore: 30, ReasonCode: "audit_only"},
		Verdict:  promptfilter.Verdict{Enabled: true, Action: promptfilter.ActionAllow, Score: 0, Matched: []promptfilter.Match{{Name: "audit_signal", Weight: 30, SignalOnly: true}}},
	}
	handler.capturePromptRuleLearningEvidence(ctx, "/v1/responses", "gpt-5.4", evaluation)
	payload := []byte(`{"error":{"code":"cyber_policy","message":"cyber security risk detected"}}`)
	handler.logUpstreamCyberPolicy(ctx, "/v1/responses", "gpt-5.4", payload)
	waitPromptFilterAuditIdle(t, db)

	if got := store.GetPromptFilterConfig().CustomPatterns; len(got) != 0 {
		t.Fatalf("CY evidence entered runtime custom patterns: %#v", got)
	}
	candidates, total, err := db.ListPromptRuleCandidates(ctx.Request.Context(), database.PromptRuleCandidateQuery{Status: database.PromptRuleCandidateStatusPending})
	if err != nil || total != 1 || len(candidates) != 1 {
		t.Fatalf("candidates total=%d items=%#v err=%v", total, candidates, err)
	}
	if candidates[0].Kind != database.PromptRuleCandidateKindEvidence || candidates[0].EvidenceCount != 1 {
		t.Fatalf("CY candidate = %#v", candidates[0])
	}
	evidence, err := db.ListPromptRuleCandidateEvidence(ctx.Request.Context(), candidates[0].ID, 10)
	if err != nil || len(evidence) != 1 || evidence[0].APIKeyID != 9 || evidence[0].SourceRef != "cyber-learning-request-1" {
		t.Fatalf("evidence=%#v err=%v", evidence, err)
	}
	incidents, incidentTotal, err := db.ListPromptPolicyIncidentsPage(ctx.Request.Context(), database.PromptPolicyIncidentQuery{Page: 1, PageSize: 10})
	if err != nil || incidentTotal != 1 || len(incidents) != 1 || incidents[0].LocalOutcome != database.PromptPolicyOutcomeAuditHit {
		t.Fatalf("incidents total=%d items=%#v err=%v", incidentTotal, incidents, err)
	}
	if incidents[0].LocalScore == nil || *incidents[0].LocalScore != 0 || incidents[0].LocalAuditScore == nil || *incidents[0].LocalAuditScore != 30 {
		t.Fatalf("nullable score semantics = %#v", incidents[0])
	}

	// Every upstream CY response is a distinct incident/evidence observation.
	// Queue retries remain idempotent because they persist the same incident ID.
	handler.logUpstreamCyberPolicy(ctx, "/v1/responses", "gpt-5.4", payload)
	waitPromptFilterAuditIdle(t, db)
	ctx.Request.Header.Set("X-NewAPI-Request-ID", "cyber-learning-request-2")
	handler.logUpstreamCyberPolicy(ctx, "/v1/responses", "gpt-5.4", payload)
	waitPromptFilterAuditIdle(t, db)
	candidates, total, err = db.ListPromptRuleCandidates(ctx.Request.Context(), database.PromptRuleCandidateQuery{Status: database.PromptRuleCandidateStatusPending})
	if err != nil || total != 1 || candidates[0].EvidenceCount != 3 {
		t.Fatalf("deduplicated candidates total=%d items=%#v err=%v", total, candidates, err)
	}
	incidents, incidentTotal, err = db.ListPromptPolicyIncidentsPage(ctx.Request.Context(), database.PromptPolicyIncidentQuery{Page: 1, PageSize: 10})
	if err != nil || incidentTotal != 3 || len(incidents) != 3 {
		t.Fatalf("distinct incidents total=%d items=%#v err=%v", incidentTotal, incidents, err)
	}
	seen := map[string]bool{}
	correlation := incidents[0].RequestCorrelationID
	for _, incident := range incidents {
		seen[incident.IncidentID] = true
		if incident.RequestCorrelationID != correlation {
			t.Fatalf("request correlation changed across attempts: %#v", incidents)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("incident IDs were merged: %#v", incidents)
	}
}

func TestUpstreamCyberPolicyStagesEvidenceWhenLocalFilterIsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "cyber-disabled-filter.db"))
	if err != nil {
		t.Fatalf("database.New(sqlite) error: %v", err)
	}
	defer db.Close()
	settings := &database.SystemSettings{
		MaxConcurrency: 2, PromptFilterEnabled: false, PromptFilterMode: promptfilter.ModeBlock,
		PromptFilterThreshold: 50, PromptFilterMaxTextLength: promptfilter.DefaultMaxTextLength,
		PromptFilterCustomPatterns: "[]", PromptFilterDisabledPatterns: "[]",
	}
	store := auth.NewStore(nil, nil, settings)
	handler := NewHandler(store, db, nil, nil)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("X-NewAPI-Request-ID", "cyber-disabled-filter-request")
	ctx.Set("raw_body", []byte(`{"model":"gpt-5.4","input":"分析上游安全告警的原因，但不要执行任何危险操作。"}`))
	payload := []byte(`{"error":{"code":"cyber_policy","message":"cyber security risk detected"}}`)
	handler.logUpstreamCyberPolicy(ctx, "/v1/responses", "gpt-5.4", payload)
	waitPromptFilterAuditIdle(t, db)

	candidates, total, err := db.ListPromptRuleCandidates(context.Background(), database.PromptRuleCandidateQuery{Status: database.PromptRuleCandidateStatusPending})
	if err != nil || total != 1 || len(candidates) != 1 || candidates[0].Kind != database.PromptRuleCandidateKindEvidence {
		t.Fatalf("disabled-filter CY candidate total=%d items=%#v err=%v", total, candidates, err)
	}
	if got := store.GetPromptFilterConfig().CustomPatterns; len(got) != 0 {
		t.Fatalf("disabled-filter CY evidence changed runtime rules: %#v", got)
	}
	incidents, incidentTotal, err := db.ListPromptPolicyIncidentsPage(context.Background(), database.PromptPolicyIncidentQuery{Page: 1, PageSize: 10})
	if err != nil || incidentTotal != 1 || len(incidents) != 1 {
		t.Fatalf("disabled-filter incidents total=%d items=%#v err=%v", incidentTotal, incidents, err)
	}
	if incidents[0].LocalEvaluationState != database.PromptPolicyEvaluationNotRun || incidents[0].LocalScore != nil || incidents[0].LocalAuditScore != nil || incidents[0].LocalMiss {
		t.Fatalf("disabled-filter nullable/local_miss semantics = %#v", incidents[0])
	}
}

func TestUpstreamCyberPolicyGlobalCandidateKeepsPerPlatformProvenance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "cyber-platforms.db"))
	if err != nil {
		t.Fatalf("database.New(sqlite) error: %v", err)
	}
	defer db.Close()
	settings := &database.SystemSettings{
		MaxConcurrency: 2, PromptFilterEnabled: true, PromptFilterMode: promptfilter.ModeBlock,
		PromptFilterThreshold: 50, PromptFilterMaxTextLength: promptfilter.DefaultMaxTextLength,
		PromptFilterCustomPatterns: "[]", PromptFilterDisabledPatterns: "[]",
	}
	store := auth.NewStore(nil, nil, settings)
	handler := NewHandler(store, db, nil, nil)
	payload := []byte(`{"error":{"code":"cyber_policy","message":"cyber security risk detected"}}`)
	text := "请分析同一条上游安全告警，不要执行任何操作。"
	evaluation := promptGuardEvaluation{
		Envelope: promptfilter.RequestEnvelope{
			Endpoint: "/v1/responses", Protocol: promptfilter.ProtocolResponses, ModelFamily: promptfilter.ModelFamilyOpenAI,
			Segments: []promptfilter.Segment{{Origin: promptfilter.OriginCurrentUser, Role: "user", Text: text}},
		},
		Decision: promptfilter.Decision{Action: promptfilter.ActionAllow, AuditScore: 30, ReasonCode: "audit_only"},
		Verdict:  promptfilter.Verdict{Enabled: true, Action: promptfilter.ActionAllow},
	}
	observe := func(apiKeyID int64, apiKeyName, platform string) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		ctx.Request.Header.Set("X-NewAPI-Request-ID", "shared-request-id")
		ctx.Set(contextAPIKeyID, apiKeyID)
		ctx.Set(contextAPIKeyName, apiKeyName)
		ctx.Set(newAPIPolicyMetaContextKey, verifiedNewAPIPolicyContext{APIKeyID: apiKeyID, Platform: platform})
		handler.capturePromptRuleLearningEvidence(ctx, "/v1/responses", "gpt-5.4", evaluation)
		handler.logUpstreamCyberPolicy(ctx, "/v1/responses", "gpt-5.4", payload)
	}
	observe(9, "gateway-a-key", "gateway-a")
	observe(10, "gateway-b-key", "gateway-b")
	waitPromptFilterAuditIdle(t, db)

	candidates, total, err := db.ListPromptRuleCandidates(context.Background(), database.PromptRuleCandidateQuery{Status: database.PromptRuleCandidateStatusPending})
	if err != nil || total != 1 || len(candidates) != 1 || candidates[0].EvidenceCount != 2 {
		t.Fatalf("global candidate total=%d items=%#v err=%v", total, candidates, err)
	}
	evidence, err := db.ListPromptRuleCandidateEvidence(context.Background(), candidates[0].ID, 10)
	if err != nil || len(evidence) != 2 {
		t.Fatalf("evidence=%#v err=%v", evidence, err)
	}
	ids := map[int64]bool{}
	for _, item := range evidence {
		ids[item.APIKeyID] = true
	}
	if !ids[9] || !ids[10] {
		t.Fatalf("per-platform provenance was merged: %#v", evidence)
	}
}
