package proxy

import (
	"encoding/json"
	"strings"
	"sync"
)

// ToolInputCorrectionStats records automatic tool input corrections.
type ToolInputCorrectionStats struct {
	TotalCorrected      int            `json:"total_corrected"`
	CorrectionsByTool   map[string]int `json:"corrections_by_tool"`
	CorrectionsByReason map[string]int `json:"corrections_by_reason"`
}

// ToolInputCorrector applies narrow, tool-specific compatibility fixes to
// upstream function_call arguments before Claude Code validates tool inputs.
type ToolInputCorrector struct {
	mu    sync.RWMutex
	stats ToolInputCorrectionStats
}

// ClaudeToolCorrector is a Claude Code-oriented alias for ToolInputCorrector.
// It mirrors sub2api's centralized corrector pattern while keeping the more
// explicit tool-input name available to existing call sites and tests.
type ClaudeToolCorrector = ToolInputCorrector

// NewToolInputCorrector creates a tool input corrector with empty stats.
func NewToolInputCorrector() *ToolInputCorrector {
	return &ToolInputCorrector{
		stats: ToolInputCorrectionStats{
			CorrectionsByTool:   make(map[string]int),
			CorrectionsByReason: make(map[string]int),
		},
	}
}

// NewClaudeToolCorrector creates a Claude tool corrector with empty stats.
func NewClaudeToolCorrector() *ClaudeToolCorrector {
	return NewToolInputCorrector()
}

var defaultClaudeToolCorrector = NewClaudeToolCorrector()

// CorrectToolInputJSON cleans a tool arguments JSON object. It deliberately
// handles only known Claude Code compatibility issues; empty strings and nulls
// can be meaningful for many tools and must not be removed generically.
func (c *ToolInputCorrector) CorrectToolInputJSON(toolName string, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw, false
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return raw, false
	}

	var correctionReasons []string
	switch toolName {
	case "Read":
		if isJSONStringLiteral(obj["pages"], "") {
			delete(obj, "pages")
			correctionReasons = append(correctionReasons, "Read.pages:empty")
		}
	case "EnterWorktree":
		if isJSONStringLiteral(obj["name"], "") && hasNonEmptyJSONString(obj["path"]) {
			delete(obj, "name")
			correctionReasons = append(correctionReasons, "EnterWorktree.name:empty-with-path")
		}
		if isJSONStringLiteral(obj["path"], "") && hasNonEmptyJSONString(obj["name"]) {
			delete(obj, "path")
			correctionReasons = append(correctionReasons, "EnterWorktree.path:empty-with-name")
		}
	}

	if len(correctionReasons) == 0 {
		return raw, false
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw, false
	}
	for _, reason := range correctionReasons {
		c.recordCorrection(toolName, reason)
	}
	return string(out), true
}

// GetStats returns a copy of correction statistics.
func (c *ToolInputCorrector) GetStats() ToolInputCorrectionStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := ToolInputCorrectionStats{
		TotalCorrected:      c.stats.TotalCorrected,
		CorrectionsByTool:   make(map[string]int, len(c.stats.CorrectionsByTool)),
		CorrectionsByReason: make(map[string]int, len(c.stats.CorrectionsByReason)),
	}
	for k, v := range c.stats.CorrectionsByTool {
		stats.CorrectionsByTool[k] = v
	}
	for k, v := range c.stats.CorrectionsByReason {
		stats.CorrectionsByReason[k] = v
	}
	return stats
}

// ResetStats resets correction statistics.
func (c *ToolInputCorrector) ResetStats() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.TotalCorrected = 0
	c.stats.CorrectionsByTool = make(map[string]int)
	c.stats.CorrectionsByReason = make(map[string]int)
}

func (c *ToolInputCorrector) recordCorrection(toolName string, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stats.CorrectionsByTool == nil {
		c.stats.CorrectionsByTool = make(map[string]int)
	}
	if c.stats.CorrectionsByReason == nil {
		c.stats.CorrectionsByReason = make(map[string]int)
	}
	c.stats.TotalCorrected++
	c.stats.CorrectionsByTool[toolName]++
	c.stats.CorrectionsByReason[reason]++
}

func isJSONStringLiteral(raw json.RawMessage, want string) bool {
	if len(raw) == 0 {
		return false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}
	return s == want
}

func hasNonEmptyJSONString(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}
	return s != ""
}

// sanitizeToolInputJSON preserves the existing call-site contract while routing
// corrections through the centralized ToolInputCorrector.
func sanitizeToolInputJSON(toolName string, raw string) string {
	if cleaned, corrected := defaultClaudeToolCorrector.CorrectToolInputJSON(toolName, raw); corrected {
		return cleaned
	}
	return strings.TrimSpace(raw)
}
