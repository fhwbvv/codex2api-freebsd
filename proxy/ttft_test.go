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

		// 应触发首字 —— 明确的内容增量事件
		{"function_call_arguments_delta", "response.function_call_arguments.delta", true},
		{"image_partial", "response.image_generation_call.partial_image", true},
		{"reasoning_text_delta", "response.reasoning_text.delta", true},
		{"reasoning_summary_text_delta", "response.reasoning_summary_text.delta", true},
		{"reasoning_encrypted_delta", "response.reasoning.encrypted_content.delta", true},

		// 已有路径继续命中
		{"output_text_delta", "response.output_text.delta", true},
		{"output_text_done", "response.output_text.done", true},

		// 结构事件仅靠 type 不应触发首字
		{"function_call_arguments_done", "response.function_call_arguments.done", false},
		{"output_item_added", "response.output_item.added", false},
		{"output_item_done", "response.output_item.done", false},
		{"content_part_added", "response.content_part.added", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFirstTokenEvent(tc.eventType); got != tc.want {
				t.Fatalf("isFirstTokenEvent(%q) = %v, want %v", tc.eventType, got, tc.want)
			}
		})
	}
}

func TestIsFirstTokenPayload(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"created", []byte(`{"type":"response.created"}`), false},
		{"in_progress", []byte(`{"type":"response.in_progress"}`), false},
		{"output_item_added_function_call", []byte(`{"type":"response.output_item.added","item":{"type":"function_call","name":"lookup"}}`), false},
		{"output_item_done_empty_reasoning", []byte(`{"type":"response.output_item.done","item":{"type":"reasoning"}}`), false},
		{"content_part_added", []byte(`{"type":"response.content_part.added","part":{"type":"output_text","text":""}}`), false},
		{"output_text_delta", []byte(`{"type":"response.output_text.delta","delta":"hello"}`), true},
		{"function_call_arguments_delta", []byte(`{"type":"response.function_call_arguments.delta","delta":"{\"city\""}`), true},
		{"function_call_arguments_done", []byte(`{"type":"response.function_call_arguments.done","arguments":"{}"}`), true},
		{"image_partial", []byte(`{"type":"response.image_generation_call.partial_image","partial_image_b64":"abc"}`), true},
		{"output_item_done_image", []byte(`{"type":"response.output_item.done","item":{"type":"image_generation_call","result":"abc"}}`), true},
		{"output_item_done_message", []byte(`{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"done"}]}}`), true},
		{"content_part_done_text", []byte(`{"type":"response.content_part.done","part":{"type":"output_text","text":"done"}}`), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFirstTokenPayload(tc.data); got != tc.want {
				t.Fatalf("isFirstTokenPayload(%s) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}
