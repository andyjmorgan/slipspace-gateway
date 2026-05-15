package proxy

import (
	"context"
	"log/slog"
	"net/http"
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
	ctx    context.Context
	logger *slog.Logger

	status      int
	wroteHeader bool

	// streaming records whether the upstream response was SSE, populated by
	// the Forwarder's ModifyResponse hook so the success log can report it
	// without re-inspecting headers.
	streaming bool
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
			slog.Any("headers", redactSensitive(w.Header())),
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

// Streaming reports whether the upstream response was flagged as SSE.
func (w *statusWriter) Streaming() bool {
	return w.streaming
}
