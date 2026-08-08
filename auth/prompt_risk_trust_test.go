package auth

import (
	"testing"
	"time"

	"github.com/codex2api/database"
)

func TestPromptRiskTrustSnapshotRequiresActiveUnexpiredLowRiskPolicy(t *testing.T) {
	store := NewStore(nil, nil, nil)
	t.Cleanup(store.Stop)
	now := time.Now().UTC()
	store.ReplacePromptRiskTrustPolicies([]*database.PromptRiskTrustPolicy{{
		ID: 1, SubjectType: database.PromptRiskSubjectNewAPIUser, SubjectKey: "active",
		Status: database.PromptRiskTrustStatusActive, ValidUntil: now.Add(time.Hour), RiskThreshold: 35, LastRiskScore: 10,
	}, {
		ID: 2, SubjectType: database.PromptRiskSubjectNewAPIUser, SubjectKey: "expired",
		Status: database.PromptRiskTrustStatusActive, ValidUntil: now.Add(-time.Minute), RiskThreshold: 35, LastRiskScore: 0,
	}, {
		ID: 3, SubjectType: database.PromptRiskSubjectNewAPIUser, SubjectKey: "risky",
		Status: database.PromptRiskTrustStatusActive, ValidUntil: now.Add(time.Hour), RiskThreshold: 35, LastRiskScore: 35,
	}})
	if _, ok := store.GetPromptRiskTrustPolicy("active", now); !ok {
		t.Fatal("active low-risk policy missing")
	}
	for _, key := range []string{"expired", "risky", "missing"} {
		if _, ok := store.GetPromptRiskTrustPolicy(key, now); ok {
			t.Fatalf("policy %q unexpectedly active", key)
		}
	}
	store.RemovePromptRiskTrustPolicy("active")
	if _, ok := store.GetPromptRiskTrustPolicy("active", now); ok {
		t.Fatal("removed policy remained in runtime snapshot")
	}
}
