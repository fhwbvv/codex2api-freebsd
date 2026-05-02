package database

import "strings"

// ModelPricing 模型价格配置（每百万 token 的价格，单位：美元）
type ModelPricing struct {
	InputPricePerMToken             float64 // 输入价格（美元/百万token）
	InputPricePerMTokenPriority     float64 // priority service tier 输入价格
	OutputPricePerMToken            float64 // 输出价格（美元/百万token）
	OutputPricePerMTokenPriority    float64 // priority service tier 输出价格
	CacheReadPricePerMToken         float64 // 缓存命中输入价格
	CacheReadPricePerMTokenPriority float64 // priority service tier 缓存命中输入价格
}

type modelPricingRule struct {
	model   string
	pricing ModelPricing
}

type costBreakdown struct {
	InputCost                 float64
	OutputCost                float64
	CacheReadCost             float64
	TotalCost                 float64
	InputPricePerMToken       float64
	OutputPricePerMToken      float64
	CacheReadPricePerMToken   float64
	ServiceTierCostMultiplier float64
}

var (
	defaultModelPricing = &ModelPricing{InputPricePerMToken: 1.0, OutputPricePerMToken: 2.0}

	modelPricingRules = []modelPricingRule{
		// Codex/GPT-5 系列，参考 sub2api 的本地 fallback 定价。
		{model: "gpt-5.5", pricing: ModelPricing{
			InputPricePerMToken:             5.0,
			InputPricePerMTokenPriority:     10.0,
			OutputPricePerMToken:            30.0,
			OutputPricePerMTokenPriority:    60.0,
			CacheReadPricePerMToken:         0.5,
			CacheReadPricePerMTokenPriority: 1.0,
		}},
		{model: "gpt-5.4-mini", pricing: ModelPricing{InputPricePerMToken: 0.75, OutputPricePerMToken: 4.5, CacheReadPricePerMToken: 0.075}},
		{model: "gpt-5.4-nano", pricing: ModelPricing{InputPricePerMToken: 0.2, OutputPricePerMToken: 1.25, CacheReadPricePerMToken: 0.02}},
		{model: "gpt-5.4", pricing: ModelPricing{
			InputPricePerMToken:             2.5,
			InputPricePerMTokenPriority:     5.0,
			OutputPricePerMToken:            15.0,
			OutputPricePerMTokenPriority:    30.0,
			CacheReadPricePerMToken:         0.25,
			CacheReadPricePerMTokenPriority: 0.5,
		}},
		{model: "gpt-5.3-codex-spark", pricing: ModelPricing{
			InputPricePerMToken:             1.5,
			InputPricePerMTokenPriority:     3.0,
			OutputPricePerMToken:            12.0,
			OutputPricePerMTokenPriority:    24.0,
			CacheReadPricePerMToken:         0.15,
			CacheReadPricePerMTokenPriority: 0.3,
		}},
		{model: "gpt-5.3-codex", pricing: ModelPricing{
			InputPricePerMToken:             1.5,
			InputPricePerMTokenPriority:     3.0,
			OutputPricePerMToken:            12.0,
			OutputPricePerMTokenPriority:    24.0,
			CacheReadPricePerMToken:         0.15,
			CacheReadPricePerMTokenPriority: 0.3,
		}},
		{model: "gpt-5.2", pricing: ModelPricing{
			InputPricePerMToken:             1.75,
			InputPricePerMTokenPriority:     3.5,
			OutputPricePerMToken:            14.0,
			OutputPricePerMTokenPriority:    28.0,
			CacheReadPricePerMToken:         0.175,
			CacheReadPricePerMTokenPriority: 0.35,
		}},

		// GPT-4 系列。保持最具体模型优先，避免 gpt-4o-mini 被 gpt-4o/gpt-4 抢先匹配。
		{model: "gpt-4o-mini", pricing: ModelPricing{InputPricePerMToken: 0.15, OutputPricePerMToken: 0.6}},
		{model: "gpt-4o", pricing: ModelPricing{InputPricePerMToken: 2.5, OutputPricePerMToken: 10.0}},
		{model: "gpt-4-turbo", pricing: ModelPricing{InputPricePerMToken: 10.0, OutputPricePerMToken: 30.0}},
		{model: "gpt-4", pricing: ModelPricing{InputPricePerMToken: 30.0, OutputPricePerMToken: 60.0}},
		{model: "gpt-3.5-turbo", pricing: ModelPricing{InputPricePerMToken: 0.5, OutputPricePerMToken: 1.5}},
	}
)

