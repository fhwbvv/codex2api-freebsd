package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/codex2api/database"
	"github.com/codex2api/proxy"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

const promptFilterAuditContextMaxRunes = 2000

type promptFilterLogsResponse struct {
	Logs     []*database.PromptFilterLog `json:"logs"`
	Total    int                         `json:"total"`
	Page     int                         `json:"page"`
	PageSize int                         `json:"page_size"`
}

type promptPolicyIncidentsResponse struct {
	Incidents []*database.PromptPolicyIncident `json:"incidents"`
	Total     int                              `json:"total"`
	Page      int                              `json:"page"`
	PageSize  int                              `json:"page_size"`
}

type promptPolicyIncidentDetailResponse struct {
	Incident  *database.PromptPolicyIncident        `json:"incident"`
	Matches   json.RawMessage                       `json:"matches"`
	Candidate *database.PromptRuleCandidate         `json:"candidate,omitempty"`
	Evidence  *database.PromptRuleCandidateEvidence `json:"evidence,omitempty"`
}

type promptFilterTestRequest struct {
	Text     string `json:"text"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
}

type promptFilterTestResponse struct {
	Verdict  promptfilter.Verdict     `json:"verdict"`
	Decision promptfilter.Decision    `json:"decision"`
	Protocol promptfilter.Protocol    `json:"protocol"`
	Provider promptfilter.ModelFamily `json:"provider"`
	Endpoint string                   `json:"endpoint"`
	Model    string                   `json:"model"`
}

type promptReviewTestRequest struct {
	Text                 string             `json:"text"`
	APIKey               string             `json:"api_key"`
	BaseURL              string             `json:"base_url"`
	Model                string             `json:"model"`
	RequestMode          string             `json:"request_mode"`
	SystemPrompt         string             `json:"system_prompt"`
	UserPromptTemplate   string             `json:"user_prompt_template"`
	PayloadTemplate      string             `json:"payload_template"`
	ConfidenceThreshold  float64            `json:"confidence_threshold"`
	ModerationThresholds map[string]float64 `json:"moderation_thresholds"`
	TimeoutSeconds       int                `json:"timeout_seconds"`
	MaxConcurrent        int                `json:"max_concurrent"`
	MaxTextLength        int                `json:"max_text_length"`
	TestAllKeys          bool               `json:"test_all_keys"`
}

type promptReviewKeyTestResult struct {
	KeyIndex             int                `json:"key_index"`
	OK                   bool               `json:"ok"`
	Endpoint             string             `json:"endpoint,omitempty"`
	Model                string             `json:"model,omitempty"`
	Flagged              bool               `json:"flagged"`
	Confidence           float64            `json:"confidence"`
	Reason               string             `json:"reason,omitempty"`
	HighestCategory      string             `json:"highest_category,omitempty"`
	DecisionCategory     string             `json:"decision_category,omitempty"`
	DecisionScore        float64            `json:"decision_score"`
	DecisionThreshold    float64            `json:"decision_threshold"`
	CategoryScores       map[string]float64 `json:"category_scores,omitempty"`
	ModerationThresholds map[string]float64 `json:"moderation_thresholds,omitempty"`
	LatencyMS            int64              `json:"latency_ms"`
	Error                string             `json:"error,omitempty"`
}

type promptReviewTestResponse struct {
	OK                   bool                        `json:"ok"`
	Endpoint             string                      `json:"endpoint"`
	Model                string                      `json:"model"`
	Flagged              bool                        `json:"flagged"`
	Confidence           float64                     `json:"confidence"`
	ConfidenceThreshold  float64                     `json:"confidence_threshold"`
	Reason               string                      `json:"reason,omitempty"`
	HighestCategory      string                      `json:"highest_category,omitempty"`
	DecisionCategory     string                      `json:"decision_category,omitempty"`
	DecisionScore        float64                     `json:"decision_score"`
	DecisionThreshold    float64                     `json:"decision_threshold"`
	CategoryScores       map[string]float64          `json:"category_scores,omitempty"`
	ModerationThresholds map[string]float64          `json:"moderation_thresholds,omitempty"`
	LatencyMS            int64                       `json:"latency_ms"`
	KeyCount             int                         `json:"key_count,omitempty"`
	Results              []promptReviewKeyTestResult `json:"results,omitempty"`
}

type promptFilterRulePatternTestRequest struct {
	Pattern string `json:"pattern"`
	Text    string `json:"text"`
}

type promptFilterRulePatternTestResponse struct {
	Matched bool   `json:"matched"`
	Error   string `json:"error,omitempty"`
}

type promptFilterRuleItem struct {
	Name     string `json:"name"`
	Pattern  string `json:"pattern"`
	Weight   int    `json:"weight"`
	Category string `json:"category,omitempty"`
	Strict   bool   `json:"strict,omitempty"`
	Enabled  bool   `json:"enabled"`
	Builtin  bool   `json:"builtin"`
}

type promptFilterRulesResponse struct {
	BuiltinPatterns  []promptFilterRuleItem       `json:"builtin_patterns"`
	CustomPatterns   []promptfilter.PatternConfig `json:"custom_patterns"`
	DisabledPatterns []string                     `json:"disabled_patterns"`
}

func (h *Handler) inspectImageStudioPromptFilter(c *gin.Context, text string, model string, keyID int64, keyName string, keyMasked string) bool {
	return h.inspectImagePromptFilter(c, text, model, keyID, keyName, keyMasked, "/api/admin/images/jobs", nil, false)
}

func (h *Handler) inspectImagePromptFilter(c *gin.Context, text string, model string, keyID int64, keyName string, keyMasked string, endpoint string, writeBlock func(*gin.Context), redactPreview bool) bool {
	if h == nil || h.store == nil {
		return false
	}
	cfg := h.store.GetPromptFilterConfig()
	verdict := promptfilter.InspectText(text, cfg)
	if shouldReviewPromptFilterVerdict(verdict, cfg) {
		verdict = reviewPromptFilterVerdict(c.Request.Context(), text, verdict, cfg)
		verdict = promptfilter.ApplyReviewMode(verdict, cfg.Mode)
	}
	if verdict.Action == promptfilter.ActionWarn {
		c.Header("X-Prompt-Filter-Warning", verdict.Reason)
		return false
	}
	if verdict.Action != promptfilter.ActionBlock {
		return false
	}
	textPreview := promptfilter.RedactedPreview(verdict.TextPreview, 500)
	if redactPreview {
		textPreview = "[redacted]"
	}
	h.recordPromptFilterLog(c, &database.PromptFilterLogInput{
		Source:          "local_filter",
		Endpoint:        endpoint,
		Model:           model,
		Action:          verdict.Action,
		Mode:            verdict.Mode,
		Score:           verdict.Score,
		Threshold:       verdict.Threshold,
		MatchedPatterns: promptfilter.MatchesJSON(verdict.Matched),
		TextPreview:     textPreview,
		MatchContext:    promptfilter.RedactedPreview(verdict.MatchContext, promptFilterAuditContextMaxRunes),
		APIKeyID:        keyID,
		APIKeyName:      keyName,
		APIKeyMasked:    keyMasked,
		ClientIP:        c.ClientIP(),
		ReviewModel:     verdict.ReviewModel,
		ReviewFlagged:   verdict.ReviewFlagged,
		ReviewError:     verdict.ReviewError,
	})
	if writeBlock != nil {
		writeBlock(c)
	} else {
		writeError(c, http.StatusBadRequest, "Prompt 被检查规则拦截")
	}
	return true
}

func (h *Handler) recordPromptFilterLog(c *gin.Context, input *database.PromptFilterLogInput) {
	if h == nil || h.db == nil || input == nil {
		return
	}
	priority := database.PromptFilterLogPriorityLow
	if input.Action == promptfilter.ActionWarn || input.Action == promptfilter.ActionBlock {
		priority = database.PromptFilterLogPriorityHigh
	}
	_ = h.db.EnqueuePromptFilterLog(input, priority)
}

func (h *Handler) ListPromptFilterLogs(c *gin.Context) {
	page := positiveQueryInt(c, "page", 1)
	pageSize := positiveQueryInt(c, "page_size", positiveQueryInt(c, "limit", 100))
	apiKeyID := int64(0)
	if raw := strings.TrimSpace(c.Query("api_key_id")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			apiKeyID = parsed
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	logs, total, err := h.db.ListPromptFilterLogsPage(ctx, database.PromptFilterLogQuery{
		Page:                page,
		PageSize:            pageSize,
		Source:              c.Query("source"),
		Action:              c.Query("action"),
		Endpoint:            c.Query("endpoint"),
		Model:               c.Query("model"),
		APIKeyID:            apiKeyID,
		Query:               c.Query("q"),
		ReviewState:         c.Query("reviewed"),
		ReviewResult:        c.Query("review_result"),
		ExcludeIntelligence: true,
	})
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if logs == nil {
		logs = []*database.PromptFilterLog{}
	}
	c.JSON(http.StatusOK, promptFilterLogsResponse{Logs: logs, Total: total, Page: page, PageSize: pageSize})
}

func (h *Handler) ClearPromptFilterLogs(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	var err error
	message := "Prompt 检查日志已清空；风险画像和上游 CY 事件已保留"
	switch strings.ToLower(strings.TrimSpace(c.Query("reviewed"))) {
	case "":
		err = h.db.ClearPromptFilterLogs(ctx)
	case "true", "reviewed":
		err = h.db.ClearPromptFilterLogsByReviewStatus(ctx, true)
		message = "外部模型复核历史已清空；风险画像已保留"
	case "false", "not_reviewed":
		err = h.db.ClearPromptFilterLogsByReviewStatus(ctx, false)
		message = "本地过滤与异步审计日志已清空；风险画像已保留"
	default:
		writeError(c, http.StatusBadRequest, "reviewed 必须为 true 或 false")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}
	writeMessage(c, http.StatusOK, message)
}

func (h *Handler) ClearPromptPolicyIncidents(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.db.ClearPromptPolicyIncidents(ctx); err != nil {
		writeInternalError(c, err)
		return
	}
	writeMessage(c, http.StatusOK, "上游 CY 事件已清空；风险画像已保留")
}

func (h *Handler) ListPromptPolicyIncidents(c *gin.Context) {
	page := positiveQueryInt(c, "page", 1)
	pageSize := positiveQueryInt(c, "page_size", positiveQueryInt(c, "limit", 20))
	apiKeyID := int64(0)
	if raw := strings.TrimSpace(c.Query("api_key_id")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			apiKeyID = parsed
		}
	}
	accountID := int64(0)
	if raw := strings.TrimSpace(c.Query("account_id")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			accountID = parsed
		}
	}
	var localMiss *bool
	if raw := strings.TrimSpace(c.Query("local_miss")); raw != "" {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			localMiss = &parsed
		}
	}
	evaluationState := strings.TrimSpace(c.Query("evaluation_state"))
	if evaluationState == "" {
		evaluationState = strings.TrimSpace(c.Query("status"))
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	incidents, total, err := h.db.ListPromptPolicyIncidentsPage(ctx, database.PromptPolicyIncidentQuery{
		Page: page, PageSize: pageSize, Endpoint: c.Query("endpoint"), Model: c.Query("model"), APIKeyID: apiKeyID, AccountID: accountID,
		EvaluationState: evaluationState, Outcome: c.Query("outcome"), LocalComparison: c.Query("local_comparison"), LocalMiss: localMiss, Query: c.Query("q"),
	})
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if incidents == nil {
		incidents = []*database.PromptPolicyIncident{}
	}
	for _, incident := range incidents {
		h.enrichPromptPolicyIncidentRouting(incident)
	}
	c.JSON(http.StatusOK, promptPolicyIncidentsResponse{Incidents: incidents, Total: total, Page: page, PageSize: pageSize})
}

func (h *Handler) GetPromptPolicyIncident(c *gin.Context) {
	incidentID := strings.TrimSpace(c.Param("incident_id"))
	if incidentID == "" {
		writeError(c, http.StatusBadRequest, "缺少 incident_id")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	incident, err := h.db.GetPromptPolicyIncident(ctx, incidentID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(c, http.StatusNotFound, "CY 事件不存在")
		return
	}
	if err != nil {
		writeInternalError(c, err)
		return
	}
	h.enrichPromptPolicyIncidentRouting(incident)
	matches := json.RawMessage(incident.LocalMatchedPatterns)
	if !json.Valid(matches) {
		matches = json.RawMessage("[]")
	}
	response := promptPolicyIncidentDetailResponse{Incident: incident, Matches: matches}
	if incident.CandidateID > 0 {
		if candidate, candidateErr := h.db.GetPromptRuleCandidate(ctx, incident.CandidateID); candidateErr == nil {
			response.Candidate = candidate
		}
		if incident.CandidateEvidenceID > 0 {
			if item, evidenceErr := h.db.GetPromptRuleCandidateEvidence(ctx, incident.CandidateEvidenceID); evidenceErr == nil && item.CandidateID == incident.CandidateID {
				response.Evidence = item
			}
		}
	}
	c.JSON(http.StatusOK, response)
}

func (h *Handler) enrichPromptPolicyIncidentRouting(incident *database.PromptPolicyIncident) {
	if incident == nil {
		return
	}
	hasEventSnapshot := strings.TrimSpace(incident.AccountName) != "" ||
		strings.TrimSpace(incident.AccountPlatform) != "" ||
		len(incident.AccountGroupIDs) > 0 || len(incident.AccountGroupNames) > 0 ||
		len(incident.APIKeyAllowedGroupIDs) > 0 || len(incident.APIKeyAllowedGroupNames) > 0
	if hasEventSnapshot {
		incident.RoutingSnapshotState = "event_snapshot"
		return
	}
	if h == nil || h.store == nil {
		incident.RoutingSnapshotState = "unavailable"
		return
	}

	inferred := false
	if incident.AccountID > 0 {
		if account := h.store.FindByID(incident.AccountID); account != nil {
			account.Mu().RLock()
			incident.AccountName = strings.TrimSpace(account.Email)
			incident.AccountPlatform = strings.TrimSpace(account.UpstreamType)
			account.Mu().RUnlock()
			if incident.AccountPlatform == "" {
				incident.AccountPlatform = database.UpstreamChannelCodex
			}
			incident.AccountGroupIDs = account.GroupIDSnapshot()
			incident.AccountGroupNames = h.store.ResolveGroupNames(incident.AccountGroupIDs)
			inferred = true
		}
	}
	if incident.APIKeyID > 0 {
		incident.APIKeyAllowedGroupIDs = h.store.GetAPIKeyAllowedGroups(incident.APIKeyID)
		incident.APIKeyAllowedGroupNames = h.store.ResolveGroupNames(incident.APIKeyAllowedGroupIDs)
		if len(incident.APIKeyAllowedGroupIDs) > 0 {
			inferred = true
		}
	}
	if inferred {
		incident.RoutingSnapshotState = "current_inferred"
		return
	}
	incident.RoutingSnapshotState = "unavailable"
}

// MatchPromptFilterLog 按时间/端点/APIKey 找到与某次请求最接近的一条提示词过滤日志，
// 用于「使用统计」里点击 cyber_policy 报错时查看触发的完整请求内容。
// GET /api/prompt-filter/logs/match?at=<RFC3339>&endpoint=&api_key_id=&source=
func (h *Handler) MatchPromptFilterLog(c *gin.Context) {
	atRaw := strings.TrimSpace(c.Query("at"))
	if atRaw == "" {
		writeError(c, http.StatusBadRequest, "缺少 at 参数")
		return
	}
	at, err := time.Parse(time.RFC3339, atRaw)
	if err != nil {
		writeError(c, http.StatusBadRequest, "at 参数格式无效（需 RFC3339）")
		return
	}
	source := strings.TrimSpace(c.Query("source"))
	if source == "" {
		source = "upstream_cyber_policy"
	}
	apiKeyID := int64(0)
	if raw := strings.TrimSpace(c.Query("api_key_id")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			apiKeyID = parsed
		}
	}
	windowSeconds := positiveQueryInt(c, "window", 15)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	log, err := h.db.FindNearestPromptFilterLog(ctx, at, source, strings.TrimSpace(c.Query("endpoint")), apiKeyID, windowSeconds)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"found": log != nil, "log": log, "legacy_inferred": log != nil})
}

func (h *Handler) TestPromptFilter(c *gin.Context) {
	var req promptFilterTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效")
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		writeError(c, http.StatusBadRequest, "text 不能为空")
		return
	}
	if len([]rune(req.Text)) > 20000 {
		writeError(c, http.StatusBadRequest, "text 不能超过 20000 个字符")
		return
	}
	cfg := h.store.GetPromptFilterConfig()
	evaluator := h.imageProxy
	if evaluator == nil {
		evaluator = proxy.NewHandler(nil, nil, nil, nil)
	}
	result := evaluator.EvaluatePromptGuardTextForTest(c, cfg, req.Text, req.Endpoint, req.Model)
	c.JSON(http.StatusOK, promptFilterTestResponse{
		Verdict:  result.Verdict,
		Decision: result.Decision,
		Protocol: result.Protocol,
		Provider: result.Provider,
		Endpoint: result.Endpoint,
		Model:    result.Model,
	})
}

func (h *Handler) TestPromptReviewConnection(c *gin.Context) {
	var req promptReviewTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效")
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		writeError(c, http.StatusBadRequest, "text 不能为空")
		return
	}
	if len([]rune(req.Text)) > 20000 {
		writeError(c, http.StatusBadRequest, "text 不能超过 20000 个字符")
		return
	}
	if h == nil || h.store == nil {
		writeError(c, http.StatusServiceUnavailable, "Prompt 审核配置不可用")
		return
	}
	reviewCfg := h.store.GetPromptFilterConfig().Review
	reviewCfg.Enabled = true
	if key := strings.TrimSpace(req.APIKey); key != "" {
		reviewCfg.APIKey = key
	}
	if baseURL := strings.TrimSpace(req.BaseURL); baseURL != "" {
		reviewCfg.BaseURL = baseURL
	}
	if model := strings.TrimSpace(req.Model); model != "" {
		reviewCfg.Model = model
	}
	if req.TimeoutSeconds > 0 {
		reviewCfg.TimeoutSeconds = req.TimeoutSeconds
	}
	reviewCfg.Adapter = promptfilter.ReviewAdapterConfig{
		RequestMode:          req.RequestMode,
		SystemPrompt:         req.SystemPrompt,
		UserPromptTemplate:   req.UserPromptTemplate,
		PayloadTemplate:      req.PayloadTemplate,
		ConfidenceThreshold:  req.ConfidenceThreshold,
		ModerationThresholds: req.ModerationThresholds,
		MaxConcurrent:        req.MaxConcurrent,
		MaxTextLength:        req.MaxTextLength,
	}
	reviewCfg = promptfilter.NormalizeReviewConfig(reviewCfg)
	if err := promptfilter.ValidateReviewConfig(reviewCfg); err != nil {
		writeError(c, http.StatusBadRequest, "Prompt 审核配置无效: "+err.Error())
		return
	}
	keys := reviewCfg.APIKeyList()
	if req.TestAllKeys && len(keys) > 1 {
		type indexedResult struct {
			index   int
			outcome promptfilter.ReviewOutcome
			latency int64
			err     error
		}
		resultCh := make(chan indexedResult, len(keys))
		started := time.Now()
		limit := reviewCfg.Adapter.MaxConcurrent
		if limit <= 0 || limit > len(keys) {
			limit = len(keys)
		}
		slots := make(chan struct{}, limit)
		for index, key := range keys {
			go func(index int, key string) {
				slots <- struct{}{}
				defer func() { <-slots }()
				keyCfg := reviewCfg
				keyCfg.APIKey = key
				keyStarted := time.Now()
				outcome, testErr := promptfilter.DefaultReviewClient.ReviewTextDetailed(c.Request.Context(), req.Text, keyCfg)
				resultCh <- indexedResult{index: index, outcome: outcome, latency: time.Since(keyStarted).Milliseconds(), err: testErr}
			}(index, key)
		}
		results := make([]promptReviewKeyTestResult, len(keys))
		allOK := true
		var first promptfilter.ReviewOutcome
		for range keys {
			item := <-resultCh
			if item.index == 0 {
				first = item.outcome
			}
			result := promptReviewKeyTestResult{
				KeyIndex: item.index + 1, OK: item.err == nil, Flagged: item.outcome.Flagged,
				Endpoint: item.outcome.Endpoint, Model: item.outcome.Model, Confidence: item.outcome.Confidence,
				Reason: item.outcome.Reason, HighestCategory: item.outcome.HighestCategory,
				DecisionCategory: item.outcome.DecisionCategory, DecisionScore: item.outcome.DecisionScore,
				DecisionThreshold: item.outcome.DecisionThreshold,
				CategoryScores:    item.outcome.CategoryScores, ModerationThresholds: item.outcome.ModerationThresholds,
				LatencyMS: item.latency,
			}
			if item.err != nil {
				allOK = false
				result.Error = item.err.Error()
			}
			results[item.index] = result
		}
		c.JSON(http.StatusOK, promptReviewTestResponse{
			OK: allOK, Endpoint: first.Endpoint, Model: reviewCfg.Model, Flagged: first.Flagged,
			Confidence: first.Confidence, ConfidenceThreshold: reviewCfg.Adapter.ConfidenceThreshold,
			Reason: first.Reason, HighestCategory: first.HighestCategory,
			DecisionCategory: first.DecisionCategory, DecisionScore: first.DecisionScore,
			DecisionThreshold: first.DecisionThreshold,
			CategoryScores:    first.CategoryScores, ModerationThresholds: first.ModerationThresholds,
			LatencyMS: time.Since(started).Milliseconds(), KeyCount: len(keys), Results: results,
		})
		return
	}
	started := time.Now()
	outcome, err := promptfilter.DefaultReviewClient.ReviewTextDetailed(c.Request.Context(), req.Text, reviewCfg)
	if err != nil {
		writeError(c, http.StatusBadGateway, "Prompt 审核连接测试失败: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, promptReviewTestResponse{
		OK:                   true,
		Endpoint:             outcome.Endpoint,
		Model:                outcome.Model,
		Flagged:              outcome.Flagged,
		Confidence:           outcome.Confidence,
		ConfidenceThreshold:  reviewCfg.Adapter.ConfidenceThreshold,
		Reason:               outcome.Reason,
		HighestCategory:      outcome.HighestCategory,
		DecisionCategory:     outcome.DecisionCategory,
		DecisionScore:        outcome.DecisionScore,
		DecisionThreshold:    outcome.DecisionThreshold,
		CategoryScores:       outcome.CategoryScores,
		ModerationThresholds: outcome.ModerationThresholds,
		LatencyMS:            time.Since(started).Milliseconds(),
		KeyCount:             len(keys),
	})
}

func (h *Handler) TestPromptFilterRulePattern(c *gin.Context) {
	var req promptFilterRulePatternTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效")
		return
	}
	trimmedPattern := strings.TrimSpace(req.Pattern)
	if trimmedPattern == "" {
		writeError(c, http.StatusBadRequest, "pattern 不能为空")
		return
	}
	if req.Text == "" {
		writeError(c, http.StatusBadRequest, "text 不能为空")
		return
	}
	if len([]rune(req.Pattern)) > 5000 {
		writeError(c, http.StatusBadRequest, "pattern 不能超过 5000 个字符")
		return
	}
	if len([]rune(req.Text)) > 20000 {
		writeError(c, http.StatusBadRequest, "text 不能超过 20000 个字符")
		return
	}
	re, err := regexp.Compile(req.Pattern)
	if err != nil {
		c.JSON(http.StatusOK, promptFilterRulePatternTestResponse{Matched: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, promptFilterRulePatternTestResponse{Matched: re.MatchString(req.Text)})
}

func (h *Handler) GetPromptFilterRules(c *gin.Context) {
	cfg := h.store.GetPromptFilterConfig()
	disabled := map[string]bool{}
	for _, name := range cfg.DisabledPatterns {
		disabled[strings.ToLower(strings.TrimSpace(name))] = true
	}
	builtin := promptfilter.BuiltinPatternConfigs()
	items := make([]promptFilterRuleItem, 0, len(builtin))
	for _, pattern := range builtin {
		items = append(items, promptFilterRuleItem{
			Name:     pattern.Name,
			Pattern:  pattern.Pattern,
			Weight:   pattern.Weight,
			Category: pattern.Category,
			Strict:   pattern.Strict,
			Enabled:  !disabled[strings.ToLower(strings.TrimSpace(pattern.Name))],
			Builtin:  true,
		})
	}
	c.JSON(http.StatusOK, promptFilterRulesResponse{
		BuiltinPatterns:  items,
		CustomPatterns:   cfg.CustomPatterns,
		DisabledPatterns: cfg.DisabledPatterns,
	})
}

func positiveQueryInt(c *gin.Context, key string, fallback int) int {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func shouldReviewPromptFilterVerdict(verdict promptfilter.Verdict, cfg promptfilter.Config) bool {
	return cfg.Enabled && promptfilter.ShouldReviewVerdict(verdict, cfg.Review)
}

func reviewPromptFilterVerdict(ctx context.Context, text string, verdict promptfilter.Verdict, cfg promptfilter.Config) promptfilter.Verdict {
	if strings.TrimSpace(text) == "" {
		return verdict
	}
	outcome, err := promptfilter.DefaultReviewClient.ReviewTextDetailed(ctx, text, cfg.Review)
	return promptfilter.ApplyReviewOutcome(verdict, outcome, err, cfg.Review)
}
