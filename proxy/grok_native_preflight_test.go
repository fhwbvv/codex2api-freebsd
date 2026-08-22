package proxy

import (
	"testing"

	"github.com/tidwall/gjson"
)

// native 直通的是传输形态，不是请求内容：Codex 专有工具形态原样发给 Grok 会
// 400。以下用例锁住"native 的 Responses 路由仍然跑 preflight"。
func TestPrepareRoutedGrokNativeResponsesStillRunsPreflight(t *testing.T) {
	route := GrokUpstreamRoute{Model: "mapped-grok", Protocol: GrokProtocolResponses, Native: true}
	inbound := []byte(`{
		"model":"client-alias",
		"stream":false,
		"client_metadata":{"a":"b"},
		"tools":[{"type":"custom","name":"exec_command","description":"run"}],
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]
	}`)

	preflight, err := prepareRoutedGrokProtocolRequest(route, GrokProtocolResponses, inbound, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := preflight.Body

	// Codex 专有工具已降级成 Grok 可接受的 function 形态。
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		if tool.Get("type").String() == "custom" {
			t.Fatalf("custom tool leaked to a native route: %s", body)
		}
	}
	// 别名必须回传，否则响应侧无法把工具名还原成 custom_tool_call。
	if len(preflight.Aliases) == 0 {
		t.Fatalf("native route must carry back namespace aliases: %+v", preflight)
	}
	// Grok 不接受的 Codex 顶层注入已剥离。
	if gjson.GetBytes(body, "client_metadata").Exists() {
		t.Fatalf("dropped top-level field survived: %s", body)
	}
	// native 的核心语义仍在：不强制 stream=true。
	if stream := gjson.GetBytes(body, "stream"); !stream.Exists() || stream.Bool() {
		t.Fatalf("native passthrough must preserve explicit stream=false: %s", body)
	}
	// 账号映射后的模型仍然生效。
	if got := gjson.GetBytes(body, "model").String(); got != "mapped-grok" {
		t.Fatalf("model = %q, want mapped-grok", got)
	}
}

// 非 Responses 的 native 路由（Chat / Messages）没有 Responses 语义可降级，
// 保持纯直通不变。
func TestPrepareRoutedGrokNativeChatStaysPassthrough(t *testing.T) {
	route := GrokUpstreamRoute{Model: "mapped-grok", Protocol: GrokProtocolChatCompletions, Native: true}
	inbound := []byte(`{"model":"client-alias","messages":[{"role":"user","content":"hi"}],"stream":false,"top_k":3}`)

	preflight, err := prepareRoutedGrokProtocolRequest(route, GrokProtocolChatCompletions, inbound, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !gjson.GetBytes(preflight.Body, "top_k").Exists() {
		t.Fatalf("chat native passthrough must preserve provider extensions: %s", preflight.Body)
	}
	if stream := gjson.GetBytes(preflight.Body, "stream"); !stream.Exists() || stream.Bool() {
		t.Fatalf("native passthrough must preserve explicit stream=false: %s", preflight.Body)
	}
}
