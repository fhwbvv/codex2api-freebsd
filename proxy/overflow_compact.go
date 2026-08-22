package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// ==================== 上下文超窗自动压缩 ====================
// 上游对超出模型上下文窗口的输入返回确定性 400（context_length_exceeded），
// 换号重试也必然失败，此前网关只能把错误透传给下游（issue #310/#415）。
// 上游不接受 truncation/context_management 参数（实测 400 Unsupported parameter），
// 服务端截断不可用；不做压缩的客户端（chat UI / SDK）只能新开会话。
//
// 本模块提供网关侧兜底：per-key 开关（limits.auto_compact_overflow）打开时，
// 收到超窗错误后把 input[] 的旧轮次摘要成一条 developer message，保留最近的
// 轮次原文，重试一次。摘要通过内部 Responses 调用完成；摘要失败时退化为
// 直接丢弃旧轮次并插入省略标记（保证重试仍能发出）。

const (
	// overflowCompactTailBytesDefault 压缩后保留原文的“最近轮次”预算（字节）。
	overflowCompactTailBytesDefault = 200 * 1024
	// overflowCompactSummaryInputBytesDefault 送去摘要的旧轮次文本上限（字节），
	// 保证摘要调用自身远离窗口限制。
	overflowCompactSummaryInputBytesDefault = 512 * 1024
	// overflowCompactSummaryTimeout 摘要调用的总超时（覆盖全部缩量重试）。
	overflowCompactSummaryTimeout = 120 * time.Second
	// overflowCompactSummaryMinInputBytes 缩量重试的转写下限：再小的输入已经
	// 承载不了有效信息，继续折半只是白费一次上游往返。
	overflowCompactSummaryMinInputBytes = 32 * 1024
	// overflowCompactSummaryMaxAttempts 摘要调用（含缩量重试）的最大次数。
	overflowCompactSummaryMaxAttempts = 3
	// overflowCompactFallbackDigestBytes 模型摘要彻底失败时,直接摘录旧轮次原文
	// 的预算。远小于 tail 预算,不会把刚压下去的体积重新顶穿。
	overflowCompactFallbackDigestBytes = 16 * 1024
	// overflowCompactMinItems input 少于该条数时不值得压缩（多半是单条超大输入，
	// 压缩救不了，直接透传原错误）。
	overflowCompactMinItems = 4
	// overflowCompactItemTextCap 单条旧轮次拍平后计入摘要转写的文本上限。
	overflowCompactItemTextCap = 8 * 1024
)

const overflowCompactSummaryPrefix = "[Conversation summary from earlier turns]\n"
const overflowCompactOmittedMarker = "[Earlier conversation turns were omitted because the input exceeded the model context window.]"
const overflowCompactDigestPrefix = "[Verbatim excerpt of those turns, heavily truncated and possibly incomplete:]\n"

func overflowCompactTailBytes() int {
	if v := strings.TrimSpace(os.Getenv("CODEX_OVERFLOW_COMPACT_TAIL_KB")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n * 1024
		}
	}
	return overflowCompactTailBytesDefault
}

func overflowCompactSummaryInputBytes() int {
	if v := strings.TrimSpace(os.Getenv("CODEX_OVERFLOW_COMPACT_SUMMARY_INPUT_KB")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n * 1024
		}
	}
	return overflowCompactSummaryInputBytesDefault
}

// autoCompactOverflowEnabled 判断当前请求是否开启超窗自动压缩：
// 全局实验性开关（系统设置 overflow_auto_compact_enabled）或 per-key
// limits.auto_compact_overflow 任一开启即生效。内部摘要调用通过显式上下文标记
// 阻断递归，不能再依赖“没有 Key 身份”这一偶然副作用。
func autoCompactOverflowEnabled(c *gin.Context) bool {
	if c != nil {
		if disabled, exists := c.Get(contextDisableOverflowAutoCompact); exists {
			if value, ok := disabled.(bool); ok && value {
				return false
			}
		}
	}
	row := apiKeyRowFromContext(c)
	if row == nil {
		return false
	}
	if row.Limits.AutoCompactOnOverflow {
		return true
	}
	return CurrentRuntimeSettings().OverflowAutoCompact
}

// isCodexPreflightSSEEvent 判断是否为上游在响应内容前发送的元数据事件：
// codex.*（codex.rate_limits / codex.response.metadata，WS 传输路径）与裸
// response.metadata（HTTP 传输路径），均不携带模型产出。
func isCodexPreflightSSEEvent(eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	if eventType == "response.metadata" {
		return true
	}
	return strings.HasPrefix(eventType, "codex.")
}

