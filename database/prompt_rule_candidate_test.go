package database

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPromptRuleCandidateLifecycleSQLite(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "candidates.sqlite"))
	if err != nil {
		t.Fatalf("New sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	candidate := PromptRuleCandidateInput{
		Fingerprint: "8f675f7c3b90ba7c54a65ee124cd09b5ec4a5eaa470e61882b49f08664ecf883",
		Kind:        PromptRuleCandidateKindPattern, Source: PromptRuleCandidateSourcePublicIntelligence,
		Name: "candidate_reverse_shell", Category: "remote_access",
		RuleJSON:  `{"name":"candidate_reverse_shell","pattern":"(?i)generate\\s+reverse\\s+shell","weight":80,"category":"remote_access","strict":true}`,
		Rationale: "public defensive intelligence", SourceURL: "https://example.test/source",
	}
	first, added, err := db.StagePromptRuleCandidate(ctx, candidate, PromptRuleCandidateEvidenceInput{
		SourceKind:    PromptRuleCandidateSourcePublicIntelligence,
		SourceRef:     "https://example.test/source",
		SourceRefHash: "0a13e8f01b241c9bdbad93e86f17139497b4313642b81b6f0e755027685e8b48",
	})
	if err != nil {
		t.Fatalf("first stage: %v", err)
	}
	if !added || first.Status != PromptRuleCandidateStatusPending || first.EvidenceCount != 1 || first.ID == 0 {
		t.Fatalf("first candidate added=%v item=%#v", added, first)
	}
	second, added, err := db.StagePromptRuleCandidate(ctx, candidate, PromptRuleCandidateEvidenceInput{
		SourceKind:    PromptRuleCandidateSourcePublicIntelligence,
		SourceRef:     "https://example.test/source",
		SourceRefHash: "0a13e8f01b241c9bdbad93e86f17139497b4313642b81b6f0e755027685e8b48",
	})
	if err != nil {
		t.Fatalf("duplicate stage: %v", err)
	}
	if added || second.ID != first.ID || second.EvidenceCount != 1 {
		t.Fatalf("duplicate evidence added=%v item=%#v", added, second)
	}
	third, added, err := db.StagePromptRuleCandidate(ctx, candidate, PromptRuleCandidateEvidenceInput{
		SourceKind:    PromptRuleCandidateSourcePublicIntelligence,
		SourceRef:     "https://example.test/second",
		SourceRefHash: "6b8d31cbbc69ba9c8c132812b654c1135508df5a3c4c1f74d7035fa38c150e16",
	})
	if err != nil {
		t.Fatalf("new evidence stage: %v", err)
	}
	if !added || third.EvidenceCount != 2 {
		t.Fatalf("new evidence added=%v item=%#v", added, third)
	}
	_, publishedJSON, err := db.PublishPromptRuleCandidate(ctx, first.ID, candidate.RuleJSON, candidate.Name, "", candidate.RuleJSON, nil)
	if err != nil {
		t.Fatalf("publish candidate: %v", err)
	}
	var runtimeRules []map[string]any
	if err := json.Unmarshal([]byte(publishedJSON), &runtimeRules); err != nil || len(runtimeRules) != 1 || runtimeRules[0]["name"] != candidate.Name {
		t.Fatalf("published runtime rules=%s err=%v", publishedJSON, err)
	}
	published, err := db.GetPromptRuleCandidate(ctx, first.ID)
	if err != nil || published.Status != PromptRuleCandidateStatusPublished || published.PublishedAt == nil {
		t.Fatalf("published candidate=%#v err=%v", published, err)
	}
	items, total, err := db.ListPromptRuleCandidates(ctx, PromptRuleCandidateQuery{Status: PromptRuleCandidateStatusPublished, Page: 1, PageSize: 10})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("list total=%d items=%#v err=%v", total, items, err)
	}
	evidence, err := db.ListPromptRuleCandidateEvidence(ctx, first.ID, 10)
	if err != nil || len(evidence) != 2 {
		t.Fatalf("evidence=%#v err=%v", evidence, err)
	}
}

