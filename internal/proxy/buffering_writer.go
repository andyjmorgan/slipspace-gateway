package proxy

import (
	"net/http"
)

// BufferingResponseWriter wraps an http.ResponseWriter with one-time
// commit-at-status-line semantics for the v1.2 resilience orchestrator.
// It intercepts only WriteHeader; body bytes are never buffered. The
// decision to commit (pass through to the real ResponseWriter) or
// discard (signal the orchestrator that the attempt should be retried)
// is made the moment the upstream emits an HTTP status line.
//
// Headers are STAGED to an isolated map pre-commit so a discarded
// attempt's response headers never reach the underlying writer. On
// commit the staged map is copied into the underlying writer's
// Header() and WriteHeader is called once. This isolation is what
// lets the orchestrator wrap the SAME outer ResponseWriter across
// multiple failover attempts without leaking the failed attempt's
// Content-Length / Content-Type to the next one.
//
// State transitions:
//
//   - Initial: no WriteHeader observed. The orchestrator gets nothing
//     until status arrives or the upstream signals a transport error
//     via SetTransportError (typically from ReverseProxy's
//     ErrorHandler).
//   - WriteHeader(status), status in retry set: response is *discarded*.
//     The status code is recorded for the orchestrator; the real
//     ResponseWriter sees nothing. Subsequent Write/Flush calls are
//     silently absorbed so the upstream's body can be drained from the
//     connection. Committed remains false.
//   - WriteHeader(status), status not in retry set: the status flushes
//     through to the real ResponseWriter and Committed flips true. All
//     subsequent Write/Flush calls pass through verbatim. From this
//     point on the orchestrator's window to retry has closed.
//
// The "commit at status line, ride a 200 to whatever end" rule is
// load-bearing — it means we never retry past the status line, which
// removes the double-billing / partial-stream concerns the v1.2
// design call-outs warn about.
//
// Implements http.Flusher so ReverseProxy's streaming flush (with
// FlushInterval=-1) reaches the underlying writer once committed.
type BufferingResponseWriter struct {
	inner http.ResponseWriter

	retry map[int]struct{}

	// staging holds headers written via Header() pre-commit. On
	// commit it is copied into inner.Header(); on discard it is
	// dropped so the next attempt starts with a clean header map.
	staging http.Header

	statusCode int

	committed bool

	writeHeaderCalled bool

	transportErr error
}

// NewBufferingResponseWriter wraps w with retry-on-status semantics.
// retryStatusCodes is the policy's failure_status_codes set — any
// status in the set causes a discard; any other status commits.
// Duplicate codes are deduplicated; nil or empty retryStatusCodes
// means "commit every status" (the orchestrator never retries based
// on status — only on transport error).
func NewBufferingResponseWriter(w http.ResponseWriter, retryStatusCodes []int) *BufferingResponseWriter {
	set := make(map[int]struct{}, len(retryStatusCodes))
	for _, s := range retryStatusCodes {
		set[s] = struct{}{}
	}
	return &BufferingResponseWriter{inner: w, retry: set, staging: make(http.Header)}
}

// Header returns the staging header map pre-commit, the underlying
// writer's header map post-commit. Headers set during a discarded
// attempt are dropped with the writer — they never reach the client.
// Once committed, every subsequent Header() call routes to the
// underlying writer so streaming responses can keep mutating headers
// the way the standard http.ResponseWriter contract allows.
func (b *BufferingResponseWriter) Header() http.Header {
	if b.committed {
		return b.inner.Header()
	}
	return b.staging
}

// WriteHeader is the commit-or-discard decision point. The first
// call observes the status code: in-retry-set discards, otherwise
// commits. Subsequent WriteHeader calls are no-ops to match
// net/http's "only the first WriteHeader counts" contract.
func (b *BufferingResponseWriter) WriteHeader(status int) {
	if b.writeHeaderCalled {
		return
	}
	b.writeHeaderCalled = true
	b.statusCode = status
	if _, ok := b.retry[status]; ok {
		return
	}
	b.committed = true
	// Flush staged headers into the underlying writer in one shot
	// so the commit looks atomic to net/http's serialization layer.
	innerHdr := b.inner.Header()
	for k, vs := range b.staging {
		cp := make([]string, len(vs))
		copy(cp, vs)
		innerHdr[k] = cp
	}
	b.inner.WriteHeader(status)
}

// Write either passes through (when committed) or silently absorbs
// the bytes (when discarding). The byte count is reported back as
// the full length so ReverseProxy's copy loop doesn't see a short
// write and tear the connection down prematurely — the upstream
// body is consumed but never reaches the client.
//
// If Write is called before WriteHeader (legal per the
// http.ResponseWriter contract), the writer defaults to 200 OK and
// commits — matching net/http.ResponseWriter's behaviour.
func (b *BufferingResponseWriter) Write(p []byte) (int, error) {
	if !b.writeHeaderCalled {
		b.WriteHeader(http.StatusOK)
	}
	if !b.committed {
		return len(p), nil
	}
	return b.inner.Write(p)
}

// Flush forwards to the underlying writer's Flusher implementation
// once committed. Before commit, Flush is a silent no-op — the body
// is being discarded, so there is nothing to flush.
func (b *BufferingResponseWriter) Flush() {
	if !b.committed {
		return
	}
	if f, ok := b.inner.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying ResponseWriter so callers that
// need access to the raw writer (typically for type assertions
// against extension interfaces) can reach it.
func (b *BufferingResponseWriter) Unwrap() http.ResponseWriter {
	return b.inner
}

// StatusCode returns the upstream status code observed via
// WriteHeader. Zero when WriteHeader has not been called (the
// upstream produced no response — connection failure, hang, or
// not-yet-arrived).
func (b *BufferingResponseWriter) StatusCode() int {
	return b.statusCode
}

// Committed reports whether the response was passed through to the
// client. Once true, the orchestrator's retry window has closed —
// any further attempt would risk delivering two responses to the
// client.
func (b *BufferingResponseWriter) Committed() bool {
	return b.committed
}

// ShouldRetry reports whether the orchestrator should treat this
// attempt as retryable. True when either:
//
//   - WriteHeader was called with a status in the retry set
//     (Committed remained false), or
//   - A transport error was recorded via SetTransportError (no
//     WriteHeader observed at all).
//
// False once Committed flips true — the response is on its way to
// the client and the window to retry has closed.
func (b *BufferingResponseWriter) ShouldRetry() bool {
	if b.committed {
		return false
	}
	if b.transportErr != nil {
		return true
	}
	if !b.writeHeaderCalled {
		return false
	}
	_, ok := b.retry[b.statusCode]
	return ok
}

// SetTransportError records a transport-level failure — typically
// invoked from ReverseProxy.ErrorHandler when the upstream call
// fails before any response is received. Stored on the writer so
// the orchestrator can distinguish "upstream said 503" from "we
// never got a response" when deciding the next move.
func (b *BufferingResponseWriter) SetTransportError(err error) {
	b.transportErr = err
}

// TransportError returns the captured transport-level error, if
// any. Nil when no error was recorded — including the normal case
// where the upstream produced a response and the writer either
// committed or discarded based on status.
func (b *BufferingResponseWriter) TransportError() error {
	return b.transportErr
}
