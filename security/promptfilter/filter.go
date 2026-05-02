package promptfilter

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

const (
	ActionAllow = "allow"
	ActionWarn  = "warn"
	ActionBlock = "block"

	ModeMonitor = "monitor"
	ModeWarn    = "warn"
	ModeBlock   = "block"

	DefaultThreshold       = 50
	DefaultStrictThreshold = 90
	DefaultMaxTextLength   = 80 * 1024
	defaultHeadScanLength  = 64 * 1024
	defaultTailScanLength  = 16 * 1024
)

type Config struct {
	Enabled          bool            `json:"enabled"`
	Mode             string          `json:"mode"`
	Threshold        int             `json:"threshold"`
	StrictThreshold  int             `json:"strict_threshold"`
	LogMatches       bool            `json:"log_matches"`
	MaxTextLength    int             `json:"max_text_length"`
	SensitiveWords   string          `json:"sensitive_words"`
	CustomPatterns   []PatternConfig `json:"custom_patterns"`
	DisabledPatterns []string        `json:"disabled_patterns"`
}

type PatternConfig struct {
	Name     string `json:"name"`
	Pattern  string `json:"pattern"`
	Weight   int    `json:"weight"`
	Category string `json:"category,omitempty"`
	Strict   bool   `json:"strict,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

type Match struct {
	Name     string `json:"name"`
	Weight   int    `json:"weight"`
	Category string `json:"category,omitempty"`
	Strict   bool   `json:"strict,omitempty"`
}

type Verdict struct {
	Enabled        bool    `json:"enabled"`
	Mode           string  `json:"mode"`
	Action         string  `json:"action"`
	Score          int     `json:"score"`
	RawScore       int     `json:"raw_score"`
	Threshold      int     `json:"threshold"`
	StrictHit      bool    `json:"strict_hit"`
	Matched        []Match `json:"matched"`
	Reason         string  `json:"reason,omitempty"`
	TextPreview    string  `json:"text_preview,omitempty"`
	ExtractedChars int     `json:"extracted_chars"`
}

type Engine struct {
	cfg            Config
	patterns       []compiledPattern
	sensitiveWords []string
}

type compiledPattern struct {
	cfg PatternConfig
	re  *regexp.Regexp
}

func DefaultConfig() Config {
	return Config{
		Enabled:         false,
		Mode:            ModeMonitor,
		Threshold:       DefaultThreshold,
		StrictThreshold: DefaultStrictThreshold,
		LogMatches:      true,
		MaxTextLength:   DefaultMaxTextLength,
	}
}

func ParseCustomPatterns(raw string) ([]PatternConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []PatternConfig
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("invalid custom_patterns JSON: %w", err)
	}
	return out, nil
}

func MarshalCustomPatterns(patterns []PatternConfig) string {
	if len(patterns) == 0 {
		return "[]"
	}
	data, err := json.Marshal(patterns)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func ParseDisabledPatterns(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil, fmt.Errorf("invalid disabled_patterns JSON: %w", err)
	}
	return normalizePatternNames(names), nil
}

func MarshalDisabledPatterns(names []string) string {
	names = normalizePatternNames(names)
	if len(names) == 0 {
		return "[]"
	}
	data, err := json.Marshal(names)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func NormalizeConfig(cfg Config) Config {
	defaults := DefaultConfig()
	if strings.TrimSpace(cfg.Mode) == "" {
		cfg.Mode = defaults.Mode
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Mode)) {
	case ModeBlock:
		cfg.Mode = ModeBlock
	case ModeWarn:
		cfg.Mode = ModeWarn
	default:
		cfg.Mode = ModeMonitor
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = defaults.Threshold
	}
	if cfg.Threshold > 500 {
		cfg.Threshold = 500
	}
	if cfg.StrictThreshold <= 0 {
		cfg.StrictThreshold = defaults.StrictThreshold
	}
	if cfg.StrictThreshold < cfg.Threshold {
		cfg.StrictThreshold = cfg.Threshold
	}
	if cfg.StrictThreshold > 1000 {
		cfg.StrictThreshold = 1000
	}
	if cfg.MaxTextLength <= 0 {
		cfg.MaxTextLength = defaults.MaxTextLength
	}
	if cfg.MaxTextLength > 1024*1024 {
		cfg.MaxTextLength = 1024 * 1024
	}
	cfg.DisabledPatterns = normalizePatternNames(cfg.DisabledPatterns)
	return cfg
}

func NewEngine(cfg Config) (*Engine, error) {
	cfg = NormalizeConfig(cfg)
	disabled := disabledPatternSet(cfg.DisabledPatterns)
	merged := append([]PatternConfig{}, defaultPatternConfigs...)
	merged = append(merged, cfg.CustomPatterns...)

	patterns := make([]compiledPattern, 0, len(merged))
	for _, pattern := range merged {
		pattern.Name = strings.TrimSpace(pattern.Name)
		pattern.Pattern = strings.TrimSpace(pattern.Pattern)
		pattern.Category = strings.TrimSpace(pattern.Category)
		if pattern.Name == "" || pattern.Pattern == "" || pattern.Weight <= 0 {
			continue
		}
		if disabled[strings.ToLower(pattern.Name)] {
			continue
		}
		if pattern.Enabled != nil && !*pattern.Enabled {
			continue
		}
		re, err := regexp.Compile(pattern.Pattern)
		if err != nil {
			return nil, fmt.Errorf("compile pattern %q: %w", pattern.Name, err)
		}
		patterns = append(patterns, compiledPattern{cfg: pattern, re: re})
	}

	return &Engine{
		cfg:            cfg,
		patterns:       patterns,
		sensitiveWords: parseSensitiveWords(cfg.SensitiveWords),
	}, nil
}

func BuiltinPatternConfigs() []PatternConfig {
	out := make([]PatternConfig, len(defaultPatternConfigs))
	copy(out, defaultPatternConfigs)
	return out
}

func Inspect(body []byte, endpoint string, cfg Config) Verdict {
	text := ExtractText(body, endpoint, NormalizeConfig(cfg).MaxTextLength)
	return InspectText(text, cfg)
}

func InspectText(text string, cfg Config) Verdict {
	cfg = NormalizeConfig(cfg)
	preview := Preview(text, 500)
	verdict := Verdict{
		Enabled:        cfg.Enabled,
		Mode:           cfg.Mode,
		Action:         ActionAllow,
		Threshold:      cfg.Threshold,
		TextPreview:    preview,
		ExtractedChars: utf8.RuneCountInString(text),
	}
	if !cfg.Enabled || strings.TrimSpace(text) == "" {
		return verdict
	}

	engine, err := NewEngine(cfg)
	if err != nil {
		verdict.Reason = err.Error()
		return verdict
	}
	return engine.InspectText(text)
}

func (e *Engine) InspectText(text string) Verdict {
	cfg := e.cfg
	preview := Preview(text, 500)
	verdict := Verdict{
		Enabled:        cfg.Enabled,
		Mode:           cfg.Mode,
		Action:         ActionAllow,
		Threshold:      cfg.Threshold,
		TextPreview:    preview,
		ExtractedChars: utf8.RuneCountInString(text),
	}
	if !cfg.Enabled || strings.TrimSpace(text) == "" {
		return verdict
	}

	scanText := normalizeForScan(limitScanText(text, cfg.MaxTextLength))
	if utf8.RuneCountInString(scanText) < 3 {
		return verdict
	}

	matchesByName := map[string]Match{}
	rawScore := 0
	strictScore := 0
	for _, word := range e.sensitiveWords {
		if word == "" {
			continue
		}
		if strings.Contains(scanText, word) {
			match := Match{Name: "sensitive_word:" + word, Weight: 100, Category: "sensitive_word", Strict: true}
			matchesByName[match.Name] = match
		}
	}
	for _, pattern := range e.patterns {
		if pattern.re.MatchString(scanText) {
			match := Match{
				Name:     pattern.cfg.Name,
				Weight:   pattern.cfg.Weight,
				Category: pattern.cfg.Category,
				Strict:   pattern.cfg.Strict,
			}
			matchesByName[match.Name] = match
		}
	}

	matches := make([]Match, 0, len(matchesByName))
	for _, match := range matchesByName {
		matches = append(matches, match)
		rawScore += match.Weight
		if match.Strict {
			strictScore += match.Weight
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Weight == matches[j].Weight {
			return matches[i].Name < matches[j].Name
		}
		return matches[i].Weight > matches[j].Weight
	})

	score := rawScore
	if rawScore > 0 {
		score -= defensiveContextDiscount(scanText)
		if score < 0 {
			score = 0
		}
	}
	strictHit := strictScore >= cfg.StrictThreshold
	action := ActionAllow
	if score >= cfg.Threshold || strictHit {
		switch cfg.Mode {
		case ModeBlock:
			action = ActionBlock
		case ModeWarn:
			action = ActionWarn
		default:
			action = ActionAllow
		}
	}

	verdict.Action = action
	verdict.Score = score
	verdict.RawScore = rawScore
	verdict.StrictHit = strictHit
	verdict.Matched = matches
	if len(matches) > 0 {
		verdict.Reason = reasonForVerdict(action, score, cfg.Threshold, matches)
	}
	return verdict
}

func ExtractText(body []byte, endpoint string, maxLen int) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	var parts []string
	endpoint = strings.ToLower(strings.TrimSpace(endpoint))

	addResultText := func(result gjson.Result) {
		if result.Exists() {
			collectGJSONText(result, &parts)
		}
	}

	switch endpoint {
	case "chat", "chat_completions", "/v1/chat/completions":
		addResultText(gjson.GetBytes(body, "messages"))
	case "messages", "anthropic", "/v1/messages":
		addResultText(gjson.GetBytes(body, "system"))
		addResultText(gjson.GetBytes(body, "messages"))
	case "image", "images", "images_generations", "images_edits", "/v1/images/generations", "/v1/images/edits":
		addResultText(gjson.GetBytes(body, "prompt"))
		addResultText(gjson.GetBytes(body, "style"))
	default:
		addResultText(gjson.GetBytes(body, "instructions"))
		addResultText(gjson.GetBytes(body, "input"))
		addResultText(gjson.GetBytes(body, "prompt"))
		addResultText(gjson.GetBytes(body, "messages"))
	}
	return limitScanText(strings.Join(parts, "\n"), maxLen)
}

func Preview(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "..."
}

func MatchesJSON(matches []Match) string {
	if len(matches) == 0 {
		return "[]"
	}
	data, err := json.Marshal(matches)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func collectGJSONText(result gjson.Result, parts *[]string) {
	if !result.Exists() || result.Type == gjson.Null {
		return
	}
	switch {
	case result.IsArray():
		for _, item := range result.Array() {
			collectGJSONText(item, parts)
		}
	case result.IsObject():
		if t := strings.TrimSpace(result.Get("text").String()); t != "" {
			*parts = append(*parts, t)
		}
		if t := strings.TrimSpace(result.Get("content").String()); t != "" {
			*parts = append(*parts, t)
		}
		result.ForEach(func(key, value gjson.Result) bool {
			switch key.String() {
			case "image_url", "file_id", "result":
				return true
			}
			collectGJSONText(value, parts)
			return true
		})
	case result.Type == gjson.String:
		if t := strings.TrimSpace(result.String()); t != "" {
			*parts = append(*parts, t)
		}
	}
}

func parseSensitiveWords(raw string) []string {
	lines := strings.Split(raw, "\n")
	seen := map[string]struct{}{}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = normalizeForScan(strings.TrimSpace(line))
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
	}
	return out
}

func normalizePatternNames(names []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

func disabledPatternSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			out[strings.ToLower(name)] = true
		}
	}
	return out
}

func normalizeForScan(text string) string {
	text = strings.ReplaceAll(text, "```", " ")
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return ' '
		}
		return unicode.ToLower(r)
	}, text)
	return strings.Join(strings.Fields(text), " ")
}