// shouldDeferPreContentSSEEvent 决定首个内容事件前的 SSE 事件是否延迟冲刷。
// 生命周期事件（response.created / response.in_progress）始终缓冲；前置元数据
// 事件默认一并缓冲，preflightPassthrough 开启时立即写出（旧版兼容，issue #425）——
// 代价是提前提交 200，该窗口内的 response.failed 无法再按真实错误码返回，
// 也无法走静默换号或超窗压缩重试。
func shouldDeferPreContentSSEEvent(eventType string, contentTokenSeen, gotTerminal, preflightPassthrough bool) bool {
	return !contentTokenSeen && !gotTerminal &&
		(isPreContentLifecycleEvent(eventType) ||
			(!preflightPassthrough && isCodexPreflightSSEEvent(eventType)))
}

// isContextLengthExceededBody 判断上游错误体（HTTP 错误响应或 response.failed
// 的 error 对象）是否为上下文超窗。
func isContextLengthExceededBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	for _, path := range []string{"error.code", "error.type", "code", "detail.code"} {
		if strings.Contains(strings.ToLower(gjson.GetBytes(body, path).String()), "context_length") {
			return true
		}
	}
	for _, path := range []string{"error.message", "message", "detail"} {
		msg := strings.ToLower(gjson.GetBytes(body, path).String())
		if strings.Contains(msg, "exceeds the context window") || strings.Contains(msg, "context length") {
			return true
		}
	}
	return false
}

// isContextLengthExceededFailedPayload 判断 response.failed 事件是否为上下文超窗。
func isContextLengthExceededFailedPayload(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	errObj := gjson.GetBytes(payload, "response.error")
	if !errObj.Exists() {
		return false
	}
	return isContextLengthExceededBody([]byte(errObj.Raw))
}

// compactOverflowResponsesBody 把已准备好的 Codex body 中 input[] 的旧轮次
// 压缩为一条 developer 摘要消息，保留尾部最近轮次原文。返回压缩后的 body。
// 无法压缩（input 太短 / 结构不符合预期）时返回 ok=false，调用方透传原错误。
func (h *Handler) compactOverflowResponsesBody(ctx context.Context, codexBody []byte) ([]byte, bool) {
	return h.compactOverflowResponsesBodyWithAttribution(ctx, codexBody, nil)
}

// compactOverflowResponsesBodyForRequest preserves the authenticated parent
// identity for the model-generated summary while leaving the generic helper
// anonymous for administrative callers and focused unit tests.
func (h *Handler) compactOverflowResponsesBodyForRequest(c *gin.Context, codexBody []byte) ([]byte, bool) {
	if c == nil || c.Request == nil {
		return h.compactOverflowResponsesBody(context.Background(), codexBody)
	}
	attribution := internalResponseAttributionFromRequest(c, internalReasonOverflowCompact)
	return h.compactOverflowResponsesBodyWithAttribution(c.Request.Context(), codexBody, attribution)
}

func (h *Handler) compactOverflowResponsesBodyWithAttribution(ctx context.Context, codexBody []byte, attribution *internalResponseAttribution) ([]byte, bool) {
	var body map[string]any
	if err := json.Unmarshal(codexBody, &body); err != nil {
		return nil, false
	}
	inputItems, ok := body["input"].([]any)
	if !ok || len(inputItems) < overflowCompactMinItems {
		return nil, false
	}

	// 从尾部往前累计字节数，保留 tailBudget 内的最近轮次原文。
	tailBudget := overflowCompactTailBytes()
	cut := len(inputItems)
	used := 0
	for i := len(inputItems) - 1; i >= 0; i-- {
		encoded, err := json.Marshal(inputItems[i])
		if err != nil {
			break
		}
		if used+len(encoded) > tailBudget && cut < len(inputItems) {
			break
		}
		used += len(encoded)
		cut = i
	}
	// 首条 developer/system 消息通常承载系统提示，始终保留原文。
	headStart := 0
	if first, ok := inputItems[0].(map[string]any); ok && isResponsesMessageInputItem(first) {
		switch strings.TrimSpace(firstNonEmptyAnyString(first["role"])) {
		case "developer", "system":
			headStart = 1
		}
	}
	if cut <= headStart {
		// 尾部预算已covers全部旧轮次，没有可压缩的头部——说明超窗来自
		// 单条超大 item 或预算过大，压缩无意义。
		return nil, false
	}
	head := inputItems[headStart:cut]
	tail := inputItems[cut:]

	summaryText := h.summarizeOverflowItems(ctx, firstNonEmptyAnyString(body["model"]), head, attribution)
	replacement := overflowCompactSummaryPrefix + summaryText
	mode := "模型摘要"
	if summaryText == "" {
		replacement, mode = overflowCompactFallbackText(head)
	}

	newInput := make([]any, 0, headStart+1+len(tail))
	newInput = append(newInput, inputItems[:headStart]...)
	newInput = append(newInput, map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": replacement},
		},
	})
	newInput = append(newInput, tail...)
	body["input"] = newInput

	// 压缩边界可能切断工具调用配对（尾部 output 的 call 被摘要掉），复用配对修复。
	repairResponsesToolCallPairing(body)

	result, err := json.Marshal(body)
	if err != nil {
		return nil, false
	}
	log.Printf("超窗自动压缩: 旧轮次 %d 条 -> 摘要(%s), 保留最近 %d 条, body %dKB -> %dKB",
		len(head), mode, len(tail), len(codexBody)/1024, len(result)/1024)
	return result, true
}