func TestPromptRuleMigrationCompletionIsAtomicWithRuntimeCAS(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "candidate-migration-atomic.sqlite"))
	if err != nil {
		t.Fatalf("New sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	original := `[{"name":"legacy_auto","pattern":"legacy","weight":20,"category":"custom","signal_only":true}]`
	if err := db.ReplacePromptFilterCustomPatterns(ctx, original); err != nil {
		t.Fatalf("seed runtime patterns: %v", err)
	}
	item, _, err := db.StagePromptRuleCandidate(ctx, PromptRuleCandidateInput{
		Fingerprint: strings.Repeat("a", 64), Kind: PromptRuleCandidateKindPattern,
		Source: PromptRuleCandidateSourceLegacyMigration, Name: "legacy_auto", Category: "custom",
		RuleJSON: `{"name":"legacy_auto","pattern":"legacy","weight":20,"category":"custom"}`,
	}, PromptRuleCandidateEvidenceInput{
		SourceKind: PromptRuleCandidateSourceLegacyMigration, SourceRef: "legacy_auto",
		SourceRefHash: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatalf("stage migration candidate: %v", err)
	}
	completion := PromptRuleCandidateMigrationCompletion{
		CandidateID: item.ID,
		Evidence: PromptRuleCandidateEvidenceInput{
			SourceKind: PromptRuleCandidateSourceLegacyMigrationDone, SourceRef: "legacy_auto",
			SourceRefHash: strings.Repeat("c", 64),
		},
	}

	swapped, err := db.CompareAndSwapPromptFilterCustomPatternsWithMigrationCompletions(ctx, `[]`, `[]`, []PromptRuleCandidateMigrationCompletion{completion})
	if err != nil || swapped {
		t.Fatalf("mismatched CAS swapped=%v err=%v", swapped, err)
	}
	hasCompletion, err := db.HasPromptRuleCandidateEvidence(ctx, item.ID, PromptRuleCandidateSourceLegacyMigrationDone, strings.Repeat("c", 64))
	if err != nil || hasCompletion {
		t.Fatalf("failed CAS wrote completion evidence: has=%v err=%v", hasCompletion, err)
	}

	badCompletion := completion
	badCompletion.CandidateID = item.ID + 999
	swapped, err = db.CompareAndSwapPromptFilterCustomPatternsWithMigrationCompletions(ctx, original, `[]`, []PromptRuleCandidateMigrationCompletion{badCompletion})
	if err == nil || swapped {
		t.Fatalf("missing candidate did not roll back CAS: swapped=%v err=%v", swapped, err)
	}
	var current string
	if err := db.conn.QueryRowContext(ctx, `SELECT prompt_filter_custom_patterns FROM system_settings WHERE id=1`).Scan(&current); err != nil || current != original {
		t.Fatalf("failed completion changed runtime patterns: current=%q err=%v", current, err)
	}

	swapped, err = db.CompareAndSwapPromptFilterCustomPatternsWithMigrationCompletions(ctx, original, `[]`, []PromptRuleCandidateMigrationCompletion{completion})
	if err != nil || !swapped {
		t.Fatalf("atomic migration CAS swapped=%v err=%v", swapped, err)
	}
	hasCompletion, err = db.HasPromptRuleCandidateEvidence(ctx, item.ID, PromptRuleCandidateSourceLegacyMigrationDone, strings.Repeat("c", 64))
	if err != nil || !hasCompletion {
		t.Fatalf("successful CAS missed completion evidence: has=%v err=%v", hasCompletion, err)
	}
	got, err := db.GetPromptRuleCandidate(ctx, item.ID)
	if err != nil || got.EvidenceCount != 2 {
		t.Fatalf("completion aggregate item=%#v err=%v", got, err)
	}

	swapped, err = db.CompareAndSwapPromptFilterCustomPatternsWithMigrationCompletions(ctx, `[]`, `[]`, []PromptRuleCandidateMigrationCompletion{completion})
	if err != nil || !swapped {
		t.Fatalf("idempotent completion CAS swapped=%v err=%v", swapped, err)
	}
	got, err = db.GetPromptRuleCandidate(ctx, item.ID)
	if err != nil || got.EvidenceCount != 2 {
		t.Fatalf("duplicate completion inflated evidence: item=%#v err=%v", got, err)
	}
}

func TestPromptRuleCandidateNewestEvidenceWinsWithoutReplayInflation(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "candidate-order.sqlite"))
	if err != nil {
		t.Fatalf("New sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	candidate := PromptRuleCandidateInput{
		Fingerprint: "f3598c2dcad2637b7146aa19e8e1a47d9d9c2b99dfb002d6f607a5714d2e8314",
		Kind:        PromptRuleCandidateKindEvidence, Source: "new-source", SamplePreview: "new preview",
	}
	newer := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	item, added, err := db.StagePromptRuleCandidate(ctx, candidate, PromptRuleCandidateEvidenceInput{
		SourceKind: "new-source", SourceRef: "new", SourceRefHash: "77c47ff1b3db4b71e2fca5682eb488a6c109276f6f55f27e475c663f9fcfad2e",
		SamplePreview: "new preview", ObservedAt: newer,
	})
	if err != nil || !added {
		t.Fatalf("stage newer item=%#v added=%v err=%v", item, added, err)
	}
	older := newer.Add(-time.Hour)
	olderCandidate := candidate
	olderCandidate.Source = "old-source"
	olderCandidate.SamplePreview = "old preview"
	item, added, err = db.StagePromptRuleCandidate(ctx, olderCandidate, PromptRuleCandidateEvidenceInput{
		SourceKind: "old-source", SourceRef: "old", SourceRefHash: "1a6bff7aa37d1af73eb2a2f52fefebc1baf0495d39ab4d42b52e4275bd5d58cc",
		SamplePreview: "old preview", ObservedAt: older,
	})
	if err != nil || !added || item.EvidenceCount != 2 {
		t.Fatalf("stage older item=%#v added=%v err=%v", item, added, err)
	}
	if !item.LastSeenAt.Equal(newer) || item.LastSource != "new-source" || item.SamplePreview != "new preview" {
		t.Fatalf("older evidence replaced newest metadata: %#v", item)
	}
	item, added, err = db.StagePromptRuleCandidate(ctx, candidate, PromptRuleCandidateEvidenceInput{
		SourceKind: "new-source", SourceRef: "new", SourceRefHash: "77c47ff1b3db4b71e2fca5682eb488a6c109276f6f55f27e475c663f9fcfad2e",
		SamplePreview: "replayed preview", ObservedAt: newer.Add(time.Hour),
	})
	if err != nil || added || item.EvidenceCount != 2 || !item.LastSeenAt.Equal(newer) {
		t.Fatalf("replayed evidence changed aggregate: item=%#v added=%v err=%v", item, added, err)
	}
}

func TestPromptRuleCandidatePublishRejectsStaleAndSupersedesSameName(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "candidate-publish.sqlite"))
	if err != nil {
		t.Fatalf("New sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	stage := func(fingerprint, ruleJSON, refHash string) *PromptRuleCandidate {
		item, _, stageErr := db.StagePromptRuleCandidate(ctx, PromptRuleCandidateInput{
			Fingerprint: fingerprint, Kind: PromptRuleCandidateKindPattern, Source: PromptRuleCandidateSourceManual,
			Name: "shared_rule", Category: "custom", RuleJSON: ruleJSON,
		}, PromptRuleCandidateEvidenceInput{SourceKind: PromptRuleCandidateSourceManual, SourceRef: refHash, SourceRefHash: refHash})
		if stageErr != nil {
			t.Fatalf("stage candidate: %v", stageErr)
		}
		return item
	}
	ruleV1 := `{"name":"shared_rule","pattern":"(?i)version-one","weight":50,"category":"custom"}`
	ruleV2 := `{"name":"shared_rule","pattern":"(?i)version-two","weight":80,"category":"custom","strict":true}`
	v1 := stage("52b4fc747f31912b34549ec3a7c50e5fcbcc76dc684f6f5b2710f3bf41d56273", ruleV1, "f3598c2dcad2637b7146aa19e8e1a47d9d9c2b99dfb002d6f607a5714d2e8314")
	v2 := stage("04fd2c654159866a2aa1a0f226c26dfdcd9524e060a28d573ac731f9b5f606c7", ruleV2, "77c47ff1b3db4b71e2fca5682eb488a6c109276f6f55f27e475c663f9fcfad2e")
	if _, _, err := db.PublishPromptRuleCandidate(ctx, v1.ID, ruleV2, "shared_rule", "", ruleV1, nil); err == nil {
		t.Fatal("stale/mismatched proposal was published")
	}
	if _, _, err := db.PublishPromptRuleCandidate(ctx, v1.ID, ruleV1, "shared_rule", "", ruleV1, nil); err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	if _, _, err := db.PublishPromptRuleCandidate(ctx, v2.ID, ruleV2, "shared_rule", ruleV1, ruleV2, nil); err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	gotV1, _ := db.GetPromptRuleCandidate(ctx, v1.ID)
	gotV2, _ := db.GetPromptRuleCandidate(ctx, v2.ID)
	if gotV1.Status != PromptRuleCandidateStatusSuperseded || gotV2.Status != PromptRuleCandidateStatusPublished {
		t.Fatalf("candidate lifecycle v1=%#v v2=%#v", gotV1, gotV2)
	}
	if _, _, err := db.PublishPromptRuleCandidate(ctx, v1.ID, ruleV1, "shared_rule", ruleV2, ruleV1, nil); err == nil {
		t.Fatal("superseded proposal was allowed to roll runtime rules back")
	}
	manualRule := `{"name":"shared_rule","pattern":"(?i)manual-change","weight":90,"category":"custom","strict":true}`
	if err := db.ReplacePromptFilterCustomPatterns(ctx, `[`+manualRule+`]`); err != nil {
		t.Fatalf("replace with manual rule: %v", err)
	}
	if _, _, err := db.PublishPromptRuleCandidate(ctx, v2.ID, ruleV2, "shared_rule", manualRule, ruleV2, nil); !errors.Is(err, ErrPromptRuleCandidateConflict) {
		t.Fatalf("published candidate overwrote later manual edit: %v", err)
	}
	settings, err := db.GetSystemSettings(ctx)
	if err != nil || settings.PromptFilterCustomPatterns != `[`+manualRule+`]` {
		t.Fatalf("manual runtime rule changed: settings=%#v err=%v", settings, err)
	}
}

