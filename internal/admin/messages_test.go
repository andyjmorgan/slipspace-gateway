package admin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	adminc "github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability/livefeed"
)

// newMessagesTestMeters mints a minimal observability.Meters bundle
// against a fresh ManualReader so the instrumented messages tests can
// run InstrumentRoute without standing up the full observability
// pipeline. Defined locally because instrument_test.go's helper lives
// in package admin_test.
func newMessagesTestMeters(t *testing.T) (*observability.Meters, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	m, err := observability.NewMeters(mp.Meter(observability.MeterName))
	if err != nil {
		t.Fatalf("NewMeters: %v", err)
	}
	return m, reader
}

func TestMessagesRecentHandler_503WhenRingNil(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/messages/recent", nil)
	MessagesRecentHandler(nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
}

func TestMessagesRecentHandler_ReturnsEntries(t *testing.T) {
	t.Parallel()
	ring, _ := livefeed.NewRing(4)
	ring.Append(livefeed.Entry{EventID: "a", At: time.Now().UTC(), Provider: "openai", Protocol: "chat_completions", Method: "POST", StatusCode: 200})
	ring.Append(livefeed.Entry{EventID: "b", At: time.Now().UTC(), Provider: "anthropic", Protocol: "models", Method: "GET", StatusCode: 503})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/messages/recent", nil)
	MessagesRecentHandler(ring).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	var got adminc.MessagesRecentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Capacity != 4 {
		t.Errorf("Capacity=%d want 4", got.Capacity)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("Entries=%d want 2", len(got.Entries))
	}
	if got.Entries[0].EventID != "a" || got.Entries[1].EventID != "b" {
		t.Errorf("wrong order: %+v", got.Entries)
	}
	if got.Entries[0].Method != "POST" || got.Entries[1].Method != "GET" {
		t.Errorf("Method not mapped onto wire: [0]=%q [1]=%q", got.Entries[0].Method, got.Entries[1].Method)
	}
}

func TestMessagesRecentHandler_HonoursLimitQuery(t *testing.T) {
	t.Parallel()
	ring, _ := livefeed.NewRing(8)
	for i := 0; i < 5; i++ {
		ring.Append(livefeed.Entry{EventID: "e", At: time.Now().UTC()})
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/messages/recent?limit=2", nil)
	MessagesRecentHandler(ring).ServeHTTP(rec, req)
	var got adminc.MessagesRecentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("Entries=%d want 2", len(got.Entries))
	}
}

func TestMessagesRecentHandler_RejectsBadLimit(t *testing.T) {
	t.Parallel()
	ring, _ := livefeed.NewRing(4)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/messages/recent?limit=abc", nil)
	MessagesRecentHandler(ring).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/messages/recent?limit=0", nil)
	MessagesRecentHandler(ring).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
}

func TestMessagesRecentHandler_EmptyRingReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	ring, _ := livefeed.NewRing(4)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/messages/recent", nil)
	MessagesRecentHandler(ring).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	// Body must contain `[]`, not `null`, so the SPA can map it.
	if !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Errorf("expected empty entries array, got %s", rec.Body.String())
	}
}