// overflowCompactFallbackText 模型摘要不可用时的退化文本。此前只插一句"旧轮次
// 已省略"，整段历史静默蒸发、模型在无上下文下作答且下游无从察觉；改为在省略
// 标记后附一段直接摘录的旧轮次原文（远小于 tail 预算，不会重新顶穿窗口），
// 至少保住用户目标、关键路径与未完成事项。摘录也拿不到时才退回纯标记。
func overflowCompactFallbackText(items []any) (string, string) {
	digest := strings.TrimSpace(flattenOverflowItemsTranscript(items, overflowCompactFallbackDigestBytes))
	if digest == "" {
		return overflowCompactOmittedMarker, "省略标记"
	}
	return overflowCompactOmittedMarker + "\n" + overflowCompactDigestPrefix + digest, "机械摘录"
}

// summarizeOverflowItems 用内部 Responses 调用把旧轮次转写摘要成一段文本。
//
// 摘要调用自身也可能超窗：转写上限是固定字节数，而模型窗口因账号/映射而异，
// 小窗口模型上 512KB 转写必然再次 400。此时按实际转写长度折半重试，直到成功、
// 触及下限或用尽次数——比"一次失败就丢弃全部旧轮次"保住多得多的上下文。
// 其余失败（鉴权、无可用账号、限流、无文本输出）与输入体积无关，不重试。
// 全部尝试共享一个 overflowCompactSummaryTimeout 截止时间，最坏耗时不变。
// 返回空串表示彻底失败，调用方退化为机械摘录。
func (h *Handler) summarizeOverflowItems(ctx context.Context, model string, items []any, attribution *internalResponseAttribution) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	callCtx, cancel := context.WithTimeout(ctx, overflowCompactSummaryTimeout)
	defer cancel()

	budget := overflowCompactSummaryInputBytes()
	for attempt := 1; attempt <= overflowCompactSummaryMaxAttempts; attempt++ {
		transcript := flattenOverflowItemsTranscript(items, budget)
		if strings.TrimSpace(transcript) == "" {
			return ""
		}
		text, status, respBody := h.requestOverflowSummary(callCtx, model, transcript, attribution)
		if text != "" {
			return text
		}
		if callCtx.Err() != nil {
			log.Printf("超窗自动压缩: 摘要调用超时 (attempt %d), 退化为机械摘录", attempt)
			return ""
		}
		if !overflowSummaryFailedForContextWindow(status, respBody) {
			if status == 200 {
				log.Printf("超窗自动压缩: 摘要调用无文本输出, 退化为机械摘录")
			} else {
				log.Printf("超窗自动压缩: 摘要调用失败 (status %d), 退化为机械摘录", status)
			}
			return ""
		}
		// 按实际转写长度折半，保证严格缩小（budget 可能远大于转写本身）。
		next := len(transcript) / 2
		if attempt == overflowCompactSummaryMaxAttempts || next < overflowCompactSummaryMinInputBytes {
			log.Printf("超窗自动压缩: 摘要调用自身超窗且已无法继续缩量 (attempt %d, %dKB), 退化为机械摘录", attempt, len(transcript)/1024)
			return ""
		}
		log.Printf("超窗自动压缩: 摘要调用自身超窗 (attempt %d, %dKB), 缩量至 %dKB 重试", attempt, len(transcript)/1024, next/1024)
		budget = next
	}
	return ""
}

