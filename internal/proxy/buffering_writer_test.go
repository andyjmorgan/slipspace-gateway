package proxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// flushRecorder wraps httptest.ResponseRecorder with a Flusher that
// counts invocations. Used to assert Flush passthrough semantics
// before vs after commit.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (f *flushRecorder) Flush() { f.flushes++ }

func TestBufferingResponseWriter_CommitsNonRetryStatus(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	b := NewBufferingResponseWriter(rec, []int{502, 503, 504})

	b.WriteHeader(http.StatusOK)

	if !b.Committed() {
		t.Errorf("Committed = false; want true after non-retry status")
	}
	if b.StatusCode() != http.StatusOK {
		t.Errorf("StatusCode = %d; want 200", b.StatusCode())
	}
	if rec.Code != http.StatusOK {
		t.Errorf("inner status = %d; want 200", rec.Code)
	}
	if b.ShouldRetry() {
		t.Error("ShouldRetry true on committed status")
	}
}

func TestBufferingResponseWriter_DiscardsRetryStatus(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	b := NewBufferingResponseWriter(rec, []int{503})

	b.WriteHeader(http.StatusServiceUnavailable)

	if b.Committed() {
		t.Errorf("Committed = true; want false for retry status")
	}
	if b.StatusCode() != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d; want 503", b.StatusCode())
	}
	if rec.Code == http.StatusServiceUnavailable {
		t.Errorf("inner ResponseWriter received the discard status")
	}
	if !b.ShouldRetry() {
		t.Error("ShouldRetry false on retry-set status")
	}
}

