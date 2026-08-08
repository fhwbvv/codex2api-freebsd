package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/database"
	"github.com/codex2api/proxy"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

func TestSearchGitHubPromptIntelligence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "jailbreak prompt" {
			t.Fatalf("query = %q", r.URL.Query().Get("q"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"full_name":"owner/repo","html_url":"https://github.com/owner/repo","description":"prompt injection research","updated_at":"2026-07-15T00:00:00Z"}]}`))
	}))
	defer server.Close()
	old := githubPromptSearchBaseURL
	githubPromptSearchBaseURL = server.URL
	defer func() { githubPromptSearchBaseURL = old }()
	items, err := searchGitHubPromptIntelligence(context.Background(), "jailbreak prompt", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "owner/repo" {
		t.Fatalf("items = %#v", items)
	}
}

func TestValidateIntelligenceCandidate(t *testing.T) {
	valid := promptIntelligenceCandidate{Name: "new_jailbreak_phrase", Pattern: `(?i)ignore\s+safety`, Weight: 80, Category: "prompt_injection", Strict: true}
	if err := validateIntelligenceCandidate(valid); err != nil {
		t.Fatal(err)
	}
	valid.Pattern = "("
	if err := validateIntelligenceCandidate(valid); err == nil {
		t.Fatal("invalid regexp accepted")
	}
	valid.Pattern = `(?s).*`
	if err := validateIntelligenceCandidate(valid); err == nil {
		t.Fatal("match-all regexp accepted")
	}
	valid.Name = "all"
	valid.Pattern = `(?i)\ball\b`
	if err := validateIntelligenceCandidate(valid); err == nil {
		t.Fatal("generic all regexp accepted")
	}
	if !intelligencePatternHasRiskSignal(`(?i)generate\s+and\s+execute\s+(?:a\s+)?reverse\s+shell`) {
		t.Fatal("known high-risk signal was not recognized")
	}
	if intelligencePatternHasRiskSignal(`(?i)reverse\s+shell`) {
		t.Fatal("partial risk phrase was accepted for automatic rule admission")
	}
	if intelligencePatternHasRiskSignal(`[sS][tT][rR]`) {
		t.Fatal("ordinary code fragment passed the risk corpus by substring")
	}
	if intelligencePatternHasRiskSignal(`(?i)quarterly\s+report`) {
		t.Fatal("benign-only candidate passed the risk corpus")
	}
}

func TestIntelligenceCandidateDoesNotEnterRuntimeUntilPublished(t *testing.T) {
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	settings.PromptFilterEnabled = true
	settings.PromptFilterCustomPatterns = "[]"
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")
	candidate := promptIntelligenceCandidate{
		Name: "quartzowl_operation", Pattern: `(?i)execute\s+quartzowl9281\s+operation`,
		Weight: 100, Category: "custom", Strict: true, ChangeType: "new",
	}
	before := promptfilter.InspectText("execute quartzowl9281 operation", store.GetPromptFilterConfig())
	if before.Action != promptfilter.ActionAllow {
		t.Fatalf("fixture is not clean before staging: %#v", before)
	}
	staged, err := handler.stagePromptIntelligenceCandidates(context.Background(), []promptIntelligenceCandidate{candidate}, database.PromptRuleCandidateSourceManual, false)
	if err != nil || len(staged) != 1 {
		t.Fatalf("stage candidates=%#v err=%v", staged, err)
	}
	if got := store.GetPromptFilterConfig().CustomPatterns; len(got) != 0 {
		t.Fatalf("pending candidate entered runtime config: %#v", got)
	}
	afterStage := promptfilter.InspectText("execute quartzowl9281 operation", store.GetPromptFilterConfig())
	if afterStage.Action != before.Action || afterStage.Score != before.Score || len(afterStage.Matched) != len(before.Matched) {
		t.Fatalf("staging changed runtime verdict: before=%#v after=%#v", before, afterStage)
	}
	published, added, updated, err := handler.publishPromptIntelligenceCandidate(context.Background(), staged[0].ID)
	if err != nil || published.Status != database.PromptRuleCandidateStatusPublished || added != 1 || updated != 0 {
		t.Fatalf("publish candidate=%#v added=%d updated=%d err=%v", published, added, updated, err)
	}
	afterPublish := promptfilter.InspectText("execute quartzowl9281 operation", store.GetPromptFilterConfig())
	if afterPublish.Action != promptfilter.ActionBlock {
		t.Fatalf("published rule did not enter runtime: %#v", afterPublish)
	}
	_, added, updated, err = handler.publishPromptIntelligenceCandidate(context.Background(), staged[0].ID)
	if err != nil || added != 0 || updated != 0 || len(store.GetPromptFilterConfig().CustomPatterns) != 1 {
		t.Fatalf("repeated publish is not idempotent: added=%d updated=%d err=%v rules=%#v", added, updated, err, store.GetPromptFilterConfig().CustomPatterns)
	}
}

func TestIntelligenceCandidateRevisionUsesFullProposalFingerprint(t *testing.T) {
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")
	base := promptIntelligenceCandidate{Name: "revision_rule", Pattern: `(?i)revision-marker`, Weight: 20, Category: "custom"}
	updated := base
	updated.Weight = 80
	updated.Strict = true
	first, err := handler.stagePromptIntelligenceCandidates(context.Background(), []promptIntelligenceCandidate{base}, database.PromptRuleCandidateSourceManual, false)
	if err != nil || len(first) != 1 {
		t.Fatalf("stage first=%#v err=%v", first, err)
	}
	second, err := handler.stagePromptIntelligenceCandidates(context.Background(), []promptIntelligenceCandidate{updated}, database.PromptRuleCandidateSourceManual, false)
	if err != nil || len(second) != 1 {
		t.Fatalf("stage second=%#v err=%v", second, err)
	}
	if first[0].Fingerprint == second[0].Fingerprint || first[0].ID == second[0].ID {
		t.Fatalf("proposal revisions collapsed: first=%#v second=%#v", first[0], second[0])
	}
	replayed, err := handler.stagePromptIntelligenceCandidates(context.Background(), []promptIntelligenceCandidate{updated}, database.PromptRuleCandidateSourceManual, false)
	if err != nil || len(replayed) != 0 {
		t.Fatalf("unchanged candidate evidence was shown as a new update: replayed=%#v err=%v", replayed, err)
	}
	_, total, err := db.ListPromptRuleCandidates(context.Background(), database.PromptRuleCandidateQuery{Status: database.PromptRuleCandidateStatusPending})
	if err != nil || total != 2 {
		t.Fatalf("revision candidates total=%d err=%v", total, err)
	}
}

func TestIntelligenceComparisonIgnoresExplicitEnabledTrueSerialization(t *testing.T) {
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	enabled := true
	settings.PromptFilterCustomPatterns = promptfilter.MarshalCustomPatterns([]promptfilter.PatternConfig{{
		Name: "existing_active_rule", Pattern: `(?i)existing-active-marker`, Weight: 70,
		Category: "custom", Strict: true, Enabled: &enabled,
	}})
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")
	proposal := promptIntelligenceCandidate{
		Name: "existing_active_rule", Pattern: `(?i)existing-active-marker`, Weight: 70,
		Category: "custom", Strict: true,
	}
	if got := handler.comparePromptIntelligenceCandidates([]promptIntelligenceCandidate{proposal}); len(got) != 0 {
		t.Fatalf("serialization-only enabled=true difference entered review queue: %#v", got)
	}
	proposal.Weight = 80
	if got := handler.comparePromptIntelligenceCandidates([]promptIntelligenceCandidate{proposal}); len(got) != 1 || got[0].ChangeType != "update" {
		t.Fatalf("material rule update was hidden: %#v", got)
	}
}

func TestPromptRuleRuntimeSyncLoadsOnlyPublishedSettingsSnapshot(t *testing.T) {
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	settings.PromptFilterCustomPatterns = "[]"
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")
	published := []promptfilter.PatternConfig{{
		Name: "replica_synced_rule", Pattern: `(?i)replica-synced-marker`, Weight: 70,
		Category: "custom", Strict: true,
	}}
	raw := promptfilter.MarshalCustomPatterns(published)
	if err := db.ReplacePromptFilterCustomPatterns(context.Background(), raw); err != nil {
		t.Fatalf("publish external runtime snapshot: %v", err)
	}
	if err := handler.syncPromptRuleRuntimeFromDB(context.Background()); err != nil {
		t.Fatalf("sync runtime rules: %v", err)
	}
	got := store.GetPromptFilterConfig().CustomPatterns
	if len(got) != 1 || got[0].Name != published[0].Name {
		t.Fatalf("published runtime snapshot was not synced: %#v", got)
	}
	if candidates, total, err := db.ListPromptRuleCandidates(context.Background(), database.PromptRuleCandidateQuery{Page: 1, PageSize: 10}); err != nil || total != 0 || len(candidates) != 0 {
		t.Fatalf("runtime sync created or consumed candidate rows: candidates=%#v total=%d err=%v", candidates, total, err)
	}

	// A local publish can move the Store to R2 while another replica moves the
	// database back to the earlier R1 snapshot before the next poll. Sync must
	// compare against the current Store, not a goroutine-local lastRaw cache.
	localOnly := []promptfilter.PatternConfig{{
		Name: "local_only_rule", Pattern: `(?i)local-only-marker`, Weight: 60, Category: "custom",
	}}
	cfg := store.GetPromptFilterConfig()
	cfg.CustomPatterns = localOnly
	store.SetPromptFilterConfig(cfg)
	if err := handler.syncPromptRuleRuntimeFromDB(context.Background()); err != nil {
		t.Fatalf("sync database rollback snapshot: %v", err)
	}
	got = store.GetPromptFilterConfig().CustomPatterns
	if len(got) != 1 || got[0].Name != published[0].Name {
		t.Fatalf("runtime sync skipped DB rollback to an earlier snapshot: %#v", got)
	}
}

func TestPromptIntelligenceCandidateHTTPLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	settings := defaultBootstrapSettings()
	settings.PromptFilterEnabled = true
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")
	patternCandidates, err := handler.stagePromptIntelligenceCandidates(context.Background(), []promptIntelligenceCandidate{{
		Name: "http_candidate", Pattern: `(?i)execute\s+http-candidate`, Weight: 100, Category: "custom", Strict: true,
	}}, database.PromptRuleCandidateSourceManual, false)
	if err != nil || len(patternCandidates) != 1 {
		t.Fatalf("stage pattern=%#v err=%v", patternCandidates, err)
	}
	evidence, _, err := db.StagePromptRuleCandidate(context.Background(), database.PromptRuleCandidateInput{
		Fingerprint: "0a13e8f01b241c9bdbad93e86f17139497b4313642b81b6f0e755027685e8b48",
		Kind:        database.PromptRuleCandidateKindEvidence, Source: database.PromptRuleCandidateSourceUpstreamCyberPolicy, SamplePreview: "redacted evidence",
	}, database.PromptRuleCandidateEvidenceInput{
		SourceKind: database.PromptRuleCandidateSourceUpstreamCyberPolicy, SourceRef: "request-http",
		SourceRefHash: "6b8d31cbbc69ba9c8c132812b654c1135508df5a3c4c1f74d7035fa38c150e16",
	})
	if err != nil {
		t.Fatalf("stage evidence: %v", err)
	}
	request := func(method, target string, params gin.Params, invoke func(*gin.Context)) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Params = params
		ctx.Request = httptest.NewRequest(method, target, nil)
		invoke(ctx)
		return recorder
	}
	list := request(http.MethodGet, "/api/admin/prompt-filter/intelligence/candidates?status=pending", nil, handler.ListPromptIntelligenceCandidates)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"total":2`) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	evidenceID := strconv.FormatInt(evidence.ID, 10)
	detail := request(http.MethodGet, "/api/admin/prompt-filter/intelligence/candidates/"+evidenceID+"/evidence", gin.Params{{Key: "id", Value: evidenceID}}, handler.GetPromptIntelligenceCandidateEvidence)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "request-http") {
		t.Fatalf("evidence status=%d body=%s", detail.Code, detail.Body.String())
	}
	evidencePublish := request(http.MethodPost, "/api/admin/prompt-filter/intelligence/candidates/"+evidenceID+"/publish", gin.Params{{Key: "id", Value: evidenceID}}, handler.PublishPromptIntelligenceCandidate)
	if evidencePublish.Code != http.StatusBadRequest {
		t.Fatalf("evidence publish status=%d body=%s", evidencePublish.Code, evidencePublish.Body.String())
	}
	draftRecorder := httptest.NewRecorder()
	draftContext, _ := gin.CreateTestContext(draftRecorder)
	draftContext.Params = gin.Params{{Key: "id", Value: evidenceID}}
	draftContext.Request = httptest.NewRequest(http.MethodPost, "/api/admin/prompt-filter/intelligence/candidates/"+evidenceID+"/draft", strings.NewReader(`{"name":"cy_http_draft","pattern":"(?i)steal\\s+http-candidate\\s+credentials","weight":85,"category":"credential_theft","strict":true,"rationale":"human reviewed CY evidence"}`))
	draftContext.Request.Header.Set("Content-Type", "application/json")
	handler.CreatePromptIntelligenceCandidateDraft(draftContext)
	if draftRecorder.Code != http.StatusCreated {
		t.Fatalf("draft status=%d body=%s", draftRecorder.Code, draftRecorder.Body.String())
	}
	var draftResponse struct {
		Candidate promptIntelligenceCandidate `json:"candidate"`
	}
	if err := json.Unmarshal(draftRecorder.Body.Bytes(), &draftResponse); err != nil || draftResponse.Candidate.ID == 0 || draftResponse.Candidate.Kind != database.PromptRuleCandidateKindPattern {
		t.Fatalf("draft response=%s err=%v", draftRecorder.Body.String(), err)
	}
	draftID := strconv.FormatInt(draftResponse.Candidate.ID, 10)
	draftPublish := request(http.MethodPost, "/api/admin/prompt-filter/intelligence/candidates/"+draftID+"/publish", gin.Params{{Key: "id", Value: draftID}}, handler.PublishPromptIntelligenceCandidate)
	if draftPublish.Code != http.StatusOK {
		t.Fatalf("draft publish status=%d body=%s", draftPublish.Code, draftPublish.Body.String())
	}
	patternID := strconv.FormatInt(patternCandidates[0].ID, 10)
	patternPublish := request(http.MethodPost, "/api/admin/prompt-filter/intelligence/candidates/"+patternID+"/publish", gin.Params{{Key: "id", Value: patternID}}, handler.PublishPromptIntelligenceCandidate)
	if patternPublish.Code != http.StatusOK {
		t.Fatalf("pattern publish status=%d body=%s", patternPublish.Code, patternPublish.Body.String())
	}
	publishedDismiss := request(http.MethodPost, "/api/admin/prompt-filter/intelligence/candidates/"+patternID+"/dismiss", gin.Params{{Key: "id", Value: patternID}}, handler.DismissPromptIntelligenceCandidate)
	if publishedDismiss.Code != http.StatusConflict {
		t.Fatalf("published dismiss status=%d body=%s", publishedDismiss.Code, publishedDismiss.Body.String())
	}
	evidenceDismiss := request(http.MethodPost, "/api/admin/prompt-filter/intelligence/candidates/"+evidenceID+"/dismiss", gin.Params{{Key: "id", Value: evidenceID}}, handler.DismissPromptIntelligenceCandidate)
	if evidenceDismiss.Code != http.StatusOK {
		t.Fatalf("evidence dismiss status=%d body=%s", evidenceDismiss.Code, evidenceDismiss.Body.String())
	}
	missingID := "999999"
	missingDetail := request(http.MethodGet, "/api/admin/prompt-filter/intelligence/candidates/"+missingID+"/evidence", gin.Params{{Key: "id", Value: missingID}}, handler.GetPromptIntelligenceCandidateEvidence)
	if missingDetail.Code != http.StatusNotFound {
		t.Fatalf("missing detail status=%d body=%s", missingDetail.Code, missingDetail.Body.String())
	}
	missingPublish := request(http.MethodPost, "/api/admin/prompt-filter/intelligence/candidates/"+missingID+"/publish", gin.Params{{Key: "id", Value: missingID}}, handler.PublishPromptIntelligenceCandidate)
	if missingPublish.Code != http.StatusNotFound {
		t.Fatalf("missing publish status=%d body=%s", missingPublish.Code, missingPublish.Body.String())
	}
	missingDismiss := request(http.MethodPost, "/api/admin/prompt-filter/intelligence/candidates/"+missingID+"/dismiss", gin.Params{{Key: "id", Value: missingID}}, handler.DismissPromptIntelligenceCandidate)
	if missingDismiss.Code != http.StatusNotFound {
		t.Fatalf("missing dismiss status=%d body=%s", missingDismiss.Code, missingDismiss.Body.String())
	}
}