// overflowSummaryFailedForContextWindow 判断摘要调用的失败是否源于其自身输入
// 超窗——只有这一类失败缩量重试才有意义。非流式错误体是 JSON，可直接判定；
// 流式响应体是 SSE 文本，gjson 解析不了整体，用错误码子串兜底。
func overflowSummaryFailedForContextWindow(status int, body []byte) bool {
	if status == 200 || len(body) == 0 {
		return false
	}
	if isContextLengthExceededBody(body) {
		return true
	}
	lowered := strings.ToLower(string(body))
	return strings.Contains(lowered, "context_length") || strings.Contains(lowered, "exceeds the context window")
}

// requestOverflowSummary 执行一次摘要调用，返回提取出的摘要文本与原始状态/响应体
// （供调用方判定失败原因）。
func (h *Handler) requestOverflowSummary(ctx context.Context, model, transcript string, attribution *internalResponseAttribution) (string, int, []byte) {
	reqBody, err := json.Marshal(map[string]any{
		"model":     model,
		"stream":    true,
		"reasoning": map[string]any{"effort": "low"},
		"input": []any{
			map[string]any{
				"type":    "message",
				"role":    "developer",
				"content": []any{map[string]any{"type": "input_text", "text": "You are a conversation compaction assistant. Summarize the earlier conversation transcript provided by the user into a dense briefing for the assistant that will continue this conversation. Preserve: key facts and decisions, user goals and constraints, file paths, code identifiers, tool results that matter, and unresolved tasks. Output only the summary text."}},
			},
			map[string]any{
				"type":    "message",
				"role":    "user",
				"content": []any{map[string]any{"type": "input_text", "text": transcript}},
			},
		},
	})
	if err != nil {
		return "", 0, nil
	}
	status, respBody := h.executeInternalResponseWithAttribution(ctx, reqBody, attribution)
	if status != 200 {
		return "", status, respBody
	}
	return strings.TrimSpace(extractResponsesSSEOutputText(respBody)), status, respBody
}

// flattenOverflowItemsTranscript 把 input items 拍平成"role: text"转写文本，
// 单条超长内容截断，总量超出 cap 时保留头尾、丢弃中段。
func flattenOverflowItemsTranscript(items []any, capBytes int) string {
	var lines []string
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ := strings.TrimSpace(firstNonEmptyAnyString(item["type"]))
		switch {
		case isResponsesMessageInputItem(item):
			role := strings.TrimSpace(firstNonEmptyAnyString(item["role"]))
			if role == "" {
				role = "user"
			}
			text := flattenMessageContentText(item["content"])
			if text != "" {
				lines = append(lines, role+": "+capText(text, overflowCompactItemTextCap))
			}
		case isCodexToolCallContextType(typ):
			name := strings.TrimSpace(firstNonEmptyAnyString(item["name"]))
			args := capText(firstNonEmptyAnyString(item["arguments"]), 1024)
			lines = append(lines, "assistant tool call "+name+"("+args+")")
		case isCodexToolCallOutputType(typ):
			lines = append(lines, "tool output: "+capText(flattenToolOutputText(item["output"]), overflowCompactItemTextCap))
		}
	}
	transcript := strings.Join(lines, "\n")
	if len(transcript) <= capBytes {
		return transcript
	}
	// 头尾各留一半，中段用标记替换。
	half := capBytes / 2
	return transcript[:half] + "\n[... middle of the transcript truncated ...]\n" + transcript[len(transcript)-half:]
}

// flattenMessageContentText 把 message content（string 或 content parts 数组）拍平成文本。
func flattenMessageContentText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, raw := range v {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if text := firstNonEmptyAnyString(part["text"]); text != "" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(text)
			}
		}
		return sb.String()
	}
	return ""
}

func capText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]"
}

// extractResponsesSSEOutputText 从内部 Responses 调用的 SSE 响应体中提取
// response.completed 的全部 output_text。
func extractResponsesSSEOutputText(sse []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(sse))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		payload := line[len("data: "):]
		if gjson.GetBytes(payload, "type").String() != "response.completed" {
			continue
		}
		var sb strings.Builder
		gjson.GetBytes(payload, "response.output").ForEach(func(_, item gjson.Result) bool {
			if item.Get("type").String() != "message" {
				return true
			}
			item.Get("content").ForEach(func(_, part gjson.Result) bool {
				if part.Get("type").String() == "output_text" {
					sb.WriteString(part.Get("text").String())
				}
				return true
			})
			return true
		})
		return sb.String()
	}
	return ""
}