func TestBufferingResponseWriter_WriteAbsorbsAfterDiscard(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	b := NewBufferingResponseWriter(rec, []int{503})

	b.WriteHeader(http.StatusServiceUnavailable)
	n, err := b.Write([]byte("upstream-body-bytes"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len("upstream-body-bytes") {
		t.Errorf("Write reported short write: %d; want %d", n, len("upstream-body-bytes"))
	}
	if rec.Body.Len() != 0 {
		t.Errorf("inner body got %q; want empty (discarded)", rec.Body.String())
	}
}

func TestBufferingResponseWriter_WriteThroughAfterCommit(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	b := NewBufferingResponseWriter(rec, []int{503})

	b.WriteHeader(http.StatusOK)
	if _, err := b.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := rec.Body.String(); got != "hello" {
		t.Errorf("inner body = %q; want hello", got)
	}
}

func TestBufferingResponseWriter_WriteWithoutWriteHeaderCommitsAt200(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	b := NewBufferingResponseWriter(rec, []int{503})

	if _, err := b.Write([]byte("chunk")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !b.Committed() {
		t.Error("implicit 200 commit failed")
	}
	if b.StatusCode() != http.StatusOK {
		t.Errorf("StatusCode = %d; want 200", b.StatusCode())
	}
	if rec.Code != http.StatusOK {
		t.Errorf("inner status = %d; want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "chunk" {
		t.Errorf("inner body = %q", got)
	}
}

func TestBufferingResponseWriter_DoubleWriteHeaderIgnored(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	b := NewBufferingResponseWriter(rec, []int{503})

	b.WriteHeader(http.StatusOK)
	b.WriteHeader(http.StatusServiceUnavailable)

	if b.StatusCode() != http.StatusOK {
		t.Errorf("StatusCode = %d; second WriteHeader should be a no-op", b.StatusCode())
	}
	if !b.Committed() {
		t.Error("Committed flipped back to false on second WriteHeader")
	}
}

func TestBufferingResponseWriter_FlushOnlyAfterCommit(t *testing.T) {
	t.Parallel()
	rec := newFlushRecorder()
	b := NewBufferingResponseWriter(rec, []int{503})

	b.Flush() // before any WriteHeader — silent no-op
	if rec.flushes != 0 {
		t.Errorf("Flush count = %d; want 0 pre-commit", rec.flushes)
	}

	b.WriteHeader(http.StatusServiceUnavailable) // discarded
	b.Flush()
	if rec.flushes != 0 {
		t.Errorf("Flush count = %d; want 0 after discard", rec.flushes)
	}
}

func TestBufferingResponseWriter_FlushPassesThroughAfterCommit(t *testing.T) {
	t.Parallel()
	rec := newFlushRecorder()
	b := NewBufferingResponseWriter(rec, []int{503})

	b.WriteHeader(http.StatusOK)
	b.Flush()
	b.Flush()
	b.Flush()
	if rec.flushes != 3 {
		t.Errorf("Flush count = %d; want 3", rec.flushes)
	}
}

func TestBufferingResponseWriter_FlushNonFlusherInnerNoOp(t *testing.T) {
	t.Parallel()
	// httptest.ResponseRecorder is *not* an http.Flusher; assert that
	// calling Flush on a wrapping BufferingResponseWriter does not
	// panic when committed.
	rec := httptest.NewRecorder()
	b := NewBufferingResponseWriter(rec, nil)
	b.WriteHeader(http.StatusOK)
	b.Flush()
}

func TestBufferingResponseWriter_HeaderAccess(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	b := NewBufferingResponseWriter(rec, []int{503})

	b.Header().Set("X-Stage", "pre-commit")
	if got := b.Header().Get("X-Stage"); got != "pre-commit" {
		t.Errorf("Header round-trip failed: %q", got)
	}
	if got := rec.Header().Get("X-Stage"); got != "pre-commit" {
		t.Errorf("inner Header did not see the staged value: %q", got)
	}
}

func TestBufferingResponseWriter_TransportError(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	b := NewBufferingResponseWriter(rec, []int{503})

	errBoom := errors.New("dial tcp: connection refused")
	b.SetTransportError(errBoom)

	if !errors.Is(b.TransportError(), errBoom) {
		t.Errorf("TransportError = %v; want errBoom", b.TransportError())
	}
	if !b.ShouldRetry() {
		t.Error("ShouldRetry false on transport error")
	}
	if b.Committed() {
		t.Error("Committed true after transport error and no WriteHeader")
	}
}

func TestBufferingResponseWriter_TransportError_DoesNotRetryIfCommitted(t *testing.T) {
	t.Parallel()
	// Defensive: if somehow a transport error is recorded *after*
	// we've already committed (impossible per ReverseProxy semantics
	// but worth pinning), retry must be disallowed because the
	// client has already seen part of the response.
	rec := httptest.NewRecorder()
	b := NewBufferingResponseWriter(rec, []int{503})
	b.WriteHeader(http.StatusOK)
	b.SetTransportError(errors.New("late tear-down"))

	if b.ShouldRetry() {
		t.Error("ShouldRetry true after commit; retry window must be closed")
	}
}

func TestBufferingResponseWriter_NoStatusEverNoRetry(t *testing.T) {
	t.Parallel()
	// An attempt that completes without any WriteHeader and no
	// transport error is "the server just hung up cleanly" — the
	// orchestrator should not retry, because we can't tell whether
	// upstream did anything billable.
	rec := httptest.NewRecorder()
	b := NewBufferingResponseWriter(rec, []int{503})

	if b.ShouldRetry() {
		t.Error("ShouldRetry true with no status and no error; want false")
	}
}

func TestBufferingResponseWriter_Unwrap(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	b := NewBufferingResponseWriter(rec, nil)
	if b.Unwrap() != rec {
		t.Error("Unwrap did not return the underlying writer")
	}
}

func TestBufferingResponseWriter_FlushAfterWrite(t *testing.T) {
	t.Parallel()
	// SSE-style: WriteHeader → Write → Flush in a loop. After
	// commit, Flush must pass through every time so each chunk
	// reaches the client immediately.
	rec := newFlushRecorder()
	b := NewBufferingResponseWriter(rec, []int{503})

	b.WriteHeader(http.StatusOK)
	for i := 0; i < 5; i++ {
		if _, err := b.Write([]byte("data: chunk\n\n")); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
		b.Flush()
	}
	if rec.flushes != 5 {
		t.Errorf("Flush count = %d; want 5", rec.flushes)
	}
}

func TestBufferingResponseWriter_EmptyRetrySetAlwaysCommits(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	b := NewBufferingResponseWriter(rec, nil)

	b.WriteHeader(http.StatusServiceUnavailable)
	if !b.Committed() {
		t.Error("empty retry set should commit every status")
	}
	if b.ShouldRetry() {
		t.Error("empty retry set + no transport error must not retry")
	}
}
