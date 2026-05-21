package livefeed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewResponseBuffer_RejectsNonPositiveMax(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, -1, -1024} {
		if got := NewResponseBuffer(n); got != nil {
			t.Errorf("NewResponseBuffer(%d) returned non-nil; want nil", n)
		}
	}
}

func TestResponseBuffer_AppendStoresBytes(t *testing.T) {
	t.Parallel()
	b := NewResponseBuffer(64)
	b.Append([]byte("hello"))
	b.Append([]byte(" world"))
	if got := string(b.Bytes()); got != "hello world" {
		t.Errorf("Bytes=%q want 'hello world'", got)
	}
	if got := b.Total(); got != 11 {
		t.Errorf("Total=%d want 11", got)
	}
	if b.Truncated() {
		t.Error("Truncated=true before cap reached")
	}
}

func TestResponseBuffer_TruncatesAtCap(t *testing.T) {
	t.Parallel()
	b := NewResponseBuffer(4)
	b.Append([]byte("hello"))
	if got := string(b.Bytes()); got != "hell" {
		t.Errorf("Bytes=%q want 'hell' (truncated)", got)
	}
	if !b.Truncated() {
		t.Error("Truncated=false after exceeding cap")
	}
	// Subsequent appends still count toward Total but write nothing.
	b.Append([]byte("xxxxxxxx"))
	if got := string(b.Bytes()); got != "hell" {
		t.Errorf("Bytes after cap-hit append=%q want still 'hell'", got)
	}
	if got := b.Total(); got != 13 {
		t.Errorf("Total=%d want 13 (5+8)", got)
	}
}

func TestResponseBuffer_NilSafe(t *testing.T) {
	t.Parallel()
	var b *ResponseBuffer
	b.Append([]byte("x")) // must not panic
	if b.Bytes() != nil {
		t.Error("nil buffer Bytes() should return nil")
	}
	if b.Total() != 0 || b.Truncated() {
		t.Error("nil buffer should report zero / not truncated")
	}
}

func TestResponseBuffer_BytesReturnsCopy(t *testing.T) {
	t.Parallel()
	b := NewResponseBuffer(64)
	b.Append([]byte("abc"))
	got := b.Bytes()
	got[0] = 'X'
	if string(b.Bytes()) != "abc" {
		t.Error("mutating returned slice mutated the buffer")
	}
}

func TestWrapResponseWriter_NilBuf_Passthrough(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	got := WrapResponseWriter(rec, nil)
	if got != http.ResponseWriter(rec) {
		t.Error("nil buf should return the original writer")
	}
}

func TestWrapResponseWriter_TeesWritesToBuf(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	buf := NewResponseBuffer(32)
	w := WrapResponseWriter(rec, buf)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("hello ")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	if got := rec.Body.String(); got != "hello world" {
		t.Errorf("recorder got %q", got)
	}
	if got := string(buf.Bytes()); got != "hello world" {
		t.Errorf("buf got %q", got)
	}
}

// flushingRecorder lets us assert the teeWriter forwards Flush.
type flushingRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flushingRecorder) Flush() { f.flushed = true }

func TestWrapResponseWriter_ForwardsFlush(t *testing.T) {
	t.Parallel()
	rec := &flushingRecorder{ResponseRecorder: httptest.NewRecorder()}
	buf := NewResponseBuffer(32)
	w := WrapResponseWriter(rec, buf)
	f, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("teeWriter does not implement http.Flusher")
	}
	f.Flush()
	if !rec.flushed {
		t.Error("Flush() did not forward to the underlying writer")
	}
}

func TestResponseBuffer_ContextStashFetch(t *testing.T) {
	t.Parallel()
	buf := NewResponseBuffer(8)
	ctx := WithResponseBuffer(context.Background(), buf)
	got, ok := ResponseBufferFromContext(ctx)
	if !ok || got != buf {
		t.Fatalf("round-trip failed: ok=%v got=%p want=%p", ok, got, buf)
	}
	// Stashing nil leaves ctx unchanged.
	bare := WithResponseBuffer(context.Background(), nil)
	if _, ok := ResponseBufferFromContext(bare); ok {
		t.Error("ResponseBufferFromContext returned ok for ctx without buffer")
	}
}

func TestWrapResponseWriter_SnapshotsHeadersOnWriteHeader(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	buf := NewResponseBuffer(64)
	w := WrapResponseWriter(rec, buf)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Authorization", "Bearer leaked-token")
	w.WriteHeader(200)

	snap := buf.Headers()
	if got, want := snap["Content-Type"], []string{"application/json"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("Content-Type = %v, want %v", got, want)
	}
	if got := snap["Authorization"]; len(got) != 1 || got[0] != "[REDACTED]" {
		t.Errorf("Authorization not redacted: %v", got)
	}
}

func TestWrapResponseWriter_SnapshotsHeadersOnImplicitWriteHeader(t *testing.T) {
	t.Parallel()
	// stdlib's net/http triggers WriteHeader(200) implicitly on first
	// Write when the caller skipped it. Our teeWriter has to catch the
	// snapshot via Write too, otherwise non-streaming responses would
	// land with no headers in the body store.
	rec := httptest.NewRecorder()
	buf := NewResponseBuffer(64)
	w := WrapResponseWriter(rec, buf)
	w.Header().Set("X-Sluice-Correlation-Id", "abc-123")
	if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	snap := buf.Headers()
	if got := snap["X-Sluice-Correlation-Id"]; len(got) != 1 || got[0] != "abc-123" {
		t.Errorf("correlation header missing from snapshot: %v", snap)
	}
}

func TestResponseBuffer_HeadersNilWhenNoSnapshot(t *testing.T) {
	t.Parallel()
	buf := NewResponseBuffer(8)
	if got := buf.Headers(); got != nil {
		t.Errorf("Headers() = %v, want nil pre-snapshot", got)
	}
}

func TestResponseBuffer_HeadersNilSafe(t *testing.T) {
	t.Parallel()
	var b *ResponseBuffer
	if got := b.Headers(); got != nil {
		t.Errorf("nil buffer Headers() = %v, want nil", got)
	}
	b.setHeaders(map[string][]string{"X": {"y"}})
	if got := b.Headers(); got != nil {
		t.Errorf("nil buffer setHeaders+Headers() = %v, want nil", got)
	}
}

func TestResponseBuffer_HeadersReturnsCopy(t *testing.T) {
	t.Parallel()
	buf := NewResponseBuffer(8)
	buf.setHeaders(map[string][]string{"Content-Type": {"application/json"}})
	first := buf.Headers()
	first["Content-Type"][0] = "tampered"
	second := buf.Headers()
	if second["Content-Type"][0] != "application/json" {
		t.Errorf("Headers() returned aliased slice; second read = %v", second)
	}
}