func TestPromptRuleCandidateValidationFailureRollsBackPublish(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "candidate-validation.sqlite"))
	if err != nil {
		t.Fatalf("New sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.ReplacePromptFilterCustomPatterns(ctx, "[]"); err != nil {
		t.Fatalf("seed runtime settings: %v", err)
	}
	ruleJSON := `{"name":"validation_rule","pattern":"(?i)validation-marker","weight":60,"category":"custom"}`
	item, _, err := db.StagePromptRuleCandidate(ctx, PromptRuleCandidateInput{
		Fingerprint: "52b4fc747f31912b34549ec3a7c50e5fcbcc76dc684f6f5b2710f3bf41d56273",
		Kind:        PromptRuleCandidateKindPattern, Source: PromptRuleCandidateSourceManual,
		Name: "validation_rule", Category: "custom", RuleJSON: ruleJSON,
	}, PromptRuleCandidateEvidenceInput{
		SourceKind: PromptRuleCandidateSourceManual, SourceRef: "validation",
		SourceRefHash: "f3598c2dcad2637b7146aa19e8e1a47d9d9c2b99dfb002d6f607a5714d2e8314",
	})
	if err != nil {
		t.Fatalf("stage candidate: %v", err)
	}
	wantErr := errors.New("merged rule set rejected")
	if _, _, err := db.PublishPromptRuleCandidate(ctx, item.ID, ruleJSON, item.Name, "", ruleJSON, func(string) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("publish validation error=%v, want %v", err, wantErr)
	}
	got, err := db.GetPromptRuleCandidate(ctx, item.ID)
	if err != nil || got.Status != PromptRuleCandidateStatusPending || got.PublishedAt != nil {
		t.Fatalf("candidate committed after validation failure: candidate=%#v err=%v", got, err)
	}
	settings, err := db.GetSystemSettings(ctx)
	if err != nil || settings.PromptFilterCustomPatterns != "[]" {
		t.Fatalf("runtime rules committed after validation failure: settings=%#v err=%v", settings, err)
	}
}

