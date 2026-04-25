package admin

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConnectionTestModelValidation(t *testing.T) {
	if !isSupportedConnectionTestModel("gpt-5.5") {
		t.Fatal("gpt-5.5 should be allowed for connection tests")
	}
	if isSupportedConnectionTestModel("gpt-image-2") {
		t.Fatal("image models should not be allowed for connection tests")
	}
	if isSupportedConnectionTestModel("unknown-model") {
		t.Fatal("unknown models should not be allowed for connection tests")
	}
}

func TestBuildTestPayloadUsesSelectedModel(t *testing.T) {
	payload := buildTestPayload("gpt-5.5")
	if got := gjson.GetBytes(payload, "model").String(); got != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5", got)
	}
	if !gjson.GetBytes(payload, "stream").Bool() {
		t.Fatal("stream should be true")
	}
}

func TestExtractCompletedOutputText(t *testing.T) {
	event := []byte(`{
		"type":"response.completed",
		"response":{
			"status":"completed",
			"output":[
				{"type":"message","content":[{"type":"output_text","text":"hello from completed"}]}
			]
		}
	}`)

	if got := extractCompletedOutputText(event); got != "hello from completed" {
		t.Fatalf("output text = %q, want completed text", got)
	}
}

func TestFormatUpstreamTestErrorIncludesMessageAndEvent(t *testing.T) {
	event := []byte(`{
		"type":"response.failed",
		"response":{
			"error":{"message":"model unavailable","code":"model_not_available"}
		}
	}`)

	got := formatUpstreamTestError(event, "fallback")
	for _, want := range []string{"model unavailable", "model_not_available", "上游事件", "response.failed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted error %q does not contain %q", got, want)
		}
	}
}

func TestFormatNoOutputUpstreamErrorIncludesCompletedEvent(t *testing.T) {
	event := []byte(`{"type":"response.completed","response":{"status":"completed","output":[]}}`)

	got := formatNoOutputUpstreamError(event)
	for _, want := range []string{"没有返回文本输出", "上游事件", `"output": []`} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted no-output error %q does not contain %q", got, want)
		}
	}
}
