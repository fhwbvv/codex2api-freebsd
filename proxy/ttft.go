package proxy

// isFirstTokenEvent 判断 codex SSE 事件是否代表"首个有内容产出"，用于 TTFT 计时。
//
// TTFT 的语义是"上游开始向客户端产出内容的时间点"，不应限定为文本。
// 现实中很多请求并不会出 text.delta：
//   - 纯工具调用：仅 function_call_arguments.delta / output_item.added
//   - 图像生成：image_generation_call.partial_image
//   - reasoning-only / 推理型模型：先输出 reasoning_text.delta 才到 text
//   - 流首字之前断开：永远等不到 text.delta
//
// 因此采用"黑名单"策略：排除控制事件（created / in_progress）和
// 流终止事件（completed / failed），其余任何事件都视为首字。
// 与 sub2api 的"任何非空、非 [DONE]、非 usage-only 行都算首字"语义一致。
func isFirstTokenEvent(eventType string) bool {
	switch eventType {
	case "":
		return false
	case "response.created",
		"response.in_progress",
		"response.completed",
		"response.failed":
		return false
	}
	return true
}
