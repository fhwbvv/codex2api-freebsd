package proxy

import (
	"bytes"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/codex2api/security/promptfilter"
	"github.com/gin-gonic/gin"
)

const pendingFirstTokenFlushBytes = 1024 * 1024

var (
	sseDataPrefix = []byte("data: ")
	sseDataSuffix = []byte("\n\n")
)

type streamFlushWriter struct {
	writer        io.Writer
	flusher       http.Flusher
	policy        string
	interval      time.Duration
	lastFlush     time.Time
	buffer        bytes.Buffer
	outputScanner *promptfilter.OutputScanner
	writtenBytes  atomic.Int64
}

func (h *Handler) newStreamFlushWriter(c *gin.Context, writer io.Writer, flusher http.Flusher) *streamFlushWriter {
	w := newStreamFlushWriter(writer, flusher)
	if h != nil && h.store != nil {
		cfg := h.promptFilterConfigForRequest(c)
		if cfg.Enabled && cfg.Advanced.Output.Enabled {
			w.outputScanner = promptfilter.NewOutputScannerFromNormalizedConfig(cfg)
		}
	}
	return w
}

func (w *streamFlushWriter) scanOutput(data []byte) ([]byte, error) {
	if w == nil || w.outputScanner == nil {
		return data, nil
	}
	return w.outputScanner.Push(data)
}

func newStreamFlushWriter(writer io.Writer, flusher http.Flusher) *streamFlushWriter {
	settings := CurrentRuntimeSettings()
	return &streamFlushWriter{
		writer:   writer,
		flusher:  flusher,
		policy:   settings.StreamFlushPolicy,
		interval: currentStreamFlushInterval(),
	}
}

func appendSSEData(buf *bytes.Buffer, data []byte) {
	if buf == nil {
		return
	}
	buf.Write(sseDataPrefix)
	buf.Write(data)
	buf.Write(sseDataSuffix)
}

func writeDeferredSSEData(streamWriter *streamFlushWriter, pending *bytes.Buffer, data []byte, shouldDefer bool) (bool, error) {
	if streamWriter == nil {
		return false, nil
	}
	if shouldDefer {
		appendSSEData(pending, data)
		if pending != nil && pending.Len() <= pendingFirstTokenFlushBytes {
			return false, nil
		}
	}
	if pending != nil && pending.Len() > 0 {
		if !shouldDefer {
			appendSSEData(pending, data)
		}
		before := streamWriter.deliveredBytes()
		if err := streamWriter.WriteBytes(pending.Bytes()); err != nil {
			return false, err
		}
		pending.Reset()
		return streamWriter.deliveredBytes() > before, nil
	}
	if shouldDefer {
		return false, nil
	}
	before := streamWriter.deliveredBytes()
	if err := streamWriter.WriteSSEData(data); err != nil {
		return false, err
	}
	return streamWriter.deliveredBytes() > before, nil
}

func (w *streamFlushWriter) deliveredBytes() int64 {
	if w == nil {
		return 0
	}
	return w.writtenBytes.Load()
}

func (w *streamFlushWriter) writeUnderlying(data []byte) error {
	if w == nil || w.writer == nil || len(data) == 0 {
		return nil
	}
	written, err := w.writer.Write(data)
	if written > 0 {
		w.writtenBytes.Add(int64(written))
	}
	return err
}

func (w *streamFlushWriter) writeUnderlyingString(data string) error {
	if w == nil || w.writer == nil || data == "" {
		return nil
	}
	written, err := io.WriteString(w.writer, data)
	if written > 0 {
		w.writtenBytes.Add(int64(written))
	}
	return err
}

func (w *streamFlushWriter) WriteString(data string) error {
	if w == nil || w.writer == nil {
		return nil
	}
	filtered, err := w.scanOutput([]byte(data))
	if err != nil || len(filtered) == 0 {
		return err
	}
	data = string(filtered)
	if w.policy != StreamFlushPolicyCoalesce {
		if err := w.writeUnderlyingString(data); err != nil {
			return err
		}
		w.flushTransport()
		return nil
	}
	if _, err := w.buffer.WriteString(data); err != nil {
		return err
	}
	if w.lastFlush.IsZero() || time.Since(w.lastFlush) >= w.interval {
		return w.Flush()
	}
	return nil
}

func (w *streamFlushWriter) WriteBytes(data []byte) error {
	if w == nil || w.writer == nil || len(data) == 0 {
		return nil
	}
	var err error
	data, err = w.scanOutput(data)
	if err != nil || len(data) == 0 {
		return err
	}
	if w.policy != StreamFlushPolicyCoalesce {
		if err := w.writeUnderlying(data); err != nil {
			return err
		}
		w.flushTransport()
		return nil
	}
	if _, err := w.buffer.Write(data); err != nil {
		return err
	}
	if w.lastFlush.IsZero() || time.Since(w.lastFlush) >= w.interval {
		return w.Flush()
	}
	return nil
}

func (w *streamFlushWriter) WriteSSEData(data []byte) error {
	if w == nil || w.writer == nil {
		return nil
	}
	framed := make([]byte, 0, len(sseDataPrefix)+len(data)+len(sseDataSuffix))
	framed = append(framed, sseDataPrefix...)
	framed = append(framed, data...)
	framed = append(framed, sseDataSuffix...)
	var err error
	framed, err = w.scanOutput(framed)
	if err != nil || len(framed) == 0 {
		return err
	}
	if w.policy != StreamFlushPolicyCoalesce {
		if err := w.writeUnderlying(framed); err != nil {
			return err
		}
		w.flushTransport()
		return nil
	}
	w.buffer.Write(framed)
	if w.lastFlush.IsZero() || time.Since(w.lastFlush) >= w.interval {
		return w.Flush()
	}
	return nil
}

func (w *streamFlushWriter) Flush() error {
	if w == nil {
		return nil
	}
	if w.buffer.Len() > 0 {
		if err := w.writeUnderlying(w.buffer.Bytes()); err != nil {
			return err
		}
		w.buffer.Reset()
	}
	if w.outputScanner != nil {
		pending, err := w.outputScanner.Flush()
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			if err := w.writeUnderlying(pending); err != nil {
				return err
			}
		}
	}
	w.flushTransport()
	return nil
}

// Finalize releases the retained safety window at a real semantic end-of-stream.
// A transport Flush must not call this because an unsafe phrase may span chunks.
func (w *streamFlushWriter) Finalize() error {
	if w == nil {
		return nil
	}
	if w.buffer.Len() > 0 {
		if err := w.writeUnderlying(w.buffer.Bytes()); err != nil {
			return err
		}
		w.buffer.Reset()
	}
	if w.outputScanner != nil {
		pending, err := w.outputScanner.Finalize()
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			if err := w.writeUnderlying(pending); err != nil {
				return err
			}
		}
	}
	w.flushTransport()
	return nil
}

func (w *streamFlushWriter) flushTransport() {
	if w == nil || w.flusher == nil {
		return
	}
	w.flusher.Flush()
	w.lastFlush = time.Now()
}