// getModelPricing 获取模型价格配置
// 优先使用确定性的模型族匹配，避免 Go map 迭代顺序导致重叠前缀随机命中。
func getModelPricing(model string) *ModelPricing {
	normalized := normalizeBillingModelName(model)
	if pricing := claudeFamilyPricing(normalized); pricing != nil {
		return pricing
	}
	if pricing := geminiFamilyPricing(normalized); pricing != nil {
		return pricing
	}
	if codexModel, ok := normalizeCodexBillingModel(normalized); ok {
		normalized = codexModel
	}
	if pricing := modelRulePricing(normalized); pricing != nil {
		return pricing
	}
	return defaultModelPricing
}

// calculateCost 计算使用费用
// inputTokens: 输入 token 数量
// outputTokens: 输出 token 数量
// model: 模型名称
// 返回：账号计费金额（美元）
func calculateCost(inputTokens, outputTokens, cachedTokens int, model string, serviceTier string) float64 {
	return calculateCostBreakdown(inputTokens, outputTokens, cachedTokens, model, serviceTier).TotalCost
}

func calculateCostBreakdown(inputTokens, outputTokens, cachedTokens int, model string, serviceTier string) costBreakdown {
	pricing := getModelPricing(model)
	inputPrice := pricing.InputPricePerMToken
	outputPrice := pricing.OutputPricePerMToken
	cacheReadPrice := pricing.CacheReadPricePerMToken

	tierMultiplier := serviceTierCostMultiplier(serviceTier)
	if usePriorityPricing(serviceTier, pricing) {
		tierMultiplier = 1
		if pricing.InputPricePerMTokenPriority > 0 {
			inputPrice = pricing.InputPricePerMTokenPriority
		}
		if pricing.OutputPricePerMTokenPriority > 0 {
			outputPrice = pricing.OutputPricePerMTokenPriority
		}
		if pricing.CacheReadPricePerMTokenPriority > 0 {
			cacheReadPrice = pricing.CacheReadPricePerMTokenPriority
		}
	}

	if cachedTokens < 0 {
		cachedTokens = 0
	}
	if cachedTokens > inputTokens {
		cachedTokens = inputTokens
	}

	uncachedInputTokens := inputTokens
	if cacheReadPrice > 0 {
		uncachedInputTokens = inputTokens - cachedTokens
	}

	inputCost := float64(uncachedInputTokens) / 1000000.0 * inputPrice
	cacheReadCost := float64(cachedTokens) / 1000000.0 * cacheReadPrice
	outputCost := float64(outputTokens) / 1000000.0 * outputPrice

	return costBreakdown{
		InputCost:                 inputCost * tierMultiplier,
		OutputCost:                outputCost * tierMultiplier,
		CacheReadCost:             cacheReadCost * tierMultiplier,
		TotalCost:                 (inputCost + cacheReadCost + outputCost) * tierMultiplier,
		InputPricePerMToken:       inputPrice * tierMultiplier,
		OutputPricePerMToken:      outputPrice * tierMultiplier,
		CacheReadPricePerMToken:   cacheReadPrice * tierMultiplier,
		ServiceTierCostMultiplier: tierMultiplier,
	}
}

func normalizeBillingModelName(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	model = strings.TrimLeft(model, "/")
	model = strings.TrimPrefix(model, "models/")
	model = strings.TrimPrefix(model, "publishers/google/models/")
	if idx := strings.LastIndex(model, "/publishers/google/models/"); idx != -1 {
		model = model[idx+len("/publishers/google/models/"):]
	}
	if idx := strings.LastIndex(model, "/models/"); idx != -1 {
		model = model[idx+len("/models/"):]
	} else if idx := strings.LastIndex(model, "/"); idx != -1 {
		model = model[idx+1:]
	}
	return strings.TrimLeft(model, "/")
}

