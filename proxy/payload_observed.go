package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// ==================== 透传 instructions 观测 ====================
//
// 网关自身没有默认系统提示词：/v1/responses 的 instructions 由客户端透传。
// 这里在规则改写前记录最近观测到的原始 instructions 样本，供管理端展示，
// 让管理员配置追加/覆盖规则时能看到客户端实际发来的提示词原文。

const (
	observedInstructionsMaxSamples = 8
	observedInstructionsMaxLength  = 32 * 1024
)

// ObservedInstructionsSample 一条观测样本。
type ObservedInstructionsSample struct {
	Model        string    `json:"model"`
	Originator   string    `json:"originator"`
	Instructions string    `json:"instructions"`
	Length       int       `json:"length"`
	Truncated    bool      `json:"truncated"`
	ObservedAt   time.Time `json:"observed_at"`
}

var (
	observedInstructionsMu      sync.Mutex
	observedInstructionsSamples []ObservedInstructionsSample
	observedInstructionsIndex   = map[string]int{} // 去重键 → samples 下标
)

// RecordObservedInstructions 记录请求体中的原始 instructions（改写前调用）。
// 按 (模型, originator, 内容哈希) 去重，只保留最近若干条。
func RecordObservedInstructions(body []byte, headers http.Header) {
	instructions := gjson.GetBytes(body, "instructions").String()
	if instructions == "" {
		return
	}
	model := gjson.GetBytes(body, "model").String()
	originator := ""
	if headers != nil {
		originator = headers.Get("Originator")
		if originator == "" {
			originator = headers.Get("User-Agent")
		}
	}
	sum := sha256.Sum256([]byte(instructions))
	key := model + "|" + originator + "|" + hex.EncodeToString(sum[:8])

	sample := ObservedInstructionsSample{
		Model:        model,
		Originator:   originator,
		Instructions: instructions,
		Length:       len(instructions),
		ObservedAt:   time.Now(),
	}
	if len(sample.Instructions) > observedInstructionsMaxLength {
		sample.Instructions = sample.Instructions[:observedInstructionsMaxLength]
		sample.Truncated = true
	}

	observedInstructionsMu.Lock()
	defer observedInstructionsMu.Unlock()
	if idx, ok := observedInstructionsIndex[key]; ok {
		observedInstructionsSamples[idx].ObservedAt = sample.ObservedAt
		return
	}
	if len(observedInstructionsSamples) >= observedInstructionsMaxSamples {
		// 淘汰最旧样本
		oldest := 0
		for i := 1; i < len(observedInstructionsSamples); i++ {
			if observedInstructionsSamples[i].ObservedAt.Before(observedInstructionsSamples[oldest].ObservedAt) {
				oldest = i
			}
		}
		for k, idx := range observedInstructionsIndex {
			if idx == oldest {
				delete(observedInstructionsIndex, k)
			} else if idx > oldest {
				observedInstructionsIndex[k] = idx - 1
			}
		}
		observedInstructionsSamples = append(observedInstructionsSamples[:oldest], observedInstructionsSamples[oldest+1:]...)
	}
	observedInstructionsSamples = append(observedInstructionsSamples, sample)
	observedInstructionsIndex[key] = len(observedInstructionsSamples) - 1
}

// ObservedInstructions 返回最近观测样本（新→旧）。
func ObservedInstructions() []ObservedInstructionsSample {
	observedInstructionsMu.Lock()
	defer observedInstructionsMu.Unlock()
	out := make([]ObservedInstructionsSample, len(observedInstructionsSamples))
	copy(out, observedInstructionsSamples)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
