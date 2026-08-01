package proxy

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// 本文件量化 Grok 投递前置链路（normalize → sanitize → turnIndex）在大请求体下的
// 开销。fixture 按实抓的官方 CLI 会话形态构造：多轮交错 message / reasoning /
// function_call / function_call_output，reasoning 带密文，单轮 ~4KB，
// 实测第 5 轮请求体已达 ~800KB。

// grokBodyTurn 是一轮对话在 input[] 里产生的四个历史项。
// dirty=true 时按 Codex 实际发送的形态带上扩展字段（id/status/metadata/null），
// 这些字段会触发 rebuildGrokHistoryItem 重建；dirty=false 时已是 Grok 原生字段集。
func grokBodyTurn(b *strings.Builder, idx int, dirty bool) {
	text := strings.Repeat("analyze this module carefully ", 40)
	blob := strings.Repeat("QUFBQkJCQ0NDRERE", 128)
	summary := strings.Repeat("considering the call graph ", 20)

	fmt.Fprintf(b, `{"type":"message","role":"user","content":[{"type":"input_text","text":"%s"}]}`, text)

	b.WriteString(",")
	if dirty {
		fmt.Fprintf(b, `{"type":"reasoning","id":"rs_%d","summary":[{"type":"summary_text","text":"%s"}],"content":null,"encrypted_content":"%s","status":"completed"}`, idx, summary, blob)
	} else {
		fmt.Fprintf(b, `{"type":"reasoning","id":"rs_%d","summary":[{"type":"summary_text","text":"%s"}],"encrypted_content":"%s"}`, idx, summary, blob)
	}

	b.WriteString(",")
	if dirty {
		fmt.Fprintf(b, `{"type":"function_call","id":"fc_%d","status":"completed","call_id":"c%d","name":"read_file","arguments":"{\"target_file\":\"proxy/handler.go\"}","internal_chat_message_metadata_passthrough":{"turn_id":"t%d"}}`, idx, idx, idx)
	} else {
		fmt.Fprintf(b, `{"type":"function_call","call_id":"c%d","name":"read_file","arguments":"{\"target_file\":\"proxy/handler.go\"}"}`, idx)
	}

	b.WriteString(",")
	if dirty {
		fmt.Fprintf(b, `{"type":"function_call_output","id":"fco_%d","call_id":"c%d","output":"%s","status":"completed"}`, idx, idx, text)
	} else {
		fmt.Fprintf(b, `{"type":"function_call_output","call_id":"c%d","output":"%s"}`, idx, text)
	}
}