func TestLegacyAutomaticIntelligenceMigrationKeepsManualSignalOnlyRule(t *testing.T) {
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	automatic := promptfilter.PatternConfig{
		Name: "legacy_auto_reverse_shell", Pattern: `(?i)generate\s+and\s+execute\s+(?:a\s+)?reverse\s+shell`,
		Weight: 20, Category: "remote_access", SignalOnly: true,
	}
	manual := promptfilter.PatternConfig{Name: "manual_signal_rule", Pattern: `(?i)manual\s+signal\s+marker`, Weight: 20, Category: "custom", SignalOnly: true}
	settings := defaultBootstrapSettings()
	settings.PromptFilterCustomPatterns = promptfilter.MarshalCustomPatterns([]promptfilter.PatternConfig{automatic, manual})
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	logged, _ := json.Marshal([]promptfilter.PatternConfig{automatic})
	if err := db.InsertPromptFilterLog(context.Background(), &database.PromptFilterLogInput{
		Source: "intel_rule_add", Endpoint: "prompt_intelligence", Action: "added_or_updated", Mode: "audit", MatchedPatterns: "[]", FullText: string(logged),
	}); err != nil {
		t.Fatalf("seed intelligence log: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")
	if err := handler.migrateLegacyAutomaticIntelligenceRules(context.Background()); err != nil {
		t.Fatalf("migrate legacy automatic rules: %v", err)
	}
	rules := store.GetPromptFilterConfig().CustomPatterns
	if len(rules) != 1 || rules[0].Name != manual.Name {
		t.Fatalf("migration removed the wrong rules: %#v", rules)
	}
	candidates, total, err := db.ListPromptRuleCandidates(context.Background(), database.PromptRuleCandidateQuery{Status: database.PromptRuleCandidateStatusPending})
	if err != nil || total != 1 || len(candidates) != 1 || candidates[0].Name != automatic.Name {
		t.Fatalf("migrated candidates total=%d items=%#v err=%v", total, candidates, err)
	}
	if err := handler.migrateLegacyAutomaticIntelligenceRules(context.Background()); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
	candidates, total, err = db.ListPromptRuleCandidates(context.Background(), database.PromptRuleCandidateQuery{Status: database.PromptRuleCandidateStatusPending})
	if err != nil || total != 1 || candidates[0].EvidenceCount != 2 {
		t.Fatalf("migration duplicated evidence: total=%d items=%#v err=%v", total, candidates, err)
	}
	legacyLogs, _, err := db.ListPromptFilterLogsPage(context.Background(), database.PromptFilterLogQuery{Page: 1, PageSize: 10, Source: "intel_rule_add"})
	if err != nil || len(legacyLogs) == 0 {
		t.Fatalf("read legacy generation log: logs=%#v err=%v", legacyLogs, err)
	}
	completionRef := automatic.Name + "\x00" + automatic.Pattern + "\x00" + strconv.FormatInt(legacyLogs[0].ID, 10)
	completionHash := promptfilter.StableEvidenceFingerprint("evidence-ref", database.PromptRuleCandidateSourceLegacyMigrationDone+"\x00"+completionRef)
	completed, err := db.HasPromptRuleCandidateEvidence(context.Background(), candidates[0].ID, database.PromptRuleCandidateSourceLegacyMigrationDone, completionHash)
	if err != nil || !completed {
		t.Fatalf("durable completion evidence missing: completed=%v err=%v", completed, err)
	}

	// A later administrator restore of the exact historical signal rule is a
	// deliberate manual action. The durable migration evidence must prevent a
	// future restart from deleting it again.
	restored := []promptfilter.PatternConfig{automatic, manual}
	if err := db.ReplacePromptFilterCustomPatterns(context.Background(), promptfilter.MarshalCustomPatterns(restored)); err != nil {
		t.Fatalf("restore rules in DB: %v", err)
	}
	cfg := store.GetPromptFilterConfig()
	cfg.CustomPatterns = restored
	store.SetPromptFilterConfig(cfg)
	if err := handler.migrateLegacyAutomaticIntelligenceRules(context.Background()); err != nil {
		t.Fatalf("migration after manual restore: %v", err)
	}
	if rules := store.GetPromptFilterConfig().CustomPatterns; len(rules) != 2 {
		t.Fatalf("durable migration marker did not preserve manual restore: %#v", rules)
	}

	// During a blue-green rollout an older instance may still run the removed
	// AutoAdd path. A newer legacy write log is a new migration generation and
	// must remove the automatically reintroduced rule again.
	if err := db.InsertPromptFilterLog(context.Background(), &database.PromptFilterLogInput{
		Source: "intel_rule_add", Endpoint: "prompt_intelligence", Action: "added_or_updated", Mode: "audit", MatchedPatterns: "[]", FullText: string(logged),
	}); err != nil {
		t.Fatalf("seed newer legacy intelligence log: %v", err)
	}
	if err := handler.migrateLegacyAutomaticIntelligenceRules(context.Background()); err != nil {
		t.Fatalf("migration after old replica reintroduced a rule: %v", err)
	}
	if rules := store.GetPromptFilterConfig().CustomPatterns; len(rules) != 1 || rules[0].Name != manual.Name {
		t.Fatalf("new legacy generation was mistaken for a manual restore: %#v", rules)
	}

	// Once that newer generation is completed, a subsequent restore without a
	// newer legacy log is again treated as an intentional administrator action.
	if err := db.ReplacePromptFilterCustomPatterns(context.Background(), promptfilter.MarshalCustomPatterns(restored)); err != nil {
		t.Fatalf("restore rules after newer migration generation: %v", err)
	}
	cfg = store.GetPromptFilterConfig()
	cfg.CustomPatterns = restored
	store.SetPromptFilterConfig(cfg)
	if err := handler.migrateLegacyAutomaticIntelligenceRules(context.Background()); err != nil {
		t.Fatalf("migration after second manual restore: %v", err)
	}
	if rules := store.GetPromptFilterConfig().CustomPatterns; len(rules) != 2 {
		t.Fatalf("new generation marker did not preserve later manual restore: %#v", rules)
	}
}

func TestLegacyAutomaticIntelligenceMigrationPreservesAdministrativelyModifiedRule(t *testing.T) {
	base := promptfilter.PatternConfig{
		Name: "legacy_modified_reverse_shell", Pattern: `(?i)generate\s+and\s+execute\s+(?:a\s+)?reverse\s+shell`,
		Weight: 20, Category: "remote_access", SignalOnly: true,
	}
	tests := []struct {
		name   string
		modify func(*promptfilter.PatternConfig)
	}{
		{
			name: "disabled",
			modify: func(pattern *promptfilter.PatternConfig) {
				disabled := false
				pattern.Enabled = &disabled
			},
		},
		{
			name: "composite exclusions",
			modify: func(pattern *promptfilter.PatternConfig) {
				pattern.ExcludePatterns = []string{`(?i)authorized\s+defensive\s+lab`}
				pattern.MinMatches = 1
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestAdminDB(t)
			cacheStore := cache.NewMemory(4)
			t.Cleanup(func() { _ = cacheStore.Close() })
			current := base
			tc.modify(&current)
			settings := defaultBootstrapSettings()
			settings.PromptFilterCustomPatterns = promptfilter.MarshalCustomPatterns([]promptfilter.PatternConfig{current})
			if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
				t.Fatalf("seed settings: %v", err)
			}
			logged, _ := json.Marshal([]promptfilter.PatternConfig{base})
			if err := db.InsertPromptFilterLog(context.Background(), &database.PromptFilterLogInput{
				Source: "intel_rule_add", Endpoint: "prompt_intelligence", Action: "added_or_updated", Mode: "audit", MatchedPatterns: "[]", FullText: string(logged),
			}); err != nil {
				t.Fatalf("seed intelligence log: %v", err)
			}
			store := auth.NewStore(db, cacheStore, settings)
			t.Cleanup(store.Stop)
			handler := NewHandler(store, db, cacheStore, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")
			if err := handler.migrateLegacyAutomaticIntelligenceRules(context.Background()); err != nil {
				t.Fatalf("migrate modified automatic rule: %v", err)
			}
			persisted, err := db.GetSystemSettings(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			patterns, err := promptfilter.ParseCustomPatterns(persisted.PromptFilterCustomPatterns)
			if err != nil || len(patterns) != 1 || !legacyAutomaticPatternEquivalent(patterns[0], current) {
				t.Fatalf("administratively modified rule was removed or changed: patterns=%#v err=%v", patterns, err)
			}
			if candidates, total, err := db.ListPromptRuleCandidates(context.Background(), database.PromptRuleCandidateQuery{Page: 1, PageSize: 10}); err != nil || total != 0 || len(candidates) != 0 {
				t.Fatalf("modified rule was incorrectly staged: candidates=%#v total=%d err=%v", candidates, total, err)
			}
		})
	}
}

func TestLegacyAutomaticIntelligenceMigrationContinuesAfterCandidateOnlyInterruption(t *testing.T) {
	db := newTestAdminDB(t)
	tc := cache.NewMemory(4)
	t.Cleanup(func() { _ = tc.Close() })
	automatic := promptfilter.PatternConfig{
		Name: "legacy_interrupted_reverse_shell", Pattern: `(?i)generate\s+and\s+execute\s+(?:a\s+)?reverse\s+shell`,
		Weight: 20, Category: "remote_access", SignalOnly: true,
	}
	settings := defaultBootstrapSettings()
	settings.PromptFilterCustomPatterns = promptfilter.MarshalCustomPatterns([]promptfilter.PatternConfig{automatic})
	if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	logged, _ := json.Marshal([]promptfilter.PatternConfig{automatic})
	if err := db.InsertPromptFilterLog(context.Background(), &database.PromptFilterLogInput{
		Source: "intel_rule_add", Endpoint: "prompt_intelligence", Action: "added_or_updated", Mode: "audit", MatchedPatterns: "[]", FullText: string(logged),
	}); err != nil {
		t.Fatalf("seed intelligence log: %v", err)
	}
	store := auth.NewStore(db, tc, settings)
	t.Cleanup(store.Stop)
	handler := NewHandler(store, db, tc, proxy.NewRateLimiter(settings.GlobalRPM), "admin-secret")

	// Simulate a process stopping after candidate/evidence staging but before
	// the legacy runtime rule was removed. This evidence is not completion.
	proposal := promptIntelligenceCandidate{
		Name: automatic.Name, Pattern: automatic.Pattern, Weight: automatic.Weight,
		Category: automatic.Category, Strict: false, ChangeType: "new",
		Rationale: "从历史自动规则迁移至待审核候选",
	}
	staged, err := handler.stagePromptIntelligenceCandidates(context.Background(), []promptIntelligenceCandidate{proposal}, database.PromptRuleCandidateSourceLegacyMigration, false)
	if err != nil || len(staged) != 1 {
		t.Fatalf("simulate candidate-only interruption: staged=%#v err=%v", staged, err)
	}
	legacyLogs, _, err := db.ListPromptFilterLogsPage(context.Background(), database.PromptFilterLogQuery{Page: 1, PageSize: 10, Source: "intel_rule_add"})
	if err != nil || len(legacyLogs) == 0 {
		t.Fatalf("read legacy generation log: logs=%#v err=%v", legacyLogs, err)
	}
	completionRef := automatic.Name + "\x00" + automatic.Pattern + "\x00" + strconv.FormatInt(legacyLogs[0].ID, 10)
	completionHash := promptfilter.StableEvidenceFingerprint("evidence-ref", database.PromptRuleCandidateSourceLegacyMigrationDone+"\x00"+completionRef)
	completed, err := db.HasPromptRuleCandidateEvidence(context.Background(), staged[0].ID, database.PromptRuleCandidateSourceLegacyMigrationDone, completionHash)
	if err != nil || completed {
		t.Fatalf("staging evidence was treated as completion: completed=%v err=%v", completed, err)
	}

	if err := handler.migrateLegacyAutomaticIntelligenceRules(context.Background()); err != nil {
		t.Fatalf("resume interrupted migration: %v", err)
	}
	if rules := store.GetPromptFilterConfig().CustomPatterns; len(rules) != 0 {
		t.Fatalf("interrupted migration did not remove legacy runtime rule: %#v", rules)
	}
	completed, err = db.HasPromptRuleCandidateEvidence(context.Background(), staged[0].ID, database.PromptRuleCandidateSourceLegacyMigrationDone, completionHash)
	if err != nil || !completed {
		t.Fatalf("resumed migration missed completion evidence: completed=%v err=%v", completed, err)
	}
	item, err := db.GetPromptRuleCandidate(context.Background(), staged[0].ID)
	if err != nil || item.EvidenceCount != 2 {
		t.Fatalf("resumed migration evidence item=%#v err=%v", item, err)
	}

	if err := db.ReplacePromptFilterCustomPatterns(context.Background(), promptfilter.MarshalCustomPatterns([]promptfilter.PatternConfig{automatic})); err != nil {
		t.Fatalf("restore migrated rule in DB: %v", err)
	}
	cfg := store.GetPromptFilterConfig()
	cfg.CustomPatterns = []promptfilter.PatternConfig{automatic}
	store.SetPromptFilterConfig(cfg)
	if err := handler.migrateLegacyAutomaticIntelligenceRules(context.Background()); err != nil {
		t.Fatalf("migration after interrupted-path manual restore: %v", err)
	}
	if rules := store.GetPromptFilterConfig().CustomPatterns; len(rules) != 1 || rules[0].Name != automatic.Name {
		t.Fatalf("completion marker did not preserve interrupted-path manual restore: %#v", rules)
	}
}

func TestMergeIntelligenceQueriesIncludesChineseBuiltins(t *testing.T) {
	queries := mergeIntelligenceQueries(defaultIntelligenceQueries, []string{"custom query", "GPT 破甲 提示词"})
	want := map[string]bool{"大模型 破限 提示词": false, "GPT 破甲 提示词": false, "AI 越狱 提示词": false, "custom query": false}
	for _, query := range queries {
		if _, ok := want[query]; ok {
			want[query] = true
		}
	}
	for query, found := range want {
		if !found {
			t.Fatalf("missing query %q in %#v", query, queries)
		}
	}
}
