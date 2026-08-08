package database

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPromptIntelligenceAIEvidenceAndAdvancedConfigCAS(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "prompt-intelligence-ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.conn.ExecContext(ctx, `INSERT INTO system_settings (id, prompt_filter_advanced_config) VALUES (1, '{}') ON CONFLICT(id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	candidate, _, err := db.StagePromptRuleCandidate(ctx, PromptRuleCandidateInput{
		Fingerprint: strings.Repeat("a", 64), Kind: PromptRuleCandidateKindEvidence,
		Source: PromptRuleCandidateSourceUpstreamCyberPolicy, SamplePreview: "redacted CY evidence",
	}, PromptRuleCandidateEvidenceInput{
		SourceKind: PromptRuleCandidateSourceUpstreamCyberPolicy, SourceRef: "incident-1",
		SourceRefHash: strings.Repeat("b", 64), ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	input := PromptRuleCandidateEvidenceInput{
		SourceKind: PromptRuleCandidateSourceAIAnalysis, SourceRef: "analysis-1",
		SourceRefHash: strings.Repeat("c", 64), MetadataJSON: `{"decision":"identity"}`, ObservedAt: time.Now().UTC(),
	}
	evidence, added, err := db.AddPromptRuleCandidateEvidence(ctx, candidate.ID, input)
	if err != nil || !added || evidence.CandidateID != candidate.ID {
		t.Fatalf("add evidence=%#v added=%v err=%v", evidence, added, err)
	}
	replayed, added, err := db.AddPromptRuleCandidateEvidence(ctx, candidate.ID, input)
	if err != nil || added || replayed.ID != evidence.ID {
		t.Fatalf("replay evidence=%#v added=%v err=%v", replayed, added, err)
	}

	revisionInput := PromptRuleCandidateEvidenceInput{
		SourceKind: PromptRuleCandidateSourceAIIdentityUpdate, SourceRef: "analysis-1",
		SourceRefHash: strings.Repeat("d", 64), MetadataJSON: `{"version":1}`, ObservedAt: time.Now().UTC(),
	}
	if _, err := db.conn.ExecContext(ctx, `DELETE FROM system_settings WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	swapped, revision, err := db.CompareAndSwapPromptFilterAdvancedConfigWithEvidence(ctx, candidate.ID, "{}", `{"review_adapter":{"system_prompt":"managed"}}`, PromptRuleCandidateStatusPublished, revisionInput)
	if err != nil || !swapped || revision == nil || revision.SourceKind != PromptRuleCandidateSourceAIIdentityUpdate {
		t.Fatalf("CAS swapped=%v revision=%#v err=%v", swapped, revision, err)
	}
	persisted, err := db.GetSystemSettings(ctx)
	if err != nil || !strings.Contains(persisted.PromptFilterAdvancedConfig, "managed") {
		t.Fatalf("persisted=%#v err=%v", persisted, err)
	}
	published, err := db.GetPromptRuleCandidate(ctx, candidate.ID)
	if err != nil || published.Status != PromptRuleCandidateStatusPublished {
		t.Fatalf("published candidate=%#v err=%v", published, err)
	}
	conflictInput := revisionInput
	conflictInput.SourceRefHash = strings.Repeat("e", 64)
	swapped, revision, err = db.CompareAndSwapPromptFilterAdvancedConfigWithEvidence(ctx, candidate.ID, "{}", `{"review_adapter":{"system_prompt":"lost"}}`, PromptRuleCandidateStatusPublished, conflictInput)
	if err != nil || swapped || revision != nil {
		t.Fatalf("stale CAS swapped=%v revision=%#v err=%v", swapped, revision, err)
	}
	item, err := db.GetPromptRuleCandidate(ctx, candidate.ID)
	if err != nil || item.EvidenceCount != 3 {
		t.Fatalf("candidate=%#v err=%v", item, err)
	}
}

func TestListLatestPromptRuleCandidateAIAnalyses(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "prompt-intelligence-ai-latest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	candidate, _, err := db.StagePromptRuleCandidate(ctx, PromptRuleCandidateInput{
		Fingerprint: strings.Repeat("f", 64), Kind: PromptRuleCandidateKindEvidence,
		Source: PromptRuleCandidateSourceUpstreamCyberPolicy, SamplePreview: "redacted CY evidence",
	}, PromptRuleCandidateEvidenceInput{
		SourceKind: PromptRuleCandidateSourceUpstreamCyberPolicy, SourceRef: "incident-latest",
		SourceRefHash: strings.Repeat("1", 64), ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	first, added, err := db.AddPromptRuleCandidateEvidence(ctx, candidate.ID, PromptRuleCandidateEvidenceInput{
		SourceKind: PromptRuleCandidateSourceAIAnalysis, SourceRef: "analysis-first",
		SourceRefHash: strings.Repeat("2", 64), MetadataJSON: `{"version":1,"result":{"decision":"no_change","confidence":0.8,"reason":"first"}}`,
		Provider: "review", Model: "deepseek-first", ObservedAt: time.Now().UTC().Add(-time.Minute),
	})
	if err != nil || !added {
		t.Fatalf("first evidence=%#v added=%v err=%v", first, added, err)
	}
	latest, added, err := db.AddPromptRuleCandidateEvidence(ctx, candidate.ID, PromptRuleCandidateEvidenceInput{
		SourceKind: PromptRuleCandidateSourceAIAnalysis, SourceRef: "analysis-latest",
		SourceRefHash: strings.Repeat("3", 64), MetadataJSON: `{"version":1,"result":{"decision":"rule","confidence":0.95,"reason":"latest"}}`,
		Provider: "review", Model: "deepseek-latest", ObservedAt: time.Now().UTC(),
	})
	if err != nil || !added {
		t.Fatalf("latest evidence=%#v added=%v err=%v", latest, added, err)
	}
	identityUpdate, added, err := db.AddPromptRuleCandidateEvidence(ctx, candidate.ID, PromptRuleCandidateEvidenceInput{
		SourceKind: PromptRuleCandidateSourceAIIdentityUpdate, SourceRef: "analysis-latest",
		SourceRefHash: strings.Repeat("4", 64), MetadataJSON: `{"analysis_evidence_id":` + fmt.Sprint(latest.ID) + `,"mode":"manual"}`, ObservedAt: time.Now().UTC(),
	})
	if err != nil || !added {
		t.Fatalf("identity evidence=%#v added=%v err=%v", identityUpdate, added, err)
	}

	summaries, err := db.ListLatestPromptRuleCandidateAIAnalyses(ctx, []int64{candidate.ID, candidate.ID, 0})
	if err != nil {
		t.Fatal(err)
	}
	summary, ok := summaries[candidate.ID]
	if !ok || summary.Count != 2 || summary.Latest == nil || summary.Latest.ID != latest.ID || summary.Latest.Model != "deepseek-latest" || summary.LatestIdentityChange == nil || summary.LatestIdentityChange.ID != identityUpdate.ID {
		t.Fatalf("summary=%#v", summary)
	}
	if err := db.ReconcilePromptRuleCandidateIdentityStatuses(ctx); err != nil {
		t.Fatal(err)
	}
	reconciled, err := db.GetPromptRuleCandidate(ctx, candidate.ID)
	if err != nil || reconciled.Status != PromptRuleCandidateStatusPublished {
		t.Fatalf("reconciled update candidate=%#v err=%v", reconciled, err)
	}
	_, added, err = db.AddPromptRuleCandidateEvidence(ctx, candidate.ID, PromptRuleCandidateEvidenceInput{
		SourceKind: PromptRuleCandidateSourceAIIdentityRollback, SourceRef: "revision-latest",
		SourceRefHash: strings.Repeat("5", 64), MetadataJSON: `{"analysis_evidence_id":` + fmt.Sprint(latest.ID) + `,"mode":"rollback"}`, ObservedAt: time.Now().UTC(),
	})
	if err != nil || !added {
		t.Fatalf("rollback evidence added=%v err=%v", added, err)
	}
	if err := db.ReconcilePromptRuleCandidateIdentityStatuses(ctx); err != nil {
		t.Fatal(err)
	}
	reconciled, err = db.GetPromptRuleCandidate(ctx, candidate.ID)
	if err != nil || reconciled.Status != PromptRuleCandidateStatusPending {
		t.Fatalf("reconciled rollback candidate=%#v err=%v", reconciled, err)
	}
	empty, err := db.ListLatestPromptRuleCandidateAIAnalyses(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty=%#v err=%v", empty, err)
	}
}
