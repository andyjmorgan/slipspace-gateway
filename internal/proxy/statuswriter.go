package proxy

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/andyjmorgan/slipspace-gateway/internal/headers"
)

// statusWriter is a minimal http.ResponseWriter wrapper that captures the
// final status code so the Forwarder can pass it to Observer.OnComplete.
// It deliberately implements http.Flusher so httputil.ReverseProxy's
// streaming flush calls (driven by FlushInterval: -1) reach the underlying
// writer. The pipeline-aware capture writer is a follow-up wave.
type statusWriter struct {
	http.ResponseWriter

	// ctx + logger are present so WriteHeader can log the final response
	// headers at debug level — the value the client actually sees after
	// httputil.ReverseProxy's hop-by-hop strip and any prior middleware
	// additions (correlation_id, session_id).
	ctx      context.Context
	logger   *slog.Logger
	redactor *headers.Redactor

	status      int
	wroteHeader bool

	// streaming records whether the upstream response was SSE, populated by
	// the Forwarder's ModifyResponse hook so the success log can report it
	// without re-inspecting headers.
	streaming bool

	// onChunk, when set, fires on every streaming Flush — one call per
	// response chunk delivered to the client (FlushInterval=-1). The
	// Forwarder wires it to Observer.OnResponseChunk; nil disables it.
	onChunk func()
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.status = code
	w.wroteHeader = true

	if w.logger != nil && w.logger.Enabled(w.ctx, slog.LevelDebug) {
		w.logger.DebugContext(w.ctx, "proxy: final response headers",
			slog.Int("status_code", code),
			slog.Any("headers", w.redactor.Redact(w.Header())),
		)
	}

	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.status = http.StatusOK
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	// A flush on a streaming response is a chunk boundary. Fire the chunk
	// hook before the downstream flush so the timestamp reflects when the
	// chunk was ready, not after the (possibly blocking) socket write.
	if w.streaming && w.onChunk != nil {
		w.onChunk()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Status returns the captured HTTP status code. Reads default to 200 until
// WriteHeader or Write has been observed.
func (w *statusWriter) Status() int {
	return w.status
}

// Committed reports whether WriteHeader (or implicit-WriteHeader via
// Write) was observed. Independent of Status because the default 200
// is returned even when nothing happened — Committed disambiguates
// "upstream sent 200" from "upstream sent nothing, default fell
// through."
func (w *statusWriter) Committed() bool {
	return w.wroteHeader
}

// Streaming reports whether the upstream response was flagged as SSE.
func (w *statusWriter) Streaming() bool {
	return w.streaming
}
