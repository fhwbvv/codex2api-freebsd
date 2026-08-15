package proxy

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// 端到端:本地高置信度严重违规必须在发往上游前,把一次 strike 记到 NewAPI 用户
// 身上(触发 NewAPI 侧的 CYB 累计与自动封号),而同一会话的重复试探被会话锁
// 拦下且不再重复累计。
func TestLocalSevereViolationEmitsStrikeThenLockDeduplicates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := newPromptConversationLockTestHandler(t)
	identity := newAPIIdentity{UserID: "77", ClientIP: "203.0.113.9"}
	fingerprint := "0123456789abcdef0123456789abcdef"

	// 第一次:当前用户直接发出的终局严重违规。应被本地拦截并记一次 strike。
	blatantBody := promptRequestBody(t, blatantIntentBlockedByLocalRegex)
	first := signedBoundNewAPIPolicyContext(t, "local-strike-first", identity, blatantBody, 101, "gateway-a", "gateway-a-secret", fingerprint)
	setIngressRequestBodyIfAbsent(first, blatantBody)
	if blocked := handler.inspectPromptFilterOpenAI(first, blatantBody, "/v1/responses", "gpt-5.5"); !blocked {
		t.Fatal("锚点严重违规未被本地拦截,测试前提不成立")
	}
	firstMeta := policyDecisionMetadataFromHeaders(first.Writer.Header())
	if !firstMeta.StrikeEligible {
		t.Fatalf("首次本地严重违规未记 strike: %+v", firstMeta)
	}
	if firstMeta.ReasonCode == promptConversationLockedReasonCode {
		t.Fatal("首次违规不应表现为会话锁拦截")
	}

	// 第二次:同一会话再次试探(哪怕换成能绕过正则的变形)。应被会话锁拦下,
	// 且绝不重复累计 strike,否则一次违规会把用户瞬间刷到封号阈值。
	evasiveBody := promptRequestBody(t, evasiveVariantThatDefeatsLocalRegex)
	second := signedBoundNewAPIPolicyContext(t, "local-strike-repeat", identity, evasiveBody, 101, "gateway-a", "gateway-a-secret", fingerprint)
	setIngressRequestBodyIfAbsent(second, evasiveBody)
	if blocked := handler.inspectPromptFilterOpenAI(second, evasiveBody, "/v1/responses", "gpt-5.5"); !blocked {
		t.Fatal("已锁会话的重复试探被放行到上游")
	}
	secondMeta := policyDecisionMetadataFromHeaders(second.Writer.Header())
	if secondMeta.ReasonCode != promptConversationLockedReasonCode {
		t.Fatalf("重复试探应表现为会话锁拦截,实际 reason=%q", secondMeta.ReasonCode)
	}
	if secondMeta.StrikeEligible {
		t.Fatal("已锁会话的重复拦截重复累计了 strike")
	}
}

// 关闭 LocalSevereStrikeEnabled 后,本地严重违规仍被拦截,但不再记 strike——
// 拦截(安全)与封号(不可逆)两个后果解耦,由运营者独立掌控。
func TestLocalSevereStrikeCanBeDisabledWhileStillBlocking(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := newPromptConversationLockTestHandler(t)
	cfg := handler.store.GetPromptFilterConfig()
	cfg.Advanced.Enforcement.LocalSevereStrikeEnabled = false
	handler.store.SetPromptFilterConfig(cfg)

	body := promptRequestBody(t, blatantIntentBlockedByLocalRegex)
	c := signedBoundNewAPIPolicyContext(t, "local-strike-disabled", newAPIIdentity{UserID: "77", ClientIP: "203.0.113.9"}, body, 101, "gateway-a", "gateway-a-secret", "0123456789abcdef0123456789abcdef")
	setIngressRequestBodyIfAbsent(c, body)
	if blocked := handler.inspectPromptFilterOpenAI(c, body, "/v1/responses", "gpt-5.5"); !blocked {
		t.Fatal("关闭 strike 累计不应影响拦截本身")
	}
	meta := policyDecisionMetadataFromHeaders(c.Writer.Header())
	if meta.StrikeEligible {
		t.Fatalf("关闭 LocalSevereStrikeEnabled 后仍记了 strike: %+v", meta)
	}
}
