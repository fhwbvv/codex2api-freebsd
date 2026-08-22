package auth

import (
	"encoding/json"
	"strings"
)

// GrokFollowUpEffortConfig 是 Claude Code → Grok 续轮降思考的系统设置。
// 默认关闭。开启后首轮提问仍用客户端/默认 effort，工具回传和小请求可降到 low/medium/high。
type GrokFollowUpEffortConfig struct {
	Enabled     bool   `json:"follow_up_effort_enabled"`
	ToolEffort  string `json:"follow_up_tool_effort"`
	SmallEffort string `json:"follow_up_small_effort"`
}

func DefaultGrokFollowUpEffortConfig() GrokFollowUpEffortConfig {
	return GrokFollowUpEffortConfig{
		Enabled:     false,
		ToolEffort:  "medium",
		SmallEffort: "low",
	}
}

func NormalizeGrokFollowUpEffort(effort, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(effort))
	default:
		return fallback
	}
}

func NormalizeGrokFollowUpEffortConfig(cfg GrokFollowUpEffortConfig) GrokFollowUpEffortConfig {
	defaults := DefaultGrokFollowUpEffortConfig()
	cfg.ToolEffort = NormalizeGrokFollowUpEffort(cfg.ToolEffort, defaults.ToolEffort)
	cfg.SmallEffort = NormalizeGrokFollowUpEffort(cfg.SmallEffort, defaults.SmallEffort)
	return cfg
}

func GrokFollowUpEffortConfigFromJSON(raw string) GrokFollowUpEffortConfig {
	cfg := DefaultGrokFollowUpEffortConfig()
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return cfg
	}
	var parsed struct {
		Enabled     *bool  `json:"follow_up_effort_enabled"`
		ToolEffort  string `json:"follow_up_tool_effort"`
		SmallEffort string `json:"follow_up_small_effort"`
	}
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return cfg
	}
	if parsed.Enabled != nil {
		cfg.Enabled = *parsed.Enabled
	}
	if strings.TrimSpace(parsed.ToolEffort) != "" {
		cfg.ToolEffort = parsed.ToolEffort
	}
	if strings.TrimSpace(parsed.SmallEffort) != "" {
		cfg.SmallEffort = parsed.SmallEffort
	}
	return NormalizeGrokFollowUpEffortConfig(cfg)
}
