package proxy

import (
	"testing"

	"github.com/tidwall/gjson"
)

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

func TestIsPreContentLifecycleEvent(t *testing.T) {
	cases := []struct {
		eventType string
		want      bool
	}{
		// 仅 created / in_progress 属于可短暂缓冲的前置生命周期帧
		{"response.created", true},
		{"response.in_progress", true},
		{" response.created ", true},
		// 结构帧标志响应已开始，必须立即下发
		{"response.output_item.added", false},
		{"response.content_part.added", false},
		// reasoning / 内容增量必须立即下发
		{"response.reasoning_summary_text.delta", false},
		{"response.output_text.delta", false},
		// 终止帧不属于前置生命周期
		{"response.completed", false},
		{"response.failed", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isPreContentLifecycleEvent(tc.eventType); got != tc.want {
			t.Fatalf("isPreContentLifecycleEvent(%q) = %v, want %v", tc.eventType, got, tc.want)
		}
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

func TestIsFirstTokenResultForMode(t *testing.T) {
	cases := []struct {
		name       string
		data       string
		mode       string
		wantStrict bool
		wantMode   bool
	}{
		{
			name:       "output_item_added",
			data:       `{"type":"response.output_item.added","item":{"type":"reasoning"}}`,
			mode:       FirstTokenModeLoose,
			wantStrict: false,
			wantMode:   true,
		},
		{
			name:       "content_part_added",
			data:       `{"type":"response.content_part.added","part":{"type":"output_text","text":""}}`,
			mode:       FirstTokenModeLoose,
			wantStrict: false,
			wantMode:   true,
		},
		{
			name:       "created",
			data:       `{"type":"response.created"}`,
			mode:       FirstTokenModeLoose,
			wantStrict: false,
			wantMode:   false,
		},
		{
			name:       "in_progress",
			data:       `{"type":"response.in_progress"}`,
			mode:       FirstTokenModeLoose,
			wantStrict: false,
			wantMode:   false,
		},
		{
			name:       "completed",
			data:       `{"type":"response.completed"}`,
			mode:       FirstTokenModeLoose,
			wantStrict: false,
			wantMode:   false,
		},
		{
			name:       "failed",
			data:       `{"type":"response.failed"}`,
			mode:       FirstTokenModeLoose,
			wantStrict: false,
			wantMode:   false,
		},
		{
			name:       "text_delta",
			data:       `{"type":"response.output_text.delta","delta":"hello"}`,
			mode:       FirstTokenModeLoose,
			wantStrict: true,
			wantMode:   true,
		},
		{
			name:       "invalid_mode_uses_strict",
			data:       `{"type":"response.output_item.added","item":{"type":"reasoning"}}`,
			mode:       "invalid",
			wantStrict: false,
			wantMode:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := gjson.Parse(tc.data)
			if got := isFirstTokenResultForMode(parsed, FirstTokenModeStrict); got != tc.wantStrict {
				t.Fatalf("strict mode got %v, want %v", got, tc.wantStrict)
			}
			if got := isFirstTokenResultForMode(parsed, tc.mode); got != tc.wantMode {
				t.Fatalf("mode %q got %v, want %v", tc.mode, got, tc.wantMode)
			}
		})
	}
}
