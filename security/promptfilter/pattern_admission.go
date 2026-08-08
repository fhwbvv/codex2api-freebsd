package promptfilter

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	PatternQuarantineInvalidDefinition = "invalid_definition"
	PatternQuarantineInvalidRegex      = "invalid_regex"
	PatternQuarantineMatchesEmpty      = "matches_empty"
	PatternQuarantineGenericToken      = "generic_token"
	PatternQuarantineGenericStructure  = "generic_structure"
	PatternQuarantineBenignCorpus      = "benign_corpus"
)

const (
	maxCustomPatternNameRunes   = 100
	maxCustomPatternRegexRunes  = 5000
	maxCustomPatternConditions  = 64
	minCustomPatternNarrowRunes = 8
)

// PatternAdmissionError describes why an enabled custom rule is unsafe to
// publish. Code is stable for API/UI consumers; Message is intended for an
// administrator.
type PatternAdmissionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *PatternAdmissionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// PatternQuarantine records a legacy custom rule that was retained for
// inspection but disabled before being published to request handlers.
type PatternQuarantine struct {
	Index   int    `json:"index"`
	Name    string `json:"name"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

var customPatternBenignCorpus = []string{
	"hello, please help me summarize this document",
	"show all available models",
	"select all rows from this table",
	"review this code and run the unit tests",
	"the system is healthy and the user can open the file",
	"please format the data as a markdown table",
	"follow the project instructions and return a string",
	"refactor this struct and stream the response",
	"document browser credential rotation and security controls",
	"C2 is the second frontend component name",
	"Use PowerShell to list files",
	"Use IDA and Ghidra to summarize functions in my own binary",
	"Review a CVE advisory and propose a patch",
	"Document this API JSON payload",
	"请帮我写一封普通邮件",
	"列出所有可用模型",
	"检查这段代码并运行单元测试",
	"用户需要打开文件并查看数据",
	"编写逆向工程课程说明",
	"整理漏洞修复报告并给出补丁建议",
}

var customPatternGenericTokens = []string{
	"a", "i", "x", "1", "_",
	"all", "any", "code", "data", "file", "help", "model", "prompt", "run", "system", "test", "tool", "user",
	"中", "全部", "所有", "代码", "数据", "文件", "帮助", "模型", "提示词", "运行", "系统", "测试", "工具", "用户",
}

type admissionCompiledPattern struct {
	primary *regexp.Regexp
	all     []*regexp.Regexp
	any     []*regexp.Regexp
	exclude []*regexp.Regexp
	minimum int
}

// AuditPatternConfig validates the complete matching semantics of one custom
// rule. A generic primary expression is allowed when mandatory composite
// conditions make the final rule specific enough.
func AuditPatternConfig(pattern PatternConfig) *PatternAdmissionError {
	name := strings.TrimSpace(pattern.Name)
	if name == "" || utf8.RuneCountInString(name) > maxCustomPatternNameRunes {
		return patternAdmissionIssue(PatternQuarantineInvalidDefinition, "规则名称不能为空且不能超过 100 个字符")
	}
	if pattern.Weight < 1 || pattern.Weight > 1000 {
		return patternAdmissionIssue(PatternQuarantineInvalidDefinition, "规则权重必须为 1-1000")
	}
	if pattern.MinMatches < 0 {
		return patternAdmissionIssue(PatternQuarantineInvalidDefinition, "min_matches 不能小于 0")
	}
	if pattern.MinMatches > 0 && len(pattern.AnyPatterns) == 0 {
		return patternAdmissionIssue(PatternQuarantineInvalidDefinition, "配置 min_matches 时必须同时配置 any_patterns")
	}
	if pattern.MinMatches > len(pattern.AnyPatterns) {
		return patternAdmissionIssue(PatternQuarantineInvalidDefinition, "min_matches 不能大于 any_patterns 数量")
	}
	conditionCount := boolInt(strings.TrimSpace(pattern.Pattern) != "") + len(pattern.AllPatterns) + len(pattern.AnyPatterns) + len(pattern.ExcludePatterns)
	if conditionCount > maxCustomPatternConditions {
		return patternAdmissionIssue(PatternQuarantineInvalidDefinition, "单条规则的正则条件不能超过 64 个")
	}
	compiled, issue := compileAdmissionPattern(pattern)
	if issue != nil {
		return issue
	}
	if compiled.matches("") || compiled.matches(" ") {
		return patternAdmissionIssue(PatternQuarantineMatchesEmpty, "规则最终条件可以匹配空文本，可能命中几乎所有请求")
	}
	for _, token := range customPatternGenericTokens {
		if compiled.matches(token) {
			return patternAdmissionIssue(PatternQuarantineGenericToken, fmt.Sprintf("规则最终条件可仅凭通用短词 %q 命中，范围过宽", token))
		}
	}
	for _, sample := range customPatternBenignCorpus {
		if compiled.matches(sample) {
			return patternAdmissionIssue(PatternQuarantineBenignCorpus, "规则最终条件会命中内置良性语料，范围过宽")
		}
	}
	for _, sample := range generatedCustomPatternBenignCorpus() {
		if compiled.matches(sample) {
			return patternAdmissionIssue(PatternQuarantineBenignCorpus, "规则最终条件会命中内置长文本良性语料，范围过宽")
		}
	}
	if !patternRequiresSpecificEvidence(pattern) {
		return patternAdmissionIssue(PatternQuarantineGenericStructure, "规则最终正向条件没有必须出现的具体词组或高约束结构，可能仅凭长度、通配符或宽字符类别命中大量请求")
	}
	return nil
}

// patternRequiresSpecificEvidence verifies that every route capable of
// satisfying the positive rule conditions must include a meaningful literal
// run or enough narrowly constrained rune slots. It rejects length-only,
// wildcard-only and tiny character-class fragments such as `[sS][tT][rR]`,
// while retaining high-constraint token formats such as
// `[q-r][x-y][m-n][0-9]{6}`.
func patternRequiresSpecificEvidence(pattern PatternConfig) bool {
	if expressionRequiresSpecificEvidence(pattern.Pattern) {
		return true
	}
	for _, expression := range pattern.AllPatterns {
		if expressionRequiresSpecificEvidence(expression) {
			return true
		}
	}
	if len(pattern.AnyPatterns) == 0 {
		return false
	}
	minimum := pattern.MinMatches
	if minimum <= 0 {
		minimum = 1
	}
	nonSpecific := 0
	for _, expression := range pattern.AnyPatterns {
		if !expressionRequiresSpecificEvidence(expression) {
			nonSpecific++
		}
	}
	// If enough non-specific alternatives can meet min_matches by themselves,
	// the final rule still has a generic matching route.
	return nonSpecific < minimum
}

func expressionRequiresSpecificEvidence(expression string) bool {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return false
	}
	parsed, err := syntax.Parse(expression, syntax.Perl)
	if err != nil {
		return false
	}
	narrowRunes := syntaxMinimumNarrowRunes(parsed)
	return syntaxRequiresSpecificLiteral(parsed, 4) ||
		narrowRunes >= minCustomPatternNarrowRunes ||
		(syntaxRequiresSpecificLiteral(parsed, 3) && narrowRunes >= 4)
}

func syntaxRequiresSpecificLiteral(expression *syntax.Regexp, minimumRun int) bool {
	if expression == nil {
		return false
	}
	switch expression.Op {
	case syntax.OpLiteral:
		run := 0
		for _, value := range expression.Rune {
			if unicode.IsLetter(value) || unicode.IsDigit(value) {
				run++
				if run >= minimumRun {
					return true
				}
			} else {
				run = 0
			}
		}
		return false
	case syntax.OpCapture:
		return len(expression.Sub) == 1 && syntaxRequiresSpecificLiteral(expression.Sub[0], minimumRun)
	case syntax.OpConcat:
		for _, sub := range expression.Sub {
			if syntaxRequiresSpecificLiteral(sub, minimumRun) {
				return true
			}
		}
		return false
	case syntax.OpAlternate:
		if len(expression.Sub) == 0 {
			return false
		}
		for _, sub := range expression.Sub {
			if !syntaxRequiresSpecificLiteral(sub, minimumRun) {
				return false
			}
		}
		return true
	case syntax.OpPlus:
		return len(expression.Sub) == 1 && syntaxRequiresSpecificLiteral(expression.Sub[0], minimumRun)
	case syntax.OpRepeat:
		return expression.Min > 0 && len(expression.Sub) == 1 && syntaxRequiresSpecificLiteral(expression.Sub[0], minimumRun)
	default:
		// Empty assertions, optional/repeated-zero branches, character classes
		// and wildcard operators do not require a concrete phrase.
		return false
	}
}

func syntaxMinimumNarrowRunes(expression *syntax.Regexp) int {
	if expression == nil {
		return 0
	}
	switch expression.Op {
	case syntax.OpLiteral:
		count := 0
		for _, value := range expression.Rune {
			if unicode.IsLetter(value) || unicode.IsDigit(value) {
				count++
				if count >= minCustomPatternNarrowRunes {
					return minCustomPatternNarrowRunes
				}
			}
		}
		return count
	case syntax.OpCharClass:
		cardinality := int64(0)
		for index := 0; index+1 < len(expression.Rune); index += 2 {
			cardinality += int64(expression.Rune[index+1]) - int64(expression.Rune[index]) + 1
			if cardinality > 16 {
				return 0
			}
		}
		if cardinality > 0 {
			return 1
		}
		return 0
	case syntax.OpCapture:
		if len(expression.Sub) == 1 {
			return syntaxMinimumNarrowRunes(expression.Sub[0])
		}
		return 0
	case syntax.OpConcat:
		minimum := 0
		for _, sub := range expression.Sub {
			minimum += syntaxMinimumNarrowRunes(sub)
			if minimum >= minCustomPatternNarrowRunes {
				return minCustomPatternNarrowRunes
			}
		}
		return minimum
	case syntax.OpAlternate:
		if len(expression.Sub) == 0 {
			return 0
		}
		minimum := minCustomPatternNarrowRunes
		for _, sub := range expression.Sub {
			value := syntaxMinimumNarrowRunes(sub)
			if value < minimum {
				minimum = value
			}
		}
		return minimum
	case syntax.OpPlus:
		if len(expression.Sub) == 1 {
			return syntaxMinimumNarrowRunes(expression.Sub[0])
		}
		return 0
	case syntax.OpRepeat:
		if expression.Min <= 0 || len(expression.Sub) != 1 {
			return 0
		}
		minimum := syntaxMinimumNarrowRunes(expression.Sub[0]) * expression.Min
		if minimum > minCustomPatternNarrowRunes {
			return minCustomPatternNarrowRunes
		}
		return minimum
	default:
		return 0
	}
}

func generatedCustomPatternBenignCorpus() []string {
	return []string{
		strings.Repeat("ordinary safe development request ", 3),
		strings.Repeat("ordinary safe development request ", 16),
		strings.Repeat("ordinary safe development request ", 64),
		strings.Repeat("a", 256),
		strings.Repeat("7", 256),
		strings.Repeat("普通开发请求", 128),
	}
}

// ValidateCustomPatterns is the admission gate for an explicitly submitted
// custom rule set. Every rule needs a stable, case-insensitively unique name.
// Explicitly disabled legacy matching semantics may otherwise be retained;
// enabled rules must pass the complete audit.
func ValidateCustomPatterns(patterns []PatternConfig) error {
	seenNames := make(map[string]int, len(patterns))
	for index, pattern := range patterns {
		name := strings.TrimSpace(pattern.Name)
		if name == "" {
			return fmt.Errorf("custom_patterns[%d]: rule name is required", index)
		}
		nameKey := strings.ToLower(name)
		if previous, exists := seenNames[nameKey]; exists {
			return fmt.Errorf("custom_patterns[%d] %q duplicates custom_patterns[%d]; rule names must be unique", index, name, previous)
		}
		seenNames[nameKey] = index
		if pattern.Enabled != nil && !*pattern.Enabled {
			continue
		}
		if issue := AuditPatternConfig(pattern); issue != nil {
			return fmt.Errorf("custom_patterns[%d] %q: %s (%s)", index, strings.TrimSpace(pattern.Name), issue.Message, issue.Code)
		}
	}
	return nil
}

// SanitizeCustomPatterns quarantines unsafe legacy rules once, when a Store
// publishes a new runtime snapshot. It deliberately does not run from
// NormalizeConfig, which is also used on request hot paths.
func SanitizeCustomPatterns(patterns []PatternConfig) ([]PatternConfig, []PatternQuarantine) {
	if len(patterns) == 0 {
		return nil, nil
	}
	sanitized := make([]PatternConfig, len(patterns))
	quarantined := make([]PatternQuarantine, 0)
	for index, pattern := range patterns {
		if pattern.Enabled != nil && !*pattern.Enabled {
			sanitized[index] = pattern
			continue
		}
		if issue := AuditPatternConfig(pattern); issue != nil {
			disabled := false
			pattern.Enabled = &disabled
			quarantined = append(quarantined, PatternQuarantine{
				Index:   index,
				Name:    pattern.Name,
				Code:    issue.Code,
				Message: issue.Message,
			})
		}
		sanitized[index] = pattern
	}
	return sanitized, quarantined
}

func compileAdmissionPattern(pattern PatternConfig) (admissionCompiledPattern, *PatternAdmissionError) {
	var compiled admissionCompiledPattern
	positiveCount := 0
	compileOne := func(field string, expression string) (*regexp.Regexp, *PatternAdmissionError) {
		expression = strings.TrimSpace(expression)
		if expression == "" {
			return nil, patternAdmissionIssue(PatternQuarantineInvalidDefinition, field+" 中不能包含空正则")
		}
		if utf8.RuneCountInString(expression) > maxCustomPatternRegexRunes {
			return nil, patternAdmissionIssue(PatternQuarantineInvalidDefinition, field+" 中的正则不能超过 5000 个字符")
		}
		re, err := regexp.Compile(expression)
		if err != nil {
			return nil, patternAdmissionIssue(PatternQuarantineInvalidRegex, fmt.Sprintf("%s 正则无效: %v", field, err))
		}
		return re, nil
	}
	if strings.TrimSpace(pattern.Pattern) != "" {
		re, issue := compileOne("pattern", pattern.Pattern)
		if issue != nil {
			return compiled, issue
		}
		compiled.primary = re
		positiveCount++
	}
	compileList := func(field string, expressions []string) ([]*regexp.Regexp, *PatternAdmissionError) {
		out := make([]*regexp.Regexp, 0, len(expressions))
		for _, expression := range expressions {
			re, issue := compileOne(field, expression)
			if issue != nil {
				return nil, issue
			}
			out = append(out, re)
		}
		return out, nil
	}
	var issue *PatternAdmissionError
	if compiled.all, issue = compileList("all_patterns", pattern.AllPatterns); issue != nil {
		return compiled, issue
	}
	positiveCount += len(compiled.all)
	if compiled.any, issue = compileList("any_patterns", pattern.AnyPatterns); issue != nil {
		return compiled, issue
	}
	positiveCount += len(compiled.any)
	if compiled.exclude, issue = compileList("exclude_patterns", pattern.ExcludePatterns); issue != nil {
		return compiled, issue
	}
	if positiveCount == 0 {
		return compiled, patternAdmissionIssue(PatternQuarantineInvalidDefinition, "规则至少需要一个 pattern、all_patterns 或 any_patterns 条件")
	}
	compiled.minimum = pattern.MinMatches
	if compiled.minimum <= 0 && len(compiled.any) > 0 {
		compiled.minimum = 1
	}
	return compiled, nil
}

func (pattern admissionCompiledPattern) matches(text string) bool {
	return compositePatternMatchIndex(text, pattern.primary, pattern.all, pattern.any, pattern.exclude, pattern.minimum) != nil
}

func patternAdmissionIssue(code string, message string) *PatternAdmissionError {
	return &PatternAdmissionError{Code: code, Message: message}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
