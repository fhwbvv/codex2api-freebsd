package database

import (
	"encoding/json"
	"strings"
	"sync/atomic"
)

// 模型定价覆盖来源。custom 由管理员手填(最高优先级)，synced 由「从 JSON URL 同步」写入。
const (
	ModelPricingSourceCustom = "custom"
	ModelPricingSourceSynced = "synced"
)

// ModelPricingOverride 是单个模型的定价覆盖（设置存储与同步 URL 载荷共用同一形态）。
// 价格单位：USD / 1M tokens。字段为 0（未填）时该项回退代码默认价，实现"部分覆盖"。
// 优先级：custom > synced > 代码默认。
type ModelPricingOverride struct {
	Source string `json:"source,omitempty"`

	// 标准档（短上下文）
	Input       float64 `json:"input,omitempty"`
	CachedInput float64 `json:"cached_input,omitempty"`
	Output      float64 `json:"output,omitempty"`

	// priority(fast) 档
	InputPriority       float64 `json:"input_priority,omitempty"`
	CachedInputPriority float64 `json:"cached_input_priority,omitempty"`
	OutputPriority      float64 `json:"output_priority,omitempty"`

	// 长上下文档（input > 272K）
	InputLong       float64 `json:"input_long,omitempty"`
	CachedInputLong float64 `json:"cached_input_long,omitempty"`
	OutputLong      float64 `json:"output_long,omitempty"`
}

// IsEmpty 判断覆盖是否不含任何价格（全 0）。
func (o ModelPricingOverride) IsEmpty() bool {
	return o.Input == 0 && o.CachedInput == 0 && o.Output == 0 &&
		o.InputPriority == 0 && o.CachedInputPriority == 0 && o.OutputPriority == 0 &&
		o.InputLong == 0 && o.CachedInputLong == 0 && o.OutputLong == 0
}

// applyNonZero 把覆盖里非 0 的价格字段写入 p（就地）。0 字段保持 p 原值（代码默认）。
func (o ModelPricingOverride) applyNonZero(p *ModelPricing) {
	if o.Input > 0 {
		p.InputPricePerMToken = o.Input
	}
	if o.CachedInput > 0 {
		p.CacheReadPricePerMToken = o.CachedInput
	}
	if o.Output > 0 {
		p.OutputPricePerMToken = o.Output
	}
	if o.InputPriority > 0 {
		p.InputPricePerMTokenPriority = o.InputPriority
	}
	if o.CachedInputPriority > 0 {
		p.CacheReadPricePerMTokenPriority = o.CachedInputPriority
	}
	if o.OutputPriority > 0 {
		p.OutputPricePerMTokenPriority = o.OutputPriority
	}
	if o.InputLong > 0 {
		p.LongInputPricePerMToken = o.InputLong
	}
	if o.CachedInputLong > 0 {
		p.LongCacheReadPricePerMToken = o.CachedInputLong
	}
	if o.OutputLong > 0 {
		p.LongOutputPricePerMToken = o.OutputLong
	}
}

// ModelPricingOverrideFromPricing 把一份完整 ModelPricing 投影为覆盖 JSON 形态，
// 供管理端展示"当前生效价"与编辑初值。
func ModelPricingOverrideFromPricing(p *ModelPricing, source string) ModelPricingOverride {
	if p == nil {
		return ModelPricingOverride{Source: source}
	}
	return ModelPricingOverride{
		Source:              source,
		Input:               p.InputPricePerMToken,
		CachedInput:         p.CacheReadPricePerMToken,
		Output:              p.OutputPricePerMToken,
		InputPriority:       p.InputPricePerMTokenPriority,
		CachedInputPriority: p.CacheReadPricePerMTokenPriority,
		OutputPriority:      p.OutputPricePerMTokenPriority,
		InputLong:           p.LongInputPricePerMToken,
		CachedInputLong:     p.LongCacheReadPricePerMToken,
		OutputLong:          p.LongOutputPricePerMToken,
	}
}

// pricingOverrides 存 map[string]ModelPricingOverride（key 为规范化模型名，小写）。
var pricingOverrides atomic.Value

// SetModelPricingOverrides 刷新运行时定价覆盖表（key 归一为小写去空白）。
func SetModelPricingOverrides(m map[string]ModelPricingOverride) {
	norm := make(map[string]ModelPricingOverride, len(m))
	for k, v := range m {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" || v.IsEmpty() {
			continue
		}
		norm[key] = v
	}
	pricingOverrides.Store(norm)
}

func currentModelPricingOverrides() map[string]ModelPricingOverride {
	if v, ok := pricingOverrides.Load().(map[string]ModelPricingOverride); ok {
		return v
	}
	return nil
}

// lookupModelPricingOverride 按规范化模型名查覆盖。
func lookupModelPricingOverride(canonical string) (ModelPricingOverride, bool) {
	m := currentModelPricingOverrides()
	if m == nil {
		return ModelPricingOverride{}, false
	}
	ov, ok := m[strings.ToLower(strings.TrimSpace(canonical))]
	return ov, ok
}

// CanonicalBillingModelKey 返回某模型用于定价查找/覆盖的规范键（小写）。
func CanonicalBillingModelKey(model string) string {
	normalized := normalizeBillingModelName(model)
	if codexModel, ok := normalizeCodexBillingModel(normalized); ok {
		return strings.ToLower(codexModel)
	}
	return strings.ToLower(normalized)
}

// ModelPricingSourceFor 返回某规范键当前定价来源：custom / synced / default。
func ModelPricingSourceFor(canonical string) string {
	if ov, ok := lookupModelPricingOverride(canonical); ok {
		if ov.Source == ModelPricingSourceCustom {
			return ModelPricingSourceCustom
		}
		return ModelPricingSourceSynced
	}
	return "default"
}

// ParseModelPricingOverridesJSON 解析存储的 JSON blob（model → override）。
// 空串返回空 map、nil error。
func ParseModelPricingOverridesJSON(s string) (map[string]ModelPricingOverride, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" {
		return map[string]ModelPricingOverride{}, nil
	}
	var raw map[string]ModelPricingOverride
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, err
	}
	out := make(map[string]ModelPricingOverride, len(raw))
	for k, v := range raw {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" || v.IsEmpty() {
			continue
		}
		out[key] = v
	}
	return out, nil
}

// MarshalModelPricingOverridesJSON 序列化覆盖表为存储用 JSON。
func MarshalModelPricingOverridesJSON(m map[string]ModelPricingOverride) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	buf, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}