func limitScanText(text string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = DefaultMaxTextLength
	}
	if len(text) <= maxLen {
		return text
	}
	head := defaultHeadScanLength
	tail := defaultTailScanLength
	if maxLen < head+tail {
		head = maxLen * 4 / 5
		tail = maxLen - head
	}
	if head > len(text) {
		head = len(text)
	}
	if tail > len(text)-head {
		tail = len(text) - head
	}
	return text[:head] + "\n" + text[len(text)-tail:]
}

func defensiveContextDiscount(text string) int {
	discount := 0
	for _, pattern := range defensiveContextPatterns {
		if pattern.MatchString(text) {
			discount += 15
		}
	}
	if discount > 45 {
		return 45
	}
	return discount
}

func reasonForVerdict(action string, score int, threshold int, matches []Match) string {
	if len(matches) == 0 {
		return ""
	}
	names := make([]string, 0, len(matches))
	for i, match := range matches {
		if i >= 3 {
			break
		}
		names = append(names, match.Name)
	}
	if action == ActionBlock {
		return fmt.Sprintf("prompt blocked: score %d >= %d (%s)", score, threshold, strings.Join(names, ", "))
	}
	if action == ActionWarn {
		return fmt.Sprintf("prompt warning: score %d >= %d (%s)", score, threshold, strings.Join(names, ", "))
	}
	return fmt.Sprintf("prompt matched: score %d (%s)", score, strings.Join(names, ", "))
}