func TestMessagesRecentHandler_RuleHitsMappedOntoWire(t *testing.T) {
	t.Parallel()
	ring, _ := livefeed.NewRing(4)
	ring.Append(livefeed.Entry{
		EventID:    "with-rules",
		At:         time.Now().UTC(),
		StatusCode: 200,
		RulesMatched: []livefeed.RuleHit{
			{
				RuleName:       "claude-haiku-redirect",
				ActionsApplied: []string{"changeProvider", "setHeader"},
				Terminated:     false,
			},
			{
				RuleName:     "fallback-block",
				Terminated:   true,
				ErrorMessage: "blocked by policy",
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/messages/recent", nil)
	MessagesRecentHandler(ring).ServeHTTP(rec, req)

	var got adminc.MessagesRecentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Entries) != 1 || len(got.Entries[0].RulesMatched) != 2 {
		t.Fatalf("expected 1 entry with 2 rule hits, got %+v", got)
	}
	first := got.Entries[0].RulesMatched[0]
	if first.RuleName != "claude-haiku-redirect" || len(first.ActionsApplied) != 2 {
		t.Errorf("first rule = %+v", first)
	}
	second := got.Entries[0].RulesMatched[1]
	if !second.Terminated || second.ErrorMessage != "blocked by policy" {
		t.Errorf("second rule = %+v", second)
	}
}

func TestMessageBodyHandler_503WhenStoreNil(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/messages/abc/body", nil)
	req.SetPathValue("event_id", "abc")
	MessageBodyHandler(nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
}

func TestMessageBodyHandler_404WhenMissing(t *testing.T) {
	t.Parallel()
	store, _ := livefeed.NewBodyStore(1024)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/messages/missing/body", nil)
	req.SetPathValue("event_id", "missing")
	MessageBodyHandler(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
}

func TestMessageBodyHandler_400WhenEventIDMissing(t *testing.T) {
	t.Parallel()
	store, _ := livefeed.NewBodyStore(1024)
	rec := httptest.NewRecorder()
	// No SetPathValue — r.PathValue("event_id") returns "" and the
	// handler should reject with 400.
	req := httptest.NewRequest(http.MethodGet, "/messages//body", nil)
	MessageBodyHandler(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
}

func TestMessageBodyHandler_ReturnsStoredEnvelope(t *testing.T) {
	t.Parallel()
	store, _ := livefeed.NewBodyStore(64 * 1024)
	store.Put("evt-1", livefeed.BodyEnvelope{
		Request:            []byte(`{"prompt":"hi"}`),
		RequestTotalBytes:  15,
		Response:           []byte(`raw sse bytes`),
		ResponseTotalBytes: 13,
		ResponseAssembled:  `{"choices":[{"index":0,"message":{"role":"assistant","content":"hello world"}}]}`,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/messages/evt-1/body", nil)
	req.SetPathValue("event_id", "evt-1")
	MessageBodyHandler(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	var got adminc.MessageBodyDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Request != `{"prompt":"hi"}` {
		t.Errorf("Request = %q", got.Request)
	}
	if !strings.Contains(got.ResponseAssembled, `"content":"hello world"`) {
		t.Errorf("ResponseAssembled = %q", got.ResponseAssembled)
	}
}

// failingWriter is a minimal http.ResponseWriter that returns an
// error on Write after a configurable number of successful calls.
// Used to drive the writeSSE* error paths without a real network
// stack.
type failingWriter struct {
	header http.Header
	allow  int // number of writes allowed before failing
	writes int
}

func (f *failingWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}

func (f *failingWriter) Write(p []byte) (int, error) {
	f.writes++
	if f.writes > f.allow {
		return 0, errors.New("write failed")
	}
	return len(p), nil
}

func (f *failingWriter) WriteHeader(int) {}

func TestWriteSSEEntry_PropagatesWriteError(t *testing.T) {
	t.Parallel()
	// First Write succeeds (the SSE preamble), second fails (the JSON
	// payload). writeSSEEntry should return the underlying error.
	w := &failingWriter{allow: 1}
	err := writeSSEEntry(w, livefeed.Entry{EventID: "e", At: time.Now().UTC()})
	if err == nil {
		t.Fatal("writeSSEEntry should propagate write error")
	}
}

func TestWriteSSEEntry_FailsOnFirstWrite(t *testing.T) {
	t.Parallel()
	w := &failingWriter{allow: 0}
	err := writeSSEEntry(w, livefeed.Entry{EventID: "e", At: time.Now().UTC()})
	if err == nil {
		t.Fatal("writeSSEEntry should propagate first-write error")
	}
}

func TestWriteSSEDrop_PropagatesWriteError(t *testing.T) {
	t.Parallel()
	w := &failingWriter{allow: 0}
	if err := writeSSEDrop(w, 3); err == nil {
		t.Fatal("writeSSEDrop should propagate write error")
	}
}

// nonFlushingRecorder embeds httptest.ResponseRecorder without exposing
// http.Flusher. The streaming handler rejects responses that can't
// flush, which is the only code path we cover here.
type nonFlushingRecorder struct{ rec *httptest.ResponseRecorder }

func (n *nonFlushingRecorder) Header() http.Header         { return n.rec.Header() }
func (n *nonFlushingRecorder) Write(p []byte) (int, error) { return n.rec.Write(p) }
func (n *nonFlushingRecorder) WriteHeader(c int)           { n.rec.WriteHeader(c) }

func TestMessagesStreamHandler_500WhenResponseCannotFlush(t *testing.T) {
	t.Parallel()
	ring, _ := livefeed.NewRing(4)
	rec := &nonFlushingRecorder{rec: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/messages/stream", nil)
	MessagesStreamHandler(ring).ServeHTTP(rec, req)
	if rec.rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", rec.rec.Code)
	}
}

func TestMessagesStreamHandler_503WhenRingNil(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/messages/stream", nil)
	MessagesStreamHandler(nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
}

func TestMessagesStreamHandler_DeliversAppendedEntries(t *testing.T) {
	t.Parallel()
	ring, _ := livefeed.NewRing(4)
	srv := httptest.NewServer(MessagesStreamHandler(ring))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type=%q want text/event-stream", ct)
	}

	// Wait until the handler has registered its subscriber before we
	// publish; otherwise the entry can land before SubscribeCtx adds
	// the subscriber and the test races.
	deadline := time.Now().Add(2 * time.Second)
	for ring.SubscriberCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("subscriber never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	ring.Append(livefeed.Entry{EventID: "e1", At: time.Now().UTC(), Provider: "openai", StatusCode: 200})

	// Read until we see the event frame.
	br := bufio.NewReader(resp.Body)
	got, err := readUntilEvent(br, "message", 3*time.Second)
	if err != nil {
		t.Fatalf("readUntilEvent: %v", err)
	}
	var entry adminc.MessageEntry
	if err := json.Unmarshal([]byte(got), &entry); err != nil {
		t.Fatalf("unmarshal SSE data %q: %v", got, err)
	}
	if entry.EventID != "e1" || entry.Provider != "openai" {
		t.Errorf("entry = %+v want EventID=e1 Provider=openai", entry)
	}
}

// TestMessagesStream_ThroughInstrumentRoute pins the production path:
// the SSE handler must keep working when InstrumentRoute wraps it.
// statusRecorder embeds http.ResponseWriter without exposing Flusher
// by default — this test would have caught the v1.1.7 regression where
// the wrapper stripped Flush and the stream handler bailed with 500.
func TestMessagesStream_ThroughInstrumentRoute(t *testing.T) {
	t.Parallel()
	ring, _ := livefeed.NewRing(4)
	meters, _ := newMessagesTestMeters(t)
	srv := httptest.NewServer(InstrumentRoute(meters, "/api/v1/messages/stream", MessagesStreamHandler(ring)))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (wrapper likely stripped Flusher)", resp.StatusCode)
	}

	deadline := time.Now().Add(2 * time.Second)
	for ring.SubscriberCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("subscriber never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	ring.Append(livefeed.Entry{EventID: "wrapped", At: time.Now().UTC(), Provider: "openai", StatusCode: 200})

	br := bufio.NewReader(resp.Body)
	data, err := readUntilEvent(br, "message", 3*time.Second)
	if err != nil {
		t.Fatalf("readUntilEvent through instrumented wrapper: %v", err)
	}
	var entry adminc.MessageEntry
	if err := json.Unmarshal([]byte(data), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.EventID != "wrapped" {
		t.Fatalf("entry.EventID = %q want wrapped", entry.EventID)
	}
}

func TestMessagesStreamHandler_DropEventOnBufferOverflow(t *testing.T) {
	t.Parallel()
	// Capacity 1 makes the per-subscriber buffer (default 32 in
	// SubscribeCtx) the tighter constraint. We push enough entries
	// to overflow, then drain — the handler should emit a `drop`
	// event before the next `message`.
	ring, _ := livefeed.NewRing(200)
	srv := httptest.NewServer(MessagesStreamHandler(ring))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Wait for subscription so the first appends actually fan out to it.
	deadline := time.Now().Add(2 * time.Second)
	for ring.SubscriberCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("subscriber never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Append many entries faster than the handler can read them — the
	// handler reads at line speed but the bufio readers below haven't
	// started draining yet. Force a drop by piling on before any read.
	for i := 0; i < 200; i++ {
		ring.Append(livefeed.Entry{EventID: "burst", At: time.Now().UTC()})
	}

	// Drain SSE frames; assert we eventually see a drop event.
	br := bufio.NewReader(resp.Body)
	seenDrop := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		if strings.HasPrefix(line, "event: drop") {
			seenDrop = true
			break
		}
	}
	if !seenDrop {
		t.Fatalf("never saw drop event despite buffer overflow")
	}
}

// readUntilEvent reads SSE frames from br until it sees an
// `event: <name>` line, then returns the immediately following
// `data:` payload. Times out per the deadline.
func readUntilEvent(br *bufio.Reader, name string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(line, "event: ") {
			continue
		}
		if strings.TrimSpace(strings.TrimPrefix(line, "event:")) != name {
			continue
		}
		// next non-empty line should be `data: <payload>`
		data, err := br.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(strings.TrimPrefix(data, "data:")), nil
	}
	return "", context.DeadlineExceeded
}
