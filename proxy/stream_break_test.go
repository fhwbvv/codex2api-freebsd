package proxy

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// issue #473：首包后断流必须给下游一个可编程识别的失败终态，
// 而不是静默 EOF 的"假 200"。

func TestWriteResponsesStreamBreakEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	writer := newStreamFlushWriter(c.Writer, nil)
	if err := writeResponsesStreamBreakEvent(writer); err != nil {
		t.Fatalf("writeResponsesStreamBreakEvent: %v", err)
	}

	body := recorder.Body.String()
	if !strings.HasPrefix(body, "data: ") || !strings.HasSuffix(body, "\n\n") {
		t.Fatalf("unexpected SSE frame: %q", body)
	}
	data := strings.TrimSuffix(strings.TrimPrefix(body, "data: "), "\n\n")
	if !gjson.Valid(data) {
		t.Fatalf("stream break event is not valid JSON: %q", data)
	}
	if got := gjson.Get(data, "type").String(); got != "response.failed" {
		t.Fatalf("type = %q, want response.failed", got)
	}
	if got := gjson.Get(data, "response.status").String(); got != "failed" {
		t.Fatalf("response.status = %q, want failed", got)
	}
	if got := gjson.Get(data, "response.error.code").String(); got != ErrorCodeUpstreamStreamBreak {
		t.Fatalf("response.error.code = %q, want %q", got, ErrorCodeUpstreamStreamBreak)
	}
}

func TestWriteChatCompletionsStreamBreakEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	writer := newStreamFlushWriter(c.Writer, nil)
	if err := writeChatCompletionsStreamBreakEvent(writer); err != nil {
		t.Fatalf("writeChatCompletionsStreamBreakEvent: %v", err)
	}

	body := recorder.Body.String()
	if strings.Contains(body, "[DONE]") {
		t.Fatalf("stream break must not append [DONE]: %q", body)
	}
	data := strings.TrimSuffix(strings.TrimPrefix(body, "data: "), "\n\n")
	if !gjson.Valid(data) {
		t.Fatalf("stream break chunk is not valid JSON: %q", data)
	}
	if got := gjson.Get(data, "error.code").String(); got != ErrorCodeUpstreamStreamBreak {
		t.Fatalf("error.code = %q, want %q", got, ErrorCodeUpstreamStreamBreak)
	}
	if got := gjson.Get(data, "error.type").String(); got != ErrorTypeUpstreamError {
		t.Fatalf("error.type = %q, want %q", got, ErrorTypeUpstreamError)
	}
}

func TestShouldWriteStreamBreakEvent(t *testing.T) {
	errBoom := errors.New("boom")
	cases := []struct {
		name         string
		gotTerminal  bool
		wroteAnyBody bool
		ctxErr       error
		writeErr     error
		want         bool
	}{
		{"首包后断流", false, true, nil, nil, true},
		{"正常终态不触发", true, true, nil, nil, false},
		{"零写入交给透明重试/502 出口", false, false, nil, nil, false},
		{"客户端已断开不写", false, true, errBoom, nil, false},
		{"下游写失败不写", false, true, nil, errBoom, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldWriteStreamBreakEvent(tc.gotTerminal, tc.wroteAnyBody, tc.ctxErr, tc.writeErr); got != tc.want {
				t.Fatalf("shouldWriteStreamBreakEvent(%v,%v,%v,%v) = %v, want %v",
					tc.gotTerminal, tc.wroteAnyBody, tc.ctxErr, tc.writeErr, got, tc.want)
			}
		})
	}
}
