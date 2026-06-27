package proxy

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestStreamFlushWriterWriteSSEData(t *testing.T) {
	var buf bytes.Buffer
	writer := &streamFlushWriter{writer: &buf}

	if err := writer.WriteSSEData([]byte(`{"type":"response.output_text.delta","delta":"hi"}`)); err != nil {
		t.Fatalf("WriteSSEData returned error: %v", err)
	}

	want := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("unexpected SSE payload:\n got %q\nwant %q", got, want)
	}
}

func TestStreamFlushWriterWriteBytesNoStringConversionRequired(t *testing.T) {
	var buf bytes.Buffer
	writer := &streamFlushWriter{writer: &buf}

	payload := []byte("data: [DONE]\n\n")
	if err := writer.WriteBytes(payload); err != nil {
		t.Fatalf("WriteBytes returned error: %v", err)
	}
	if got := buf.String(); got != string(payload) {
		t.Fatalf("unexpected payload: got %q want %q", got, string(payload))
	}
}

func TestStreamTranslatorTranslateParsedMatchesTranslate(t *testing.T) {
	events := [][]byte{
		[]byte(`{"type":"response.output_item.added","item":{"type":"function_call","id":"item_1","call_id":"call_1","name":"lookup"}}`),
		[]byte(`{"type":"response.function_call_arguments.delta","item_id":"item_1","delta":"{\"city\":\"Paris\"}"}`),
		[]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`),
	}

	fromRaw := NewStreamTranslator("chatcmpl-test", "gpt-test", 123)
	fromParsed := NewStreamTranslator("chatcmpl-test", "gpt-test", 123)

	for _, event := range events {
		rawChunk, rawDone := fromRaw.Translate(event)
		parsedChunk, parsedDone := fromParsed.TranslateParsed(gjson.ParseBytes(event))

		if rawDone != parsedDone {
			t.Fatalf("done mismatch for %s: raw=%v parsed=%v", event, rawDone, parsedDone)
		}
		if string(rawChunk) != string(parsedChunk) {
			t.Fatalf("chunk mismatch for %s:\n raw=%s\nparsed=%s", event, rawChunk, parsedChunk)
		}
	}
}

func TestUpstreamErrorConsoleBodyTruncatesLargePayload(t *testing.T) {
	body := []byte(strings.Repeat("x", consoleUpstreamErrorLogMaxBytes+128))
	got := upstreamErrorConsoleBody(body)
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	if len(got) > consoleUpstreamErrorLogMaxBytes+len(" ... [truncated]") {
		t.Fatalf("truncated console body too large: %d", len(got))
	}
}

func TestUpstreamErrorFileLogBodyTruncatesLargePayload(t *testing.T) {
	body := []byte(strings.Repeat("x", upstreamErrorLogBodyMaxBytes+128))
	got := upstreamErrorLogBody(body)
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	if len(got) > 5000+len(" ... [truncated]") {
		t.Fatalf("truncated file log body too large: %d", len(got))
	}
}

func TestWriteDeferredSSEDataFlushesPendingWithCurrentEvent(t *testing.T) {
	var buf bytes.Buffer
	writer := &streamFlushWriter{writer: &buf}
	var pending bytes.Buffer

	wrote, err := writeDeferredSSEData(writer, &pending, []byte(`{"type":"response.created"}`), true)
	if err != nil {
		t.Fatalf("defer lifecycle event returned error: %v", err)
	}
	if wrote {
		t.Fatal("deferred lifecycle event should not write before first token")
	}
	if buf.Len() != 0 {
		t.Fatalf("unexpected early output: %q", buf.String())
	}

	wrote, err = writeDeferredSSEData(writer, &pending, []byte(`{"type":"response.output_text.delta","delta":"hi"}`), false)
	if err != nil {
		t.Fatalf("first content event returned error: %v", err)
	}
	if !wrote {
		t.Fatal("first content event should flush pending data")
	}

	want := "data: {\"type\":\"response.created\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("unexpected deferred SSE output:\n got %q\nwant %q", got, want)
	}
	if pending.Len() != 0 {
		t.Fatalf("pending buffer not reset: %d", pending.Len())
	}
}

func TestWriteDeferredSSEDataFlushesLargeDeferredEventOnce(t *testing.T) {
	var buf bytes.Buffer
	writer := &streamFlushWriter{writer: &buf}
	var pending bytes.Buffer
	payload := []byte(strings.Repeat("x", pendingFirstTokenFlushBytes))

	wrote, err := writeDeferredSSEData(writer, &pending, payload, true)
	if err != nil {
		t.Fatalf("large deferred event returned error: %v", err)
	}
	if !wrote {
		t.Fatal("large deferred event should force a flush")
	}
	want := "data: " + string(payload) + "\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("unexpected large deferred output length/content: got len=%d want len=%d equal=%v", len(got), len(want), got == want)
	}
	if strings.Count(buf.String(), "data: ") != 1 {
		t.Fatalf("large deferred event should be written once, got %d data frames", strings.Count(buf.String(), "data: "))
	}
	if pending.Len() != 0 {
		t.Fatalf("pending buffer not reset: %d", pending.Len())
	}
}

func TestShouldSuppressRetryableResponseFailedBeforeFirstToken(t *testing.T) {
	retryableFailed := []byte(`{"type":"response.failed","response":{"error":{"message":"rate limited","status_code":429,"code":"rate_limit_exceeded"}}}`)
	nonRetryableFailed := []byte(`{"type":"response.failed","response":{"error":{"message":"bad request","status_code":400,"code":"invalid_request_error"}}}`)

	if !shouldSuppressRetryableResponseFailedBeforeFirstToken("response.failed", retryableFailed, false, false, 0, 1, nil, nil) {
		t.Fatal("retryable response.failed before first token should be suppressed while another attempt remains")
	}
	if shouldSuppressRetryableResponseFailedBeforeFirstToken("response.failed", retryableFailed, false, false, 1, 1, nil, nil) {
		t.Fatal("last attempt should not suppress response.failed")
	}
	if shouldSuppressRetryableResponseFailedBeforeFirstToken("response.failed", nonRetryableFailed, false, false, 0, 1, nil, nil) {
		t.Fatal("non-retryable response.failed should still be sent to the client")
	}
	if shouldSuppressRetryableResponseFailedBeforeFirstToken("response.failed", retryableFailed, true, false, 0, 1, nil, nil) {
		t.Fatal("response.failed after first token should not be suppressed")
	}
	if shouldSuppressRetryableResponseFailedBeforeFirstToken("response.failed", retryableFailed, false, true, 0, 1, nil, nil) {
		t.Fatal("response.failed after writing any body should not be suppressed")
	}
}

func BenchmarkStreamFlushWriterWriteSSEData(b *testing.B) {
	payload := []byte(`{"type":"response.output_text.delta","delta":"hello"}`)
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		writer := &streamFlushWriter{writer: &buf}
		if err := writer.WriteSSEData(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStreamTranslatorTranslateParsed(b *testing.B) {
	event := []byte(`{"type":"response.output_text.delta","delta":"hello"}`)
	parsed := gjson.ParseBytes(event)
	st := NewStreamTranslator("chatcmpl-bench", "gpt-test", 123)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if chunk, done := st.TranslateParsed(parsed); done || chunk == nil {
			b.Fatalf("unexpected translation result: done=%v chunk=%s", done, chunk)
		}
	}
}
