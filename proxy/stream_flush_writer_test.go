package proxy

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestWriteSSECommentDrainsBufferFirstAndBypassesScanner(t *testing.T) {
	var out bytes.Buffer
	w := &streamFlushWriter{writer: &out, policy: StreamFlushPolicyCoalesce, interval: time.Hour}
	// lastFlush 零值会让首次写立即冲刷;置为当前时刻让合并缓冲真正囤住事件。
	w.lastFlush = time.Now()

	// 合并缓冲里囤着一个完整事件(interval 极长,不会自行冲刷)。
	if err := w.WriteSSEData([]byte(`{"type":"response.output_text.delta"}`)); err != nil {
		t.Fatalf("WriteSSEData: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("coalesce 策略下事件不应立即落底层: %q", out.String())
	}

	if err := w.WriteSSEComment(continueKeepaliveComment); err != nil {
		t.Fatalf("WriteSSEComment: %v", err)
	}
	got := out.String()
	idxEvent := strings.Index(got, `data: {"type":"response.output_text.delta"}`)
	idxComment := strings.Index(got, ": keepalive\n\n")
	if idxEvent < 0 || idxComment < 0 {
		t.Fatalf("输出缺事件或注释: %q", got)
	}
	if idxComment < idxEvent {
		t.Fatalf("注释先于已缓冲事件落地,事件顺序被打乱: %q", got)
	}
}

func TestWriteSSECommentNilAndEmptySafe(t *testing.T) {
	var w *streamFlushWriter
	if err := w.WriteSSEComment(": keepalive\n\n"); err != nil {
		t.Fatalf("nil writer: %v", err)
	}
	var out bytes.Buffer
	w2 := &streamFlushWriter{writer: &out}
	if err := w2.WriteSSEComment(""); err != nil {
		t.Fatalf("empty comment: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("空注释不应写任何字节: %q", out.String())
	}
}