func normalizeCodexBillingModel(model string) (string, bool) {
	compact := strings.NewReplacer(" ", "-", "_", "-").Replace(model)
	switch {
	case strings.Contains(compact, "gpt-5.5") || strings.Contains(compact, "gpt5-5") || strings.Contains(compact, "gpt5.5"):
		return "gpt-5.5", true
	case strings.Contains(compact, "gpt-5.4-mini") || strings.Contains(compact, "gpt5-4-mini") || strings.Contains(compact, "gpt5.4-mini"):
		return "gpt-5.4-mini", true
	case strings.Contains(compact, "gpt-5.4-nano") || strings.Contains(compact, "gpt5-4-nano") || strings.Contains(compact, "gpt5.4-nano"):
		return "gpt-5.4-nano", true
	case strings.Contains(compact, "gpt-5.4") || strings.Contains(compact, "gpt5-4") || strings.Contains(compact, "gpt5.4"):
		return "gpt-5.4", true
	case strings.Contains(compact, "gpt-5.2") || strings.Contains(compact, "gpt5-2") || strings.Contains(compact, "gpt5.2"):
		return "gpt-5.2", true
	case strings.Contains(compact, "gpt-5.3-codex-spark") || strings.Contains(compact, "gpt5-3-codex-spark") || strings.Contains(compact, "gpt5.3-codex-spark"):
		return "gpt-5.3-codex-spark", true
	case strings.Contains(compact, "gpt-5.3-codex") || strings.Contains(compact, "gpt5-3-codex") || strings.Contains(compact, "gpt5.3-codex"):
		return "gpt-5.3-codex", true
	case strings.Contains(compact, "gpt-5.3") || strings.Contains(compact, "gpt5-3") || strings.Contains(compact, "gpt5.3"):
		return "gpt-5.3-codex", true
	case strings.Contains(compact, "codex"):
		return "gpt-5.3-codex", true
	case strings.Contains(compact, "gpt-5") || strings.Contains(compact, "gpt5"):
		return "gpt-5.4", true
	default:
		return "", false
	}
}

func modelRulePricing(model string) *ModelPricing {
	bestIdx := -1
	bestLen := -1
	for i := range modelPricingRules {
		rule := modelPricingRules[i]
		if modelMatchesRule(model, rule.model) && len(rule.model) > bestLen {
			bestIdx = i
			bestLen = len(rule.model)
		}
	}
	if bestIdx == -1 {
		return nil
	}
	return &modelPricingRules[bestIdx].pricing
}

func modelMatchesRule(model string, rule string) bool {
	if model == rule {
		return true
	}
	if !strings.HasPrefix(model, rule) {
		return false
	}
	if len(model) == len(rule) {
		return true
	}
	switch model[len(rule)] {
	case '-', '.', ':':
		return true
	default:
		return false
	}
}

func claudeFamilyPricing(model string) *ModelPricing {
	switch {
	case strings.Contains(model, "opus"):
		if strings.Contains(model, "4.7") || strings.Contains(model, "4-7") ||
			strings.Contains(model, "4.6") || strings.Contains(model, "4-6") ||
			strings.Contains(model, "4.5") || strings.Contains(model, "4-5") {
			return &ModelPricing{InputPricePerMToken: 5.0, OutputPricePerMToken: 25.0}
		}
		return &ModelPricing{InputPricePerMToken: 15.0, OutputPricePerMToken: 75.0}
	case strings.Contains(model, "sonnet"):
		return &ModelPricing{InputPricePerMToken: 3.0, OutputPricePerMToken: 15.0}
	case strings.Contains(model, "haiku"):
		if strings.Contains(model, "3-5") || strings.Contains(model, "3.5") {
			return &ModelPricing{InputPricePerMToken: 1.0, OutputPricePerMToken: 5.0}
		}
		return &ModelPricing{InputPricePerMToken: 0.25, OutputPricePerMToken: 1.25}
	case strings.Contains(model, "claude"):
		return &ModelPricing{InputPricePerMToken: 3.0, OutputPricePerMToken: 15.0}
	default:
		return nil
	}
}

func geminiFamilyPricing(model string) *ModelPricing {
	if strings.Contains(model, "gemini-3.1-pro") || strings.Contains(model, "gemini-3-1-pro") {
		return &ModelPricing{InputPricePerMToken: 2.0, OutputPricePerMToken: 12.0}
	}
	return nil
}

func usePriorityPricing(serviceTier string, pricing *ModelPricing) bool {
	if normalizeServiceTier(serviceTier) != "priority" {
		return false
	}
	return pricing.InputPricePerMTokenPriority > 0 ||
		pricing.OutputPricePerMTokenPriority > 0 ||
		pricing.CacheReadPricePerMTokenPriority > 0
}

func serviceTierCostMultiplier(serviceTier string) float64 {
	switch normalizeServiceTier(serviceTier) {
	case "priority":
		return 2.0
	case "flex":
		return 0.5
	default:
		return 1.0
	}
}

func normalizeServiceTier(serviceTier string) string {
	return strings.ToLower(strings.TrimSpace(serviceTier))
}