func TestPromptRuleCandidateConcurrentPublishesPreserveDifferentRules(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "candidate-concurrent.sqlite"))
	if err != nil {
		t.Fatalf("New sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	type stagedRule struct {
		candidate *PromptRuleCandidate
		ruleJSON  string
	}
	stage := func(fingerprint, name, pattern, refHash string) stagedRule {
		ruleJSON := `{"name":"` + name + `","pattern":"` + pattern + `","weight":60,"category":"custom"}`
		item, _, stageErr := db.StagePromptRuleCandidate(ctx, PromptRuleCandidateInput{
			Fingerprint: fingerprint, Kind: PromptRuleCandidateKindPattern, Source: PromptRuleCandidateSourceManual,
			Name: name, Category: "custom", RuleJSON: ruleJSON,
		}, PromptRuleCandidateEvidenceInput{SourceKind: PromptRuleCandidateSourceManual, SourceRef: refHash, SourceRefHash: refHash})
		if stageErr != nil {
			t.Fatalf("stage %s: %v", name, stageErr)
		}
		return stagedRule{candidate: item, ruleJSON: ruleJSON}
	}
	first := stage(
		"52b4fc747f31912b34549ec3a7c50e5fcbcc76dc684f6f5b2710f3bf41d56273",
		"concurrent_one", "(?i)concurrent-one", "f3598c2dcad2637b7146aa19e8e1a47d9d9c2b99dfb002d6f607a5714d2e8314",
	)
	second := stage(
		"04fd2c654159866a2aa1a0f226c26dfdcd9524e060a28d573ac731f9b5f606c7",
		"concurrent_two", "(?i)concurrent-two", "77c47ff1b3db4b71e2fca5682eb488a6c109276f6f55f27e475c663f9fcfad2e",
	)

	start := make(chan struct{})
	errorsByRule := make(chan error, 2)
	var wg sync.WaitGroup
	for _, item := range []stagedRule{first, second} {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, publishErr := db.PublishPromptRuleCandidate(ctx, item.candidate.ID, item.ruleJSON, item.candidate.Name, "", item.ruleJSON, nil)
			errorsByRule <- publishErr
		}()
	}
	close(start)
	wg.Wait()
	close(errorsByRule)
	for publishErr := range errorsByRule {
		if publishErr != nil {
			t.Fatalf("concurrent publish: %v", publishErr)
		}
	}

	settings, err := db.GetSystemSettings(ctx)
	if err != nil {
		t.Fatalf("GetSystemSettings: %v", err)
	}
	var rules []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(settings.PromptFilterCustomPatterns), &rules); err != nil {
		t.Fatalf("decode custom patterns: %v", err)
	}
	seen := map[string]bool{}
	for _, rule := range rules {
		seen[rule.Name] = true
	}
	if len(rules) != 2 || !seen[first.candidate.Name] || !seen[second.candidate.Name] {
		t.Fatalf("concurrent rules were lost: %s", settings.PromptFilterCustomPatterns)
	}
}

