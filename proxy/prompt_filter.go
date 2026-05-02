package proxy

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/codex2api/api"
	"github.com/codex2api/database"
	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func (h *Handler) inspectPromptFilterOpenAI(c *gin.Context, rawBody []byte, endpoint string, model string) bool {
	if h == nil || h.store == nil {
		return false
	}
	cfg := h.store.GetPromptFilterConfig()
	verdict := promptfilter.Inspect(rawBody, endpoint, cfg)
	h.logPromptFilterVerdict(c, endpoint, model, "local_filter", "", verdict)
	if verdict.Action == promptfilter.ActionWarn {
		c.Header("X-Prompt-Filter-Warning", verdict.Reason)
	}
	if verdict.Action != promptfilter.ActionBlock {
		return false
	}
	api.SendErrorWithStatus(c, api.NewAPIError(
		api.ErrorCode("prompt_blocked"),
		"Request contains content blocked by prompt filter",
		api.ErrorTypeInvalidRequest,
	), http.StatusBadRequest)
	return true
}

func (h *Handler) inspectPromptFilterTextOpenAI(c *gin.Context, text string, endpoint string, model string) bool {
	if h == nil || h.store == nil {
		return false
	}
	cfg := h.store.GetPromptFilterConfig()
	verdict := promptfilter.InspectText(text, cfg)
	h.logPromptFilterVerdict(c, endpoint, model, "local_filter", "", verdict)
	if verdict.Action == promptfilter.ActionWarn {
		c.Header("X-Prompt-Filter-Warning", verdict.Reason)
	}
	if verdict.Action != promptfilter.ActionBlock {
		return false
	}
	api.SendErrorWithStatus(c, api.NewAPIError(
		api.ErrorCode("prompt_blocked"),
		"Request contains content blocked by prompt filter",
		api.ErrorTypeInvalidRequest,
	), http.StatusBadRequest)
	return true
}

func (h *Handler) inspectPromptFilterAnthropic(c *gin.Context, rawBody []byte, endpoint string, model string) bool {
	if h == nil || h.store == nil {
		return false
	}
	cfg := h.store.GetPromptFilterConfig()
	verdict := promptfilter.Inspect(rawBody, endpoint, cfg)
	h.logPromptFilterVerdict(c, endpoint, model, "local_filter", "", verdict)
	if verdict.Action == promptfilter.ActionWarn {
		c.Header("X-Prompt-Filter-Warning", verdict.Reason)
	}
	if verdict.Action == promptfilter.ActionBlock {
		sendAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "Request contains content blocked by prompt filter")
		return true
	}
	return false
}

func (h *Handler) logPromptFilterVerdict(c *gin.Context, endpoint string, model string, source string, errorCode string, verdict promptfilter.Verdict) {
	if h == nil || h.db == nil || !verdict.Enabled {
		return
	}
	if source == "local_filter" && len(verdict.Matched) == 0 {
		return
	}
	if h.store != nil {
		cfg := h.store.GetPromptFilterConfig()
		if source == "local_filter" && !cfg.LogMatches {
			return
		}
	}
	input := &database.PromptFilterLogInput{
		Source:          source,
		Endpoint:        endpoint,
		Model:           model,
		Action:          verdict.Action,
		Mode:            verdict.Mode,
		Score:           verdict.Score,
		Threshold:       verdict.Threshold,
		MatchedPatterns: promptfilter.MatchesJSON(verdict.Matched),
		TextPreview:     verdict.TextPreview,
		ClientIP:        c.ClientIP(),
		ErrorCode:       errorCode,
	}
	populatePromptFilterAPIKeyMeta(c, input)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = h.db.InsertPromptFilterLog(ctx, input)
}

func (h *Handler) logUpstreamCyberPolicy(c *gin.Context, endpoint string, model string, body []byte) {
	if h == nil || h.store == nil {
		return
	}
	errorCode := upstreamCyberPolicyCode(body)
	if errorCode == "" {
		return
	}
	cfg := h.store.GetPromptFilterConfig()
	verdict := promptfilter.Verdict{
		Enabled:   true,
		Mode:      cfg.Mode,
		Action:    promptfilter.ActionBlock,
		Score:     0,
		Threshold: cfg.Threshold,
		Reason:    "upstream returned cyber policy",
	}
	h.logPromptFilterVerdict(c, endpoint, model, "upstream_cyber_policy", errorCode, verdict)
}

func upstreamCyberPolicyCode(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	raw := string(body)
	for _, path := range []string{"codex_error_info", "error.codex_error_info", "error.code", "code"} {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); strings.EqualFold(value, "cyber_policy") {
			return "cyber_policy"
		}
	}
	if strings.Contains(strings.ToLower(raw), "cyber_policy") || strings.Contains(strings.ToLower(raw), "cyber security risk") {
		return "cyber_policy"
	}
	return ""
}

func populatePromptFilterAPIKeyMeta(c *gin.Context, input *database.PromptFilterLogInput) {
	if c == nil || input == nil {
		return
	}
	if v, exists := c.Get(contextAPIKeyID); exists && v != nil {
		switch typed := v.(type) {
		case int64:
			input.APIKeyID = typed
		case int:
			input.APIKeyID = int64(typed)
		}
	}
	if v, exists := c.Get(contextAPIKeyName); exists && v != nil {
		if name, ok := v.(string); ok {
			input.APIKeyName = name
		}
	}
	if v, exists := c.Get(contextAPIKeyMasked); exists && v != nil {
		if masked, ok := v.(string); ok {
			input.APIKeyMasked = masked
		}
	}
}
