package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codex2api/api"
	"github.com/codex2api/database"
	"github.com/gin-gonic/gin"
)

func TestInternalResponseAttributionPreservesParentAuditIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parent, _ := gin.CreateTestContext(httptest.NewRecorder())
	parent.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	parent.Request.RemoteAddr = "203.0.113.42:41234"
	parent.Request.Header.Set("User-Agent", "codex-cli/test")
	row := &database.APIKeyRow{
		ID:              42,
		AllowedGroupIDs: []int64{9},
		Limits: database.APIKeyLimits{
			AutoCompactOnOverflow: true,
			ModelAllow:             []string{"gpt-5.4"},
			UpstreamChannel:        database.UpstreamChannelGrok,
		},
	}
	parent.Set(contextAPIKeyID, row.ID)
	parent.Set(contextAPIKeyName, "team-key")
	parent.Set(contextAPIKeyMasked, "sk-...test")
	parent.Set(contextAPIKeyRow, row)
	parent.Set("request_context", &api.RequestContext{RequestID: "req-parent-42"})

	attribution := internalResponseAttributionFromRequest(parent, internalReasonOverflowCompact)
	if attribution == nil {
		t.Fatal("attribution = nil")
	}
	if attribution.APIKeyID != 42 || attribution.APIKeyRow != row {
		t.Fatalf("API key attribution = (%d, %p), want (42, %p)", attribution.APIKeyID, attribution.APIKeyRow, row)
	}
	if attribution.ClientIP != "203.0.113.42" || attribution.ClientUserAgent != "codex-cli/test" {
		t.Fatalf("client attribution = (%q, %q)", attribution.ClientIP, attribution.ClientUserAgent)
	}
	if attribution.ParentRequestID != "req-parent-42" || attribution.Reason != internalReasonOverflowCompact {
		t.Fatalf("internal attribution = (%q, %q)", attribution.ParentRequestID, attribution.Reason)
	}

	child, _ := gin.CreateTestContext(httptest.NewRecorder())
	child.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	applyInternalResponseAttribution(child, child.Request, attribution)
	if got := requestAPIKeyID(child); got != 42 {
		t.Fatalf("child APIKeyID = %d, want 42", got)
	}
	if got := apiKeyRowFromContext(child); got != row {
		t.Fatalf("child APIKeyRow = %p, want %p", got, row)
	}
	if got := child.ClientIP(); got != "203.0.113.42" {
		t.Fatalf("child ClientIP = %q, want 203.0.113.42", got)
	}
	if got := child.Request.UserAgent(); got != "codex-cli/test" {
		t.Fatalf("child User-Agent = %q, want codex-cli/test", got)
	}
	if authHeader := child.GetHeader("Authorization"); authHeader != "" {
		t.Fatalf("child copied raw authorization header: %q", authHeader)
	}
	if autoCompactOverflowEnabled(child) {
		t.Fatal("attributed summary request must not recursively auto-compact")
	}
	if got := requestUpstreamChannel(child); got != database.UpstreamChannelGrok {
		t.Fatalf("child upstream channel = %q, want grok", got)
	}
	if status, _ := (&Handler{}).enforceAPIKeyLimits(child, "gpt-other"); status != http.StatusForbidden {
		t.Fatalf("child model restriction status = %d, want 403", status)
	}

	input := &database.UsageLogInput{}
	populateAPIKeyMetaFromContext(child, input)
	populateInternalUsageMetaFromContext(child, input)
	populateClientIPFromRequest(child, input)
	populateUserAgentMetaFromRequest(child, input)
	if input.APIKeyID != 42 || input.APIKeyName != "team-key" || input.APIKeyMasked != "sk-...test" {
		t.Fatalf("usage key attribution = %+v", input)
	}
	if input.InternalReason != internalReasonOverflowCompact || input.ParentRequestID != "req-parent-42" {
		t.Fatalf("usage internal attribution = (%q, %q)", input.InternalReason, input.ParentRequestID)
	}
	if input.ClientIP != "203.0.113.42" || input.ClientUserAgent != "codex-cli/test" {
		t.Fatalf("usage client attribution = (%q, %q)", input.ClientIP, input.ClientUserAgent)
	}
}

func TestApplyInternalResponseAttributionNilRemainsAnonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	applyInternalResponseAttribution(c, c.Request, nil)
	if requestAPIKeyID(c) != 0 || apiKeyRowFromContext(c) != nil {
		t.Fatal("anonymous internal response unexpectedly received API key identity")
	}
	if _, exists := c.Get(contextDisableOverflowAutoCompact); exists {
		t.Fatal("nil attribution unexpectedly changed recursion policy")
	}
}