// buildGrokCodexBody 生成目标字节数附近的 Codex 形态 Responses 体。
// dirty 决定历史项是否携带需要重建的 Codex 扩展字段。
func buildGrokCodexBody(targetBytes int, dirty bool) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"grok-4.5","include":["reasoning.encrypted_content"],"stream":true,"store":false,`)
	b.WriteString(`"client_metadata":{"cli":"codex"},"prompt_cache_key":"pk-1","service_tier":"priority","safety_identifier":"sid-1",`)
	b.WriteString(`"reasoning":{"effort":"xhigh","summary":"detailed"},"input":[`)
	for i := 0; b.Len() < targetBytes; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		grokBodyTurn(&b, i, dirty)
	}
	b.WriteString(`],"tools":[{"type":"function","name":"read_file","parameters":{"type":"object","properties":{}}}],"tool_choice":"auto"}`)
	return []byte(b.String())
}

// TestGrokBenchFixtureShape 锁住 fixture 的前提：dirty 变体确实触发重建、
// clean 变体确实不触发。否则基准数字会失去意义。
func TestGrokBenchFixtureShape(t *testing.T) {
	dirty := buildGrokCodexBody(64*1024, true)
	if !gjson.ValidBytes(dirty) {
		t.Fatalf("dirty fixture 不是合法 JSON")
	}
	out, _ := normalizeGrokUpstreamTools(dirty)
	if gjson.GetBytes(out, "input.2.id").Exists() || gjson.GetBytes(out, "input.2.status").Exists() {
		t.Fatalf("dirty fixture 未被重建: %s", gjson.GetBytes(out, "input.2").Raw)
	}

	clean := buildGrokCodexBody(64*1024, false)
	if !gjson.ValidBytes(clean) {
		t.Fatalf("clean fixture 不是合法 JSON")
	}
	cleanOut, _ := normalizeGrokUpstreamTools(clean)
	if len(cleanOut) != len(clean) {
		t.Fatalf("clean fixture 本不该被改写，却发生了重建 (%d → %d)", len(clean), len(cleanOut))
	}
}

func benchGrokSizes() []struct {
	name  string
	bytes int
} {
	return []struct {
		name  string
		bytes int
	}{
		{"100KB", 100 * 1024},
		{"800KB", 800 * 1024},
	}
}

// BenchmarkGrokNormalizeDirty 是真实 Codex 流量的主场景：历史项带扩展字段，
// 必须完整 Unmarshal → 重建 → Marshal。
func BenchmarkGrokNormalizeDirty(b *testing.B) {
	for _, size := range benchGrokSizes() {
		body := buildGrokCodexBody(size.bytes, true)
		b.Run(size.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				out, aliases := normalizeGrokUpstreamTools(body)
				if len(out) == 0 {
					b.Fatal("空结果")
				}
				_ = aliases
			}
		})
	}
}

// BenchmarkGrokNormalizeClean 量化"body 本来就干净"时被白做的那次全量 Unmarshal
// （方案 A 精确化触发条件能省下的部分）。
func BenchmarkGrokNormalizeClean(b *testing.B) {
	for _, size := range benchGrokSizes() {
		body := buildGrokCodexBody(size.bytes, false)
		b.Run(size.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				out, aliases := normalizeGrokUpstreamTools(body)
				if len(out) == 0 {
					b.Fatal("空结果")
				}
				_ = aliases
			}
		})
	}
}

// BenchmarkGrokSanitize 量化 sanitize 链上多次 sjson 全量重分配的代价。
func BenchmarkGrokSanitize(b *testing.B) {
	for _, size := range benchGrokSizes() {
		body := buildGrokCodexBody(size.bytes, true)
		b.Run(size.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				if out := sanitizeGrokRequestBody(body); len(out) == 0 {
					b.Fatal("空结果")
				}
			}
		})
	}
}

// BenchmarkGrokTurnIndex 量化独立于 normalize 的第二次 input[] 全量遍历（#4）。
func BenchmarkGrokTurnIndex(b *testing.B) {
	for _, size := range benchGrokSizes() {
		body := buildGrokCodexBody(size.bytes, true)
		b.Run(size.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				if grokTurnIndex(body) < 1 {
					b.Fatal("轮次为 0")
				}
			}
		})
	}
}

// BenchmarkGrokPreflightChainLegacy 是逐步改写实现的前置链路，作为优化前的基线。
func BenchmarkGrokPreflightChainLegacy(b *testing.B) {
	for _, size := range benchGrokSizes() {
		body := buildGrokCodexBody(size.bytes, true)
		b.Run(size.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				out, aliases := normalizeGrokUpstreamTools(body)
				out = sanitizeGrokRequestBody(out)
				turn := grokTurnIndex(out)
				model := gjson.GetBytes(out, "model").String()
				if len(out) == 0 || turn < 1 || model == "" {
					b.Fatal("前置链路结果异常")
				}
				_ = aliases
			}
		})
	}
}

// BenchmarkGrokPreflightChain 是 ExecuteGrokRequest 实际走的单次遍历前置链路。
func BenchmarkGrokPreflightChain(b *testing.B) {
	for _, size := range benchGrokSizes() {
		body := buildGrokCodexBody(size.bytes, true)
		b.Run(size.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				res := prepareGrokUpstreamBody(body)
				if len(res.Body) == 0 || res.TurnIndex < 1 || res.Model == "" {
					b.Fatal("前置链路结果异常")
				}
			}
		})
	}
}

// BenchmarkGrokPreflightChainClean 覆盖已符合原生契约的请求体：应几乎只有一次
// raw 拷贝的成本。
func BenchmarkGrokPreflightChainClean(b *testing.B) {
	for _, size := range benchGrokSizes() {
		body := buildGrokCodexBody(size.bytes, false)
		b.Run(size.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				res := prepareGrokUpstreamBody(body)
				if len(res.Body) == 0 || res.TurnIndex < 1 {
					b.Fatal("前置链路结果异常")
				}
			}
		})
	}
}

// BenchmarkGrokJSONRoundTrip 把 normalize 的开销拆成 Unmarshal / Marshal 两段，
// 用于判断瓶颈到底在解码、重建循环还是编码。
func BenchmarkGrokJSONRoundTrip(b *testing.B) {
	body := buildGrokCodexBody(800*1024, true)

	b.Run("Unmarshal/800KB", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				b.Fatal(err)
			}
		}
	})

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		b.Fatal(err)
	}
	b.Run("Marshal/800KB", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			if _, err := json.Marshal(payload); err != nil {
				b.Fatal(err)
			}
		}
	})
}
