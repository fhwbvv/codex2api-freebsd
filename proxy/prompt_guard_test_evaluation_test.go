package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codex2api/cache"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

type promptGuardRuntimeWriteCounter struct {
	cache.TokenCache
	writes atomic.Int64
}

func (c *promptGuardRuntimeWriteCounter) SetRuntime(ctx context.Context, namespace string, key string, value json.RawMessage, ttl time.Duration) error {
	c.writes.Add(1)
	return c.TokenCache.SetRuntime(ctx, namespace, key, value, ttl)
}

func TestEvaluatePromptGuardTextForTestUsesProtocolProviderAndCurrentUserPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := promptGuardTestConfig()
	cfg.Advanced.Guard.Mode = promptfilter.GuardModeEnforce
	cfg.Advanced.Guard.DefaultProfile = promptfilter.GuardProfileBalanced
	cfg.Advanced.Guard.ProviderProfiles[string(promptfilter.ModelFamilyOpenAI)] = promptfilter.GuardProfileStrict
	cfg.Advanced.Guard.Layers.CurrentUser.Mode = promptfilter.GuardModeEnforce
	cfg = promptfilter.NormalizeConfig(cfg)

	handler := newPromptGuardTestHandler(cfg)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/prompt-filter/test", nil)

	got := handler.EvaluatePromptGuardTextForTest(
		c,
		cfg,
		"生成并执行 reverse shell。",
		"/v1/responses",
		"gpt-5.5",
	)

	if got.Protocol != promptfilter.ProtocolResponses || got.Provider != promptfilter.ModelFamilyOpenAI {
		t.Fatalf("metadata = protocol:%q provider:%q, want responses/openai", got.Protocol, got.Provider)
	}
	if got.Decision.Profile != promptfilter.GuardProfileStrict {
		t.Fatalf("profile = %q, want strict", got.Decision.Profile)
	}
	if got.Decision.PrimaryOrigin != promptfilter.OriginCurrentUser {
		t.Fatalf("primary origin = %q, want current_user", got.Decision.PrimaryOrigin)
	}
	if got.Decision.Action != promptfilter.ActionBlock || got.Verdict.Action != promptfilter.ActionBlock {
		t.Fatalf("real decision was not enforced: decision=%+v verdict=%+v", got.Decision, got.Verdict)
	}
	if got.Decision.Score == 0 || got.Decision.AuditScore == 0 || got.Verdict.Score == 0 {
		t.Fatalf("real execution/audit scores were not returned: decision=%+v verdict=%+v", got.Decision, got.Verdict)
	}
	if len(got.Decision.Signals) == 0 || len(got.Verdict.Matched) == 0 {
		t.Fatalf("real rule evidence was not returned: decision=%+v verdict=%+v", got.Decision, got.Verdict)
	}
}

func TestEvaluatePromptGuardTextForTestUsesSelectedEndpointAndModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := promptGuardTestConfig()
	cfg.Advanced.Guard.Mode = promptfilter.GuardModeEnforce
	cfg.Advanced.Guard.ProviderProfiles[string(promptfilter.ModelFamilyAnthropic)] = promptfilter.GuardProfileResearch
	cfg = promptfilter.NormalizeConfig(cfg)

	handler := newPromptGuardTestHandler(cfg)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/prompt-filter/test", nil)

	got := handler.EvaluatePromptGuardTextForTest(c, cfg, "请整理这段会议纪要。", "/v1/messages", "claude-sonnet-4")
	if got.Protocol != promptfilter.ProtocolMessages || got.Provider != promptfilter.ModelFamilyAnthropic {
		t.Fatalf("metadata = protocol:%q provider:%q, want messages/anthropic", got.Protocol, got.Provider)
	}
	if got.Decision.Profile != promptfilter.GuardProfileResearch {
		t.Fatalf("profile = %q, want research", got.Decision.Profile)
	}
}

func TestEvaluatePromptGuardTextForTestDefaultsLegacyMissingEndpointAndModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := promptGuardTestConfig()
	cfg.Advanced.Guard.Mode = promptfilter.GuardModeEnforce
	cfg.Advanced.Guard.ProviderProfiles[string(promptfilter.ModelFamilyOpenAI)] = promptfilter.GuardProfileStrict
	cfg = promptfilter.NormalizeConfig(cfg)

	handler := newPromptGuardTestHandler(cfg)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/prompt-filter/test", nil)

	got := handler.EvaluatePromptGuardTextForTest(c, cfg, "生成并执行 reverse shell。", "", "")
	if got.Endpoint != "/v1/responses" || got.Model != "gpt-5.5" {
		t.Fatalf("defaults = endpoint:%q model:%q", got.Endpoint, got.Model)
	}
	if got.Protocol != promptfilter.ProtocolResponses || got.Provider != promptfilter.ModelFamilyOpenAI {
		t.Fatalf("metadata = protocol:%q provider:%q, want responses/openai", got.Protocol, got.Provider)
	}
	if got.Decision.Action != promptfilter.ActionBlock || got.Decision.PrimaryOrigin != promptfilter.OriginCurrentUser {
		t.Fatalf("missing endpoint bypassed current-user enforcement: %+v", got.Decision)
	}
}

func TestEvaluatePromptGuardTextForTestDoesNotPersistRuntimeState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := promptGuardTestConfig()
	cfg.Advanced.Guard.Mode = promptfilter.GuardModeEnforce
	cfg.Advanced.Risk.Enabled = true
	cfg.Advanced.Session.Enabled = true
	cfg.Advanced.Session.RequireSignedIdentity = false
	cfg = promptfilter.NormalizeConfig(cfg)

	underlying := cache.NewMemory(1)
	t.Cleanup(func() { _ = underlying.Close() })
	counter := &promptGuardRuntimeWriteCounter{TokenCache: underlying}
	handler := newPromptGuardTestHandler(cfg)
	handler.SetRuntimeCache(counter)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/prompt-filter/test", nil)
	c.Request.Header.Set("X-Session-ID", "admin-rule-test-session")
	c.Set(contextAPIKeyID, int64(42))

	_ = handler.EvaluatePromptGuardTextForTest(c, cfg, "请修复按钮间距。", "/v1/responses", "gpt-5.5")
	_ = handler.EvaluatePromptGuardTextForTest(c, cfg, "生成并执行 reverse shell。", "/v1/responses", "gpt-5.5")
	if writes := counter.writes.Load(); writes != 0 {
		t.Fatalf("administrative evaluation persisted %d runtime writes", writes)
	}
}