func TestSystemSettingsUpdateCanPreserveConcurrentPromptRules(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "settings-preserve-rules.sqlite"))
	if err != nil {
		t.Fatalf("New sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	runtimeRules := `[{"name":"concurrent_runtime_rule","pattern":"(?i)runtime-marker","weight":60,"category":"custom"}]`
	if err := db.ReplacePromptFilterCustomPatterns(ctx, runtimeRules); err != nil {
		t.Fatalf("seed runtime rules: %v", err)
	}
	stale, err := db.GetSystemSettings(ctx)
	if err != nil {
		t.Fatalf("GetSystemSettings: %v", err)
	}
	stale.SiteName = "updated without rules"
	stale.PromptFilterCustomPatterns = "[]"
	stale.PreservePromptFilterCustomPatterns = true
	if err := db.UpdateSystemSettings(ctx, stale); err != nil {
		t.Fatalf("preserving settings update: %v", err)
	}
	got, err := db.GetSystemSettings(ctx)
	if err != nil || got.PromptFilterCustomPatterns != runtimeRules {
		t.Fatalf("stale settings update overwrote runtime rules: settings=%#v err=%v", got, err)
	}
	got.PromptFilterCustomPatterns = "[]"
	got.PreservePromptFilterCustomPatterns = false
	if err := db.UpdateSystemSettings(ctx, got); err != nil {
		t.Fatalf("explicit rules update: %v", err)
	}
	got, err = db.GetSystemSettings(ctx)
	if err != nil || got.PromptFilterCustomPatterns != "[]" {
		t.Fatalf("explicit rules update was not applied: settings=%#v err=%v", got, err)
	}
}

func TestPromptRuleCandidateEvidenceAllowsNoPattern(t *testing.T) {
	db, err := New("sqlite", filepath.Join(t.TempDir(), "evidence.sqlite"))
	if err != nil {
		t.Fatalf("New sqlite: %v", err)
	}
	defer db.Close()
	item, added, err := db.StagePromptRuleCandidate(context.Background(), PromptRuleCandidateInput{
		Fingerprint: "0a13e8f01b241c9bdbad93e86f17139497b4313642b81b6f0e755027685e8b48",
		Kind:        PromptRuleCandidateKindEvidence, Source: PromptRuleCandidateSourceUpstreamCyberPolicy,
		SamplePreview: "redacted upstream prompt evidence",
	}, PromptRuleCandidateEvidenceInput{
		SourceKind:    PromptRuleCandidateSourceUpstreamCyberPolicy,
		SourceRef:     "request-1",
		SourceRefHash: "6b8d31cbbc69ba9c8c132812b654c1135508df5a3c4c1f74d7035fa38c150e16",
		MetadataJSON:  `{"error_code":"cyber_policy"}`,
	})
	if err != nil || !added {
		t.Fatalf("stage evidence added=%v err=%v", added, err)
	}
	if item.Kind != PromptRuleCandidateKindEvidence || item.RuleJSON != "{}" || item.Status != PromptRuleCandidateStatusPending {
		t.Fatalf("evidence candidate = %#v", item)
	}
}
