package proxy

import "testing"

func TestIsFirstTokenEvent(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		want      bool
	}{
		// 不应触发首字（控制 / 终止事件）
		{"empty", "", false},
		{"created", "response.created", false},
		{"in_progress", "response.in_progress", false},
		{"completed", "response.completed", false},
		{"failed", "response.failed", false},

		// 应触发首字 —— 修复前漏掉的关键场景
		{"function_call_arguments_delta", "response.function_call_arguments.delta", true},
		{"function_call_arguments_done", "response.function_call_arguments.done", true},
		{"output_item_added", "response.output_item.added", true},
		{"output_item_done", "response.output_item.done", true},
		{"image_partial", "response.image_generation_call.partial_image", true},
		{"reasoning_text_delta", "response.reasoning_text.delta", true},
		{"reasoning_summary_text_delta", "response.reasoning_summary_text.delta", true},
		{"reasoning_encrypted_delta", "response.reasoning.encrypted_content.delta", true},
		{"content_part_added", "response.content_part.added", true},

		// 已有路径继续命中
		{"output_text_delta", "response.output_text.delta", true},
		{"output_text_done", "response.output_text.done", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFirstTokenEvent(tc.eventType); got != tc.want {
				t.Fatalf("isFirstTokenEvent(%q) = %v, want %v", tc.eventType, got, tc.want)
			}
		})
	}
}
