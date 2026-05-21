package livefeed

import (
	"context"
	"net/http"
	"sync"

	"github.com/andyjmorgan/sluice-gateway/internal/headers"
)

// ResponseBuffer is a per-request, bounded byte sink that captures the
// outbound response bytes for the live-feed body store. Goroutine-safe
// because httputil.ReverseProxy can interleave streaming writes; the
// proxy itself is single-writer per request but capacity checks and
// the truncated flag are read by the reporter after the response
// finishes, so the mutex keeps the read consistent.
//
// Append silently drops bytes once the byte cap is reached and sets
// the truncated flag. Total tracks the bytes the writer tried to
// append so the operator can see how much was dropped.
type ResponseBuffer struct {
	maxBytes int

	mu        sync.Mutex
	buf       []byte
	total     int64
	truncated bool
	headers   map[string][]string
}

// NewResponseBuffer constructs a buffer with the given per-body byte
// cap. A non-positive cap returns nil — callers should gate buffer
// creation on env.LiveFeedBodiesEnabled().
func NewResponseBuffer(maxBytes int) *ResponseBuffer {
	if maxBytes <= 0 {
		return nil
	}
	return &ResponseBuffer{maxBytes: maxBytes}
}

// Append copies p into the buffer up to the per-body cap, returning
// the number of bytes accepted. Bytes beyond the cap are dropped and
// the truncated flag is set; subsequent appends still count toward
// Total so operators see the full size.
func (b *ResponseBuffer) Append(p []byte) {
	if b == nil || len(p) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(len(p))
	remaining := b.maxBytes - len(b.buf)
	if remaining <= 0 {
		b.truncated = true
		return
	}
	if len(p) > remaining {
		b.buf = append(b.buf, p[:remaining]...)
		b.truncated = true
		return
	}
	b.buf = append(b.buf, p...)
}

// Bytes returns a copy of the captured bytes. Callers may mutate the
// returned slice without affecting the buffer.
func (b *ResponseBuffer) Bytes() []byte {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]byte, len(b.buf))
	copy(out, b.buf)
	return out
}

// Total returns the total bytes the writer appended (including dropped
// overflow). Useful for surfacing "captured N of M" in the UI.
func (b *ResponseBuffer) Total() int64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}

// Truncated reports whether any bytes were dropped due to the cap.
func (b *ResponseBuffer) Truncated() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

// Headers returns the snapshot of the outbound response headers taken
// at WriteHeader time, with credential-bearing values already redacted.
// Returns nil when the response wrote no headers (transport failure).
func (b *ResponseBuffer) Headers() map[string][]string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.headers == nil {
		return nil
	}
	out := make(map[string][]string, len(b.headers))
	for k, vs := range b.headers {
		cloned := make([]string, len(vs))
		copy(cloned, vs)
		out[k] = cloned
	}
	return out
}

// setHeaders records the snapshot taken inside teeWriter.WriteHeader.
// The map is stored as-is — callers must hand over a deep copy.
func (b *ResponseBuffer) setHeaders(h map[string][]string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.headers = h
}

// teeWriter is an http.ResponseWriter wrapper that copies every Write
// into a ResponseBuffer in addition to the underlying writer.
//
// Implements http.Flusher so SSE streaming reaches the client; the
// pre-existing statusWriter in internal/proxy also implements Flusher
// and wraps this one in turn — the chain forwards Flush all the way
// down.
type teeWriter struct {
	http.ResponseWriter
	buf          *ResponseBuffer
	headersTaken bool
}

func (t *teeWriter) Write(p []byte) (int, error) {
	t.snapshotHeadersOnce()
	n, err := t.ResponseWriter.Write(p)
	if n > 0 {
		t.buf.Append(p[:n])
	}
	return n, err
}

// WriteHeader snapshots the outbound header map (with credentials
// redacted) just before they get serialised on the wire, so the
// live-feed body envelope can carry them. The proxy / ResponseWriter
// will deduplicate the underlying call — WriteHeader is idempotent in
// httputil.ReverseProxy's chain.
func (t *teeWriter) WriteHeader(status int) {
	t.snapshotHeadersOnce()
	t.ResponseWriter.WriteHeader(status)
}

// snapshotHeadersOnce captures the header map exactly once per response.
// We can't rely on WriteHeader being called explicitly (the stdlib's
// implicit 200 happens via Write), so Write also calls this.
func (t *teeWriter) snapshotHeadersOnce() {
	if t.headersTaken {
		return
	}
	t.headersTaken = true
	t.buf.setHeaders(headers.RedactSensitive(t.Header()))
}

func (t *teeWriter) Flush() {
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (t *teeWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// WrapResponseWriter wraps w so every Write tees into buf. buf may be
// nil — in which case the original writer is returned unchanged. This
// lets callers omit a feature-flag check at every wrap site.
func WrapResponseWriter(w http.ResponseWriter, buf *ResponseBuffer) http.ResponseWriter {
	if buf == nil {
		return w
	}
	return &teeWriter{ResponseWriter: w, buf: buf}
}

// responseBufKey is the unexported context-key type for ResponseBuffer
// stash/fetch. Following the stdlib idiom keeps key collisions with
// other packages impossible.
type responseBufKey struct{}

// WithResponseBuffer stashes buf on ctx so downstream handlers (the
// forwarder, then the reporter at OnComplete) can pull it back via
// ResponseBufferFromContext. A nil buf is a no-op — fetching from a
// ctx that never stashed one returns (nil, false).
func WithResponseBuffer(ctx context.Context, buf *ResponseBuffer) context.Context {
	if buf == nil {
		return ctx
	}
	return context.WithValue(ctx, responseBufKey{}, buf)
}

// ResponseBufferFromContext retrieves the buffer stashed by
// WithResponseBuffer. Returns (nil, false) when no buffer is on ctx.
func ResponseBufferFromContext(ctx context.Context) (*ResponseBuffer, bool) {
	v := ctx.Value(responseBufKey{})
	if v == nil {
		return nil, false
	}
	buf, ok := v.(*ResponseBuffer)
	return buf, ok
}
