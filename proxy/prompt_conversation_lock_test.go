package proxy

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/database"
)

func newPromptConversationLockTestHandler(t *testing.T) (*Handler, *database.DB) {
	t.Helper()
	db, err := database.New("sqlite", filepath.Join(t.TempDir(), "conversation-lock.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := promptGuardTestConfig()
	cfg.Advanced.Enforcement.ConversationLockEnabled = true
	cfg.Advanced.Enforcement.CYBStrikeEnabled = true
	store := auth.NewStore(db, nil, &database.SystemSettings{MaxConcurrency: 2, TestConcurrency: 1})
	store.SetPromptFilterConfig(cfg)
	store.ReplacePromptFilterNewAPIBindings([]*database.PromptFilterNewAPIBinding{{
		APIKeyID: 101, PlatformCode: "gateway-a", Secret: "gateway-a-secret", Enabled: true,
		PolicyMode: database.PromptFilterPolicyModeEnforce, PolicyProfile: database.PromptFilterPolicyProfileBalanced,
	}})
	handler := NewHandler(store, db, nil, nil)
	handler.SetRuntimeCache(cache.NewMemory(1))
	return handler, db
}

func TestExplicitUpstreamCYBLocksOnlyTheSignedConversation(t *testing.T) {
	handler, db := newPromptConversationLockTestHandler(t)
	body := []byte(`{"model":"gpt-5.5","input":"ordinary request"}`)
	fingerprint := "0123456789abcdef0123456789abcdef"
	identity := newAPIIdentity{UserID: "42", ClientIP: "203.0.113.8"}
	first := signedBoundNewAPIPolicyContext(t, "cyb-lock-first", identity, body, 101, "gateway-a", "gateway-a-secret", fingerprint)
	setIngressRequestBodyIfAbsent(first, body)

	handler.logUpstreamCyberPolicy(first, "/v1/responses", "gpt-5.5", []byte(`{"error":{"code":"cyber_policy"}}`))
	metadata := policyDecisionMetadataFromHeaders(first.Writer.Header())
	if metadata.ReasonCode != newAPIUpstreamCyberPolicyReasonCode || !metadata.StrikeEligible {
		t.Fatalf("first CYB metadata = %+v", metadata)
	}
	responseMetadata, delegated := newAPIUpstreamCyberPolicyDecision(first)
	if !delegated || !responseMetadata.ConversationLocked {
		t.Fatalf("first CYB response metadata = %+v delegated=%t", responseMetadata, delegated)
	}
	if message := newAPIPolicyDecisionAPIError(responseMetadata).Message; message != upstreamCyberPolicyLockedUserMessage || !strings.Contains(message, "再次触发可能会停用账号") {
		t.Fatalf("first locked CYB message = %q", message)
	}
	policyContext, verified := handler.verifyNewAPIPolicyContext(first, handler.promptFilterConfigForRequest(first).Advanced.NewAPI, body)
	lockIdentity, ok := verifiedPromptConversationLockIdentity(first, policyContext)
	if !verified || !ok {
		t.Fatal("signed session identity was not available for conversation lock")
	}
	lock, err := db.GetActivePromptConversationLock(t.Context(), lockIdentity.LockKey)
	if err != nil || lock.TriggerCount != 1 || lock.DecisionID != metadata.DecisionID {
		t.Fatalf("active lock = %#v err=%v", lock, err)
	}

	repeat := signedBoundNewAPIPolicyContext(t, "cyb-lock-repeat", identity, body, 101, "gateway-a", "gateway-a-secret", fingerprint)
	setIngressRequestBodyIfAbsent(repeat, body)
	if blocked := handler.inspectPromptFilterOpenAI(repeat, body, "/v1/responses", "gpt-5.5"); !blocked {
		t.Fatal("locked conversation was forwarded")
	}
	repeatMetadata := policyDecisionMetadataFromHeaders(repeat.Writer.Header())
	if repeatMetadata.ReasonCode != promptConversationLockedReasonCode || repeatMetadata.StrikeEligible {
		t.Fatalf("locked retry metadata = %+v", repeatMetadata)
	}
	if message := newAPIPolicyDecisionAPIError(repeatMetadata).Message; !strings.Contains(message, "不会重复累计") || !strings.Contains(message, "再次触发 CYB 可能会停用账号") {
		t.Fatalf("locked retry message = %q", message)
	}
	lock, err = db.GetActivePromptConversationLock(t.Context(), lockIdentity.LockKey)
	if err != nil || lock.TriggerCount != 1 {
		t.Fatalf("locked retry changed CYB count: lock=%#v err=%v", lock, err)
	}

	otherFingerprint := "fedcba9876543210fedcba9876543210"
	other := signedBoundNewAPIPolicyContext(t, "cyb-lock-other", identity, body, 101, "gateway-a", "gateway-a-secret", otherFingerprint)
	setIngressRequestBodyIfAbsent(other, body)
	if blocked := handler.inspectPromptFilterOpenAI(other, body, "/v1/responses", "gpt-5.5"); blocked {
		t.Fatal("new conversation was blocked by another conversation's CYB lock")
	}
}

func TestConversationLockRequiresStableSignedFingerprintAndCanBeDisabled(t *testing.T) {
	handler, db := newPromptConversationLockTestHandler(t)
	body := []byte(`{"model":"gpt-5.5","input":"ordinary request"}`)
	identity := newAPIIdentity{UserID: "42", ClientIP: "203.0.113.8"}
	withoutSession := signedBoundNewAPIPolicyContext(t, "cyb-no-session", identity, body, 101, "gateway-a", "gateway-a-secret", "")
	setIngressRequestBodyIfAbsent(withoutSession, body)
	handler.logUpstreamCyberPolicy(withoutSession, "/v1/responses", "gpt-5.5", []byte(`{"error":{"code":"cyber_policy"}}`))
	if _, err := db.GetActivePromptConversationLockBySessionHash(t.Context(), "anything"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CYB without signed session created a lock: %v", err)
	}
	withoutSessionMetadata, delegated := newAPIUpstreamCyberPolicyDecision(withoutSession)
	if !delegated || withoutSessionMetadata.ConversationLocked {
		t.Fatalf("CYB without session response metadata = %+v delegated=%t", withoutSessionMetadata, delegated)
	}
	if message := newAPIPolicyDecisionAPIError(withoutSessionMetadata).Message; message != upstreamCyberPolicyUserMessage || strings.Contains(message, "已锁定当前对话") || !strings.Contains(message, "再次触发可能会停用账号") {
		t.Fatalf("CYB without session message = %q", message)
	}

	cfg := handler.store.GetPromptFilterConfig()
	cfg.Advanced.Enforcement.ConversationLockEnabled = false
	handler.store.SetPromptFilterConfig(cfg)
	fingerprint := "0123456789abcdef0123456789abcdef"
	disabled := signedBoundNewAPIPolicyContext(t, "cyb-lock-disabled", identity, body, 101, "gateway-a", "gateway-a-secret", fingerprint)
	setIngressRequestBodyIfAbsent(disabled, body)
	handler.logUpstreamCyberPolicy(disabled, "/v1/responses", "gpt-5.5", []byte(`{"error":{"code":"cyber_policy"}}`))
	if _, err := db.GetActivePromptConversationLockBySessionHash(t.Context(), hashRiskIdentity(fingerprint)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("disabled conversation-lock feature created a lock: %v", err)
	}
}

func TestConversationLockStorageFailureDoesNotClaimConversationWasLocked(t *testing.T) {
	handler, _ := newPromptConversationLockTestHandler(t)
	body := []byte(`{"model":"gpt-5.5","input":"ordinary request"}`)
	c := signedBoundNewAPIPolicyContext(t, "cyb-lock-db-failure", newAPIIdentity{UserID: "42", ClientIP: "203.0.113.8"}, body, 101, "gateway-a", "gateway-a-secret", "0123456789abcdef0123456789abcdef")
	setIngressRequestBodyIfAbsent(c, body)
	metadata, delegated := handler.emitNewAPIUpstreamCyberPolicyDecision(c, "/v1/responses", "gpt-5.5", []byte(`{"error":{"code":"cyber_policy"}}`))
	if !delegated {
		t.Fatal("signed CYB decision was not emitted")
	}
	canceledContext, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(canceledContext)
	metadata.ConversationLocked = handler.lockPromptConversationAfterUpstreamCYB(c, "/v1/responses", "gpt-5.5", "incident-db-failure", metadata)
	if metadata.ConversationLocked {
		t.Fatal("database failure was reported as a successful conversation lock")
	}
	if message := newAPIPolicyDecisionAPIError(metadata).Message; message != upstreamCyberPolicyUserMessage || strings.Contains(message, "已锁定当前对话") {
		t.Fatalf("database failure CYB message = %q", message)
	}
}
