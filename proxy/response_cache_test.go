package proxy

import (
	"testing"

	"github.com/tidwall/gjson"
)

func resetResponseCacheForTest() {
	respCache.mu.Lock()
	respCache.store = make(map[string]*responseCacheEntry)
	respCache.mu.Unlock()
}

func TestCacheCompletedResponseCachesCodexNativeToolCalls(t *testing.T) {
	resetResponseCacheForTest()

	expandedInput := []byte(`[{"type":"message","role":"user","content":"find a tool"}]`)
	completed := []byte(`{"type":"response.completed","response":{"id":"resp_native","output":[{"type":"tool_search_call","call_id":"call_search","status":"completed"}]}}`)

	cacheCompletedResponse(expandedInput, completed)

	cached := getResponseCache("resp_native")
	if len(cached) != 2 {
		t.Fatalf("cached items = %d, want 2", len(cached))
	}
	if got := gjson.GetBytes(cached[1], "call_id").String(); got != "call_search" {
		t.Fatalf("cached call_id = %q, want call_search", got)
	}
}

func TestExpandPreviousResponseUsesCachedCodexNativeToolContext(t *testing.T) {
	resetResponseCacheForTest()

	cacheCompletedResponse(
		[]byte(`[{"type":"message","role":"user","content":"run mcp tool"}]`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_mcp","output":[{"type":"mcp_tool_call","call_id":"call_mcp","name":"read","arguments":"{}"}]}}`),
	)

	body := []byte(`{"model":"gpt-5.4","previous_response_id":"resp_mcp","input":[{"type":"mcp_tool_call_output","call_id":"call_mcp","output":"ok"}]}`)
	got, prevID := expandPreviousResponse(body)

	if prevID != "resp_mcp" {
		t.Fatalf("prevID = %q, want resp_mcp", prevID)
	}
	input := gjson.GetBytes(got, "input").Array()
	if len(input) != 3 {
		t.Fatalf("expanded input count = %d, want 3; body=%s", len(input), got)
	}
	if typ := input[1].Get("type").String(); typ != "mcp_tool_call" {
		t.Fatalf("cached tool call type = %q, want mcp_tool_call", typ)
	}
	if callID := input[2].Get("call_id").String(); callID != "call_mcp" {
		t.Fatalf("current output call_id = %q, want call_mcp", callID)
	}
}

func TestCacheCompletedResponseDoesNotCacheNonCallIDToolCalls(t *testing.T) {
	resetResponseCacheForTest()

	for _, test := range []struct {
		name string
		body []byte
	}{
		{
			name: "image_generation_call",
			body: []byte(`{"type":"response.completed","response":{"id":"resp_image","output":[{"type":"image_generation_call","id":"ig_1","status":"completed"}]}}`),
		},
		{
			name: "web_search_call",
			body: []byte(`{"type":"response.completed","response":{"id":"resp_web","output":[{"type":"web_search_call","call_id":"call_bad","status":"completed"}]}}`),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetResponseCacheForTest()

			cacheCompletedResponse([]byte(`[{"type":"message","role":"user","content":"hello"}]`), test.body)

			respID := gjson.GetBytes(test.body, "response.id").String()
			if cached := getResponseCache(respID); cached != nil {
				t.Fatalf("expected no cache for %s, got %d items", test.name, len(cached))
			}
		})
	}
}
