package proxy

import (
	"bytes"
	"io"
	"net/http"
	"time"
)

type streamFlushWriter struct {
	writer    io.Writer
	flusher   http.Flusher
	policy    string
	interval  time.Duration
	lastFlush time.Time
	buffer    bytes.Buffer
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

func (w *streamFlushWriter) WriteString(data string) error {
	if w == nil || w.writer == nil {
		return nil
	}
	if w.policy != StreamFlushPolicyCoalesce {
		if _, err := io.WriteString(w.writer, data); err != nil {
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
	if w == nil || len(data) == 0 {
		return nil
	}
	return w.WriteString(string(data))
}

func (w *streamFlushWriter) Flush() error {
	if w == nil {
		return nil
	}
	if w.buffer.Len() > 0 {
		if _, err := w.writer.Write(w.buffer.Bytes()); err != nil {
			return err
		}
		w.buffer.Reset()
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
