package proxy

import (
	"strings"

	"github.com/tidwall/gjson"
)

// isFirstTokenEvent 判断 codex SSE 事件类型是否可能代表"首个有内容产出"。
// 纯生命周期/结构事件不算首 token；完整 payload 场景应优先使用 isFirstTokenResult。
func isFirstTokenEvent(eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	switch eventType {
	case "",
		"response.created",
		"response.in_progress",
		"response.completed",
		"response.failed",
		"response.incomplete",
		"response.cancelled",
		"response.canceled",
		"response.output_item.added",
		"response.output_item.done",
		"response.content_part.added":
		return false
	}
	if strings.Contains(eventType, ".delta") {
		return true
	}
	if strings.HasPrefix(eventType, "response.output_text") {
		return true
	}
	if strings.HasPrefix(eventType, "response.output") {
		return true
	}
	if strings.HasPrefix(eventType, "response.image_generation_call.") {
		return true
	}
	return false
}

// isPreContentLifecycleEvent 判断一个 SSE 事件是否为纯前置生命周期帧
// （response.created / response.in_progress）——它们不携带任何模型产出。
//
// 这是唯一可以在"流可见地开始"之前短暂缓冲的帧：缓冲它们，才能在首个真实
// token 到来前遇到可重试的上游故障时，静默换号重试且客户端不会看到"假开始"。
//
// 除此之外的所有帧——包括标志响应已开始的结构帧 response.output_item.added /
// response.content_part.added，以及随后的 reasoning 帧——都必须立即下发给客户端
// 以保证响应及时性。把它们一直压到"首个内容 token"才下发（issue #207），会让
// 推理型请求在整个思考阶段看起来像卡死。
func isPreContentLifecycleEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.created", "response.in_progress":
		return true
	}
	return false
}

func isFirstTokenPayload(data []byte) bool {
	return isFirstTokenResult(gjson.ParseBytes(data))
}

func isFirstTokenResult(parsed gjson.Result) bool {
	eventType := strings.TrimSpace(parsed.Get("type").String())
	switch eventType {
	case "response.output_item.done":
		return outputItemHasFirstTokenContent(parsed.Get("item"))
	case "response.content_part.done":
		return contentPartHasFirstTokenContent(parsed.Get("part"))
	case "response.function_call_arguments.done":
		return stringFieldHasValue(parsed, "arguments")
	}
	if strings.Contains(eventType, ".delta") {
		return deltaEventHasContent(parsed)
	}
	if eventType == "response.image_generation_call.partial_image" {
		return stringFieldHasValue(parsed, "partial_image_b64") ||
			stringFieldHasValue(parsed, "partial_image")
	}
	if strings.HasPrefix(eventType, "response.output_text") {
		return stringFieldHasValue(parsed, "text") ||
			stringFieldHasValue(parsed, "delta") ||
			isFirstTokenEvent(eventType)
	}
	return isFirstTokenEvent(eventType)
}

func isFirstTokenResultForMode(parsed gjson.Result, mode string) bool {
	if NormalizeFirstTokenMode(mode) != FirstTokenModeLoose {
		return isFirstTokenResult(parsed)
	}
	return isLooseFirstTokenResult(parsed)
}

func isLooseFirstTokenResult(parsed gjson.Result) bool {
	eventType := strings.TrimSpace(parsed.Get("type").String())
	if eventType == "" || isPreContentLifecycleEvent(eventType) {
		return false
	}
	switch eventType {
	case "error",
		"response.error",
		"response.completed",
		"response.failed",
		"response.incomplete",
		"response.cancelled",
		"response.canceled":
		return false
	default:
		return true
	}
}

func deltaEventHasContent(parsed gjson.Result) bool {
	return stringFieldHasValue(parsed, "delta") ||
		stringFieldHasValue(parsed, "partial_image_b64") ||
		stringFieldHasValue(parsed, "partial_image")
}

func outputItemHasFirstTokenContent(item gjson.Result) bool {
	if !item.Exists() {
		return false
	}
	switch strings.TrimSpace(item.Get("type").String()) {
	case "message":
		return contentArrayHasFirstTokenContent(item.Get("content"))
	case "function_call":
		return stringFieldHasValue(item, "arguments")
	case "image_generation_call":
		return stringFieldHasValue(item, "result") ||
			stringFieldHasValue(item, "partial_image_b64") ||
			stringFieldHasValue(item, "partial_image")
	case "reasoning":
		return stringFieldHasValue(item, "encrypted_content") ||
			contentArrayHasFirstTokenContent(item.Get("summary"))
	default:
		return false
	}
}

func contentArrayHasFirstTokenContent(content gjson.Result) bool {
	if !content.IsArray() {
		return contentPartHasFirstTokenContent(content)
	}
	for _, part := range content.Array() {
		if contentPartHasFirstTokenContent(part) {
			return true
		}
	}
	return false
}

func contentPartHasFirstTokenContent(part gjson.Result) bool {
	if !part.Exists() {
		return false
	}
	return stringFieldHasValue(part, "text") ||
		stringFieldHasValue(part, "output_text") ||
		stringFieldHasValue(part, "refusal") ||
		stringFieldHasValue(part, "summary_text") ||
		stringFieldHasValue(part, "encrypted_content")
}

func stringFieldHasValue(result gjson.Result, path string) bool {
	field := result.Get(path)
	return field.Exists() && len(field.String()) > 0
}
