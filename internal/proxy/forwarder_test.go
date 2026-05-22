package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
	"github.com/andyjmorgan/sluice-gateway/internal/headers"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func newDestination(t *testing.T, upstreamURL string) Destination {
	t.Helper()
	u, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	base := &url.URL{Scheme: u.Scheme, Host: u.Host}
	return Destination{BaseURL: base, UpstreamURL: u}
}

func TestForward_HappyPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("upstream method: want GET got %s", r.Method)
		}
		if r.URL.Path != "/v1/models" {
			t.Errorf("upstream path: want /v1/models got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"list"}`))
	}))
	t.Cleanup(upstream.Close)

	obs := newRecordingObserver()
	f := New(Options{ObserverFactory: staticObserver(obs)})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/models", nil)
	dest := newDestination(t, upstream.URL+"/v1/models")

	if _, err := f.Forward(context.Background(), rec, req, dest); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d", rec.Code)
	}
	if got := rec.Body.String(); got != `{"object":"list"}` {
		t.Fatalf("body: %q", got)
	}

	events := obs.snapshot()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d: %+v", len(events), events)
	}
	if events[0].kind != eventStart || events[1].kind != eventHeaders || events[2].kind != eventComplete {
		t.Fatalf("event ordering: %+v", events)
	}
	if events[1].statusCode != http.StatusOK {
		t.Fatalf("response headers status: %d", events[1].statusCode)
	}
	if events[1].streaming {
		t.Fatalf("response should not be flagged streaming")
	}
	if events[2].statusCode != http.StatusOK {
		t.Fatalf("complete status: %d", events[2].statusCode)
	}
	if events[2].durationMs < 0 {
		t.Fatalf("durationMs negative: %d", events[2].durationMs)
	}
}

func TestForward_SuccessLog(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f := New(Options{Logger: logger})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)
	dest := newDestination(t, upstream.URL+"/v1/x")
	if _, err := f.Forward(context.Background(), rec, req, dest); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	out := logs.String()
	if !strings.Contains(out, `"msg":"proxy: upstream completed"`) {
		t.Fatalf("success log not emitted: %s", out)
	}
	if !strings.Contains(out, `"status_code":201`) {
		t.Fatalf("success log missing status_code=201: %s", out)
	}
	if !strings.Contains(out, `"streaming":false`) {
		t.Fatalf("success log missing streaming=false: %s", out)
	}
	if !strings.Contains(out, `"upstream_url":"`+upstream.URL+`/v1/x"`) {
		t.Fatalf("success log missing upstream_url: %s", out)
	}
}

func TestForward_SuccessLog_StreamingFlag(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: done\n\n"))
	}))
	t.Cleanup(upstream.Close)

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f := New(Options{Logger: logger})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/stream", nil)
	dest := newDestination(t, upstream.URL+"/v1/stream")
	if _, err := f.Forward(context.Background(), rec, req, dest); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if !strings.Contains(logs.String(), `"streaming":true`) {
		t.Fatalf("streaming flag not set in success log: %s", logs.String())
	}
}

func TestForward_NoSuccessLogOnDialFailure(t *testing.T) {
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f := New(Options{Logger: logger})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)
	upstreamURL, _ := url.Parse("http://127.0.0.1:1/v1/x")
	base, _ := url.Parse("http://127.0.0.1:1")
	dest := Destination{BaseURL: base, UpstreamURL: upstreamURL}

	if _, err := f.Forward(context.Background(), rec, req, dest); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if strings.Contains(logs.String(), `"msg":"proxy: upstream completed"`) {
		t.Fatalf("success log must not fire on dial failure: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"msg":"proxy: upstream forward failed"`) {
		t.Fatalf("error log missing: %s", logs.String())
	}
}

func TestForward_HeaderRewrite(t *testing.T) {
	var captured http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	f := New(Options{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer client-token")
	req.Header.Set("X-Sluice-Configuration", "openai-default")
	req.Header.Set("X-Custom-Drop", "yes")
	req.Header.Set("X-Keep", "yes")
	req.Header.Set("Origin", "https://sluice.example.com")
	req.Header.Set("Referer", "https://sluice.example.com/chat")
	req.Header.Set("Cookie", "session=abc; other=def")
	req.Header.Set("Accept-Encoding", "gzip, br")

	dest := newDestination(t, upstream.URL+"/v1/messages")
	dest.DropHeaders = []string{"X-Custom-Drop"}
	dest.OutgoingHeaders = http.Header{
		"Authorization":     []string{"Bearer upstream-token"},
		"X-Provider-Header": []string{"abc"},
	}

	if _, err := f.Forward(context.Background(), rec, req, dest); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	if got := captured.Get("Authorization"); got != "Bearer upstream-token" {
		t.Fatalf("Authorization should be swapped, got %q", got)
	}
	if got := captured.Get("X-Sluice-Configuration"); got != "" {
		t.Fatalf("X-Sluice-Configuration should be dropped, got %q", got)
	}
	if got := captured.Get("X-Custom-Drop"); got != "" {
		t.Fatalf("X-Custom-Drop should be dropped, got %q", got)
	}
	if got := captured.Get("X-Keep"); got != "yes" {
		t.Fatalf("X-Keep should be forwarded, got %q", got)
	}
	if got := captured.Get("X-Provider-Header"); got != "abc" {
		t.Fatalf("X-Provider-Header should be set, got %q", got)
	}
	for _, name := range []string{"Origin", "Referer", "Cookie"} {
		if got := captured.Get(name); got != "" {
			t.Errorf("browser header %s should be stripped, got %q", name, got)
		}
	}
	// The client sent "gzip, br" but we strip Accept-Encoding so the
	// stdlib transport's auto-gzip negotiation can take over. The
	// upstream must never see brotli — Go's transport can't decode it,
	// and a br-encoded body would render as binary garbage in the
	// admin live-messages capture. The transport may add its own
	// "gzip" header (which it transparently decompresses on the way
	// back), so we assert on what the upstream observed: no br, and
	// no propagation of the client's literal value.
	if ae := captured.Get("Accept-Encoding"); strings.Contains(ae, "br") || ae == "gzip, br" {
		t.Errorf("Accept-Encoding leaked client value to upstream, got %q", ae)
	}
}

// TestForward_GzippedUpstream_DecodedToClient verifies the load-bearing
// property of stripping Accept-Encoding: a gzipped upstream response is
// transparently decoded by the stdlib transport, so the client (and the
// admin live-messages capture that tees writes to the client) sees
// plaintext bytes. If we ever let a non-gzip encoding propagate to the
// upstream, the body would arrive still-encoded and render as binary
// garbage in the viewer.
func TestForward_GzippedUpstream_DecodedToClient(t *testing.T) {
	plain := []byte(`{"hello":"world"}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var compressed bytes.Buffer
		zw := gzip.NewWriter(&compressed)
		if _, err := zw.Write(plain); err != nil {
			t.Fatalf("gzip write: %v", err)
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("gzip close: %v", err)
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(compressed.Bytes()); err != nil {
			t.Fatalf("upstream write: %v", err)
		}
	}))
	t.Cleanup(upstream.Close)

	f := New(Options{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)
	// Client requests brotli (we strip it). The stdlib transport will
	// then add Accept-Encoding: gzip on its own and transparently
	// decompress the upstream response before ReverseProxy reads it.
	req.Header.Set("Accept-Encoding", "br, gzip")

	dest := newDestination(t, upstream.URL+"/v1/x")
	if _, err := f.Forward(context.Background(), rec, req, dest); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	body := rec.Body.Bytes()
	if !bytes.Equal(body, plain) {
		t.Fatalf("client body = %q, want plaintext %q", body, plain)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding on client response = %q, want empty (transport should strip after decoding)", ce)
	}
}

func TestForward_Streaming(t *testing.T) {
	releaseSecond := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("upstream does not support flusher")
			return
		}
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		if _, err := w.Write([]byte("data: first\n\n")); err != nil {
			t.Errorf("write first: %v", err)
		}
		flusher.Flush()
		select {
		case <-releaseSecond:
		case <-r.Context().Done():
			return
		}
		if _, err := w.Write([]byte("data: second\n\n")); err != nil {
			t.Errorf("write second: %v", err)
		}
		flusher.Flush()
	}))
	t.Cleanup(upstream.Close)

	obs := newRecordingObserver()
	f := New(Options{ObserverFactory: staticObserver(obs)})

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dest := newDestination(t, upstream.URL+"/v1/stream")
		if _, err := f.Forward(r.Context(), w, r, dest); err != nil {
			t.Errorf("Forward: %v", err)
		}
	}))
	t.Cleanup(gateway.Close)

	resp, err := http.Get(gateway.URL + "/stream")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type: want text/event-stream got %q", ct)
	}

	reader := bufio.NewReader(resp.Body)
	firstLine, err := readUntilBlank(reader)
	if err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	firstArrival := time.Now()
	if !strings.Contains(firstLine, "first") {
		t.Fatalf("first chunk: want 'first', got %q", firstLine)
	}

	close(releaseSecond)
	secondLine, err := readUntilBlank(reader)
	if err != nil {
		t.Fatalf("read second chunk: %v", err)
	}
	if !strings.Contains(secondLine, "second") {
		t.Fatalf("second chunk: want 'second', got %q", secondLine)
	}
	if elapsed := time.Since(firstArrival); elapsed < 1*time.Millisecond {
		t.Logf("second chunk arrived %s after first (informational)", elapsed)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events := obs.snapshot()
		for _, e := range events {
			if e.kind == eventHeaders && e.streaming {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected response headers event with streaming=true, got %+v", obs.snapshot())
}

func readUntilBlank(r *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return sb.String(), err
		}
		sb.WriteString(line)
		if line == "\n" || line == "\r\n" {
			return sb.String(), nil
		}
	}
}

func TestForward_Upstream5xxPassesThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"oops"}`))
	}))
	t.Cleanup(upstream.Close)

	obs := newRecordingObserver()
	f := New(Options{ObserverFactory: staticObserver(obs)})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)
	dest := newDestination(t, upstream.URL+"/v1/x")

	if _, err := f.Forward(context.Background(), rec, req, dest); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500 got %d", rec.Code)
	}

	for _, e := range obs.snapshot() {
		if e.kind == eventError {
			t.Fatalf("OnUpstreamError should not fire on upstream HTTP 5xx; got %+v", e)
		}
	}
}

func TestForward_DialFailure(t *testing.T) {
	obs := newRecordingObserver()
	f := New(Options{ObserverFactory: staticObserver(obs)})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)

	upstreamURL, _ := url.Parse("http://127.0.0.1:1/v1/x")
	base, _ := url.Parse("http://127.0.0.1:1")
	dest := Destination{BaseURL: base, UpstreamURL: upstreamURL}

	if _, err := f.Forward(context.Background(), rec, req, dest); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: want 502 got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type: want application/json got %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body should be JSON: %v: %s", err, rec.Body.String())
	}

	var sawError bool
	for _, e := range obs.snapshot() {
		if e.kind == eventError {
			sawError = true
			if e.err == nil {
				t.Fatalf("upstream error event missing error value")
			}
		}
	}
	if !sawError {
		t.Fatalf("expected OnUpstreamError to fire on dial failure; events=%+v", obs.snapshot())
	}
}

func TestForward_DialFailure_BufferingWriter_RecordsTransportError(t *testing.T) {
	// When the caller wraps the writer in a BufferingResponseWriter
	// (the v1.2 orchestrator's pattern), ErrorHandler must record the
	// transport error on the buffer and leave the response decision
	// to the orchestrator — no 502 written to the client.
	obs := newRecordingObserver()
	f := New(Options{ObserverFactory: staticObserver(obs)})

	rec := httptest.NewRecorder()
	buf := NewBufferingResponseWriter(rec, []int{503})
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)

	upstreamURL, _ := url.Parse("http://127.0.0.1:1/v1/x")
	base, _ := url.Parse("http://127.0.0.1:1")
	dest := Destination{BaseURL: base, UpstreamURL: upstreamURL}

	result, err := f.Forward(context.Background(), buf, req, dest)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}

	if result.Err == nil {
		t.Error("Result.Err nil; want transport error captured")
	}
	if result.Committed {
		t.Error("Result.Committed true; want false on transport error")
	}
	if !buf.ShouldRetry() {
		t.Error("buf.ShouldRetry false on transport error")
	}
	if buf.TransportError() == nil {
		t.Error("buf.TransportError nil; should be set from ErrorHandler")
	}
	// No 502 should reach the underlying writer — the orchestrator
	// will decide what to write after inspecting the buffer.
	if rec.Code == http.StatusBadGateway {
		t.Error("inner ResponseWriter received 502; orchestrator path must skip writeBadGateway")
	}
}

func TestForward_PreservesUpstreamQuery(t *testing.T) {
	var capturedQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	f := New(Options{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)
	u, _ := url.Parse(upstream.URL + "/v1/x?api-version=2024-01")
	base := &url.URL{Scheme: u.Scheme, Host: u.Host}
	dest := Destination{BaseURL: base, UpstreamURL: u}
	if _, err := f.Forward(context.Background(), rec, req, dest); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if capturedQuery != "api-version=2024-01" {
		t.Fatalf("query: want api-version=2024-01 got %q", capturedQuery)
	}
}

func TestForward_NilUpstreamURL(t *testing.T) {
	f := New(Options{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)
	_, err := f.Forward(context.Background(), rec, req, Destination{})
	if err == nil || !strings.Contains(err.Error(), "UpstreamURL") {
		t.Fatalf("expected UpstreamURL error, got %v", err)
	}
}

func TestForward_NoBaseURLFallsBackToUpstreamHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	f := New(Options{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)
	u, _ := url.Parse(upstream.URL + "/v1/x")
	dest := Destination{UpstreamURL: u}

	if _, err := f.Forward(context.Background(), rec, req, dest); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d", rec.Code)
	}
}

func TestForward_NilBaseAndUpstreamReturnsError(t *testing.T) {
	f := New(Options{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)

	upstreamURL := &url.URL{Path: "/no-host"}
	_, err := f.Forward(context.Background(), rec, req, Destination{UpstreamURL: upstreamURL})
	if err == nil {
		t.Fatalf("expected error when both BaseURL and UpstreamURL host are missing")
	}
	if !strings.Contains(err.Error(), "BaseURL") {
		t.Fatalf("expected BaseURL error, got %v", err)
	}
}

func TestIsSSE(t *testing.T) {
	cases := []struct {
		header http.Header
		want   bool
	}{
		{http.Header{"Content-Type": []string{"text/event-stream"}}, true},
		{http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}}, true},
		{http.Header{"Content-Type": []string{"TEXT/EVENT-STREAM"}}, true},
		{http.Header{"Content-Type": []string{"application/json"}}, false},
		{http.Header{}, false},
		{http.Header{"Content-Type": []string{""}}, false},
	}
	for _, c := range cases {
		got := isSSE(c.header)
		if got != c.want {
			t.Errorf("isSSE(%v) = %v; want %v", c.header, got, c.want)
		}
	}
}

func TestStatusWriter_DefaultStatus200OnWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}
	if _, err := sw.Write([]byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if sw.status != http.StatusOK {
		t.Fatalf("status: want 200 got %d", sw.status)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("recorder status: want 200 got %d", rec.Code)
	}
}

func TestStatusWriter_DoubleWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}
	sw.WriteHeader(http.StatusCreated)
	sw.WriteHeader(http.StatusAccepted)
	if sw.status != http.StatusCreated {
		t.Fatalf("status: want 201, got %d", sw.status)
	}
}

func TestStatusWriter_FlushPassesThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}
	sw.Flush()
}

func TestStatusWriter_FlushOnNonFlusher(t *testing.T) {
	w := &nonFlusherWriter{header: http.Header{}}
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	sw.Flush()
}

func TestStatusWriter_Unwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}
	if sw.Unwrap() != rec {
		t.Fatalf("Unwrap should return the underlying writer")
	}
}

type nonFlusherWriter struct {
	header http.Header
	body   []byte
	status int
}

func (w *nonFlusherWriter) Header() http.Header { return w.header }
func (w *nonFlusherWriter) Write(b []byte) (int, error) {
	w.body = append(w.body, b...)
	return len(b), nil
}
func (w *nonFlusherWriter) WriteHeader(code int) { w.status = code }

func TestNopObserverImplementsObserver(t *testing.T) {
	var obs Observer = nopObserver{}
	obs.OnRequestStart(context.Background(), Destination{})
	obs.OnResponseHeaders(context.Background(), 200, http.Header{}, false)
	obs.OnUpstreamError(context.Background(), errors.New("boom"))
	obs.OnComplete(context.Background(), 200, 5)
	obs.OnRuleMatched(context.Background(), events.RuleMatched{RuleName: "noop"})
}

func TestNew_DefaultsApplied(t *testing.T) {
	f := New(Options{})
	if f.logger == nil {
		t.Fatalf("default logger should be set")
	}
	if f.observerFactory == nil {
		t.Fatalf("default observer factory should be set")
	}
	if obs := f.observerFactory(context.Background(), Destination{}); obs == nil {
		t.Fatalf("default observer factory should return a non-nil observer")
	}
}

func TestIsSensitiveHeaderName(t *testing.T) {
	cases := map[string]bool{
		"Authorization":          true,
		"authorization":          true,
		"Proxy-Authorization":    true,
		"X-Sluice-Authorization": true,
		"X-Api-Key":              true,
		"x-api-key":              true,
		"X-API-KEY":              true,
		"Anthropic-Api-Key":      true,
		"X-Goog-Api-Key":         true,
		"X-APIKey":               true,
		"Cookie":                 true,
		"Set-Cookie":             true,
		"X-CSRF-Token":           true,
		"X-Auth-Token":           true,
		"Authorization-Bearer":   true,
		"X-API-Secret":           true,

		"Content-Type":            false,
		"X-Custom-Header":         false,
		"User-Agent":              false,
		"X-Sluice-Correlation-Id": false,
		"X-Provider-Header":       false,
	}
	for name, want := range cases {
		if got := headers.IsSensitiveHeaderName(name); got != want {
			t.Errorf("headers.IsSensitiveHeaderName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestRedactSensitive(t *testing.T) {
	t.Run("nil and empty", func(t *testing.T) {
		if got := redactSensitive(nil); got != nil {
			t.Errorf("redactSensitive(nil) = %v, want nil", got)
		}
		if got := redactSensitive(http.Header{}); got != nil {
			t.Errorf("redactSensitive(empty) = %v, want nil", got)
		}
	})
	t.Run("redacts only sensitive names", func(t *testing.T) {
		in := http.Header{}
		in.Set("Authorization", "Bearer sk_live_secret")
		in.Set("X-Api-Key", "sk-real-anthropic-key")
		in.Set("X-Goog-Api-Key", "AIza...")
		in.Set("Cookie", "session=abc")
		in.Set("Content-Type", "application/json")
		in.Set("X-Provider-Header", "leave-me-alone")
		in.Add("Set-Cookie", "first=1")
		in.Add("Set-Cookie", "second=2")

		out := redactSensitive(in)

		for _, k := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Cookie"} {
			vals := out[http.CanonicalHeaderKey(k)]
			if len(vals) == 0 || vals[0] != "[REDACTED]" {
				t.Errorf("%s not redacted: %v", k, vals)
			}
		}
		if vals := out["Set-Cookie"]; len(vals) != 2 || vals[0] != "[REDACTED]" || vals[1] != "[REDACTED]" {
			t.Errorf("Set-Cookie multi-value not fully redacted: %v", vals)
		}
		if vals := out["Content-Type"]; len(vals) != 1 || vals[0] != "application/json" {
			t.Errorf("Content-Type should pass through, got %v", vals)
		}
		if vals := out["X-Provider-Header"]; len(vals) != 1 || vals[0] != "leave-me-alone" {
			t.Errorf("X-Provider-Header should pass through, got %v", vals)
		}
	})
	t.Run("returns independent slice copies", func(t *testing.T) {
		in := http.Header{"X-Keep": []string{"original"}}
		out := redactSensitive(in)
		out["X-Keep"][0] = "mutated"
		if got := in.Get("X-Keep"); got != "original" {
			t.Errorf("input mutated through redactSensitive copy: %q", got)
		}
	})
}

func TestForward_DebugLoggingEmitsHeaderTrace(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Upstream-Trace", "yes")
		w.Header().Set("Set-Cookie", "upstream=zzz")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := observability.WithLogger(context.Background(), logger)

	f := New(Options{Logger: logger})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/v1/messages", strings.NewReader("{}"))
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", "Bearer client-token")
	req.Header.Set("Origin", "https://sluice.example.com")
	req.Header.Set("X-Keep", "yes")

	dest := newDestination(t, upstream.URL+"/v1/messages")
	dest.OutgoingHeaders = http.Header{"X-Goog-Api-Key": []string{"AIza-upstream"}}

	if _, err := f.Forward(ctx, rec, req, dest); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	out := buf.String()
	for _, expect := range []string{
		`"msg":"proxy: inbound headers"`,
		`"msg":"proxy: stripped headers"`,
		`"msg":"proxy: outbound headers"`,
		`"msg":"proxy: upstream response headers"`,
		`"msg":"proxy: final response headers"`,
	} {
		if !strings.Contains(out, expect) {
			t.Errorf("expected log line %s in trace output:\n%s", expect, out)
		}
	}

	// Authorization in inbound/outbound traces must be redacted; the
	// upstream's Set-Cookie must be redacted in upstream-response trace.
	if !strings.Contains(out, `"Authorization":["[REDACTED]"]`) {
		t.Errorf("inbound Authorization should be redacted in trace; got:\n%s", out)
	}
	if !strings.Contains(out, `"X-Goog-Api-Key":["[REDACTED]"]`) {
		t.Errorf("outbound X-Goog-Api-Key should be redacted in trace; got:\n%s", out)
	}
	if !strings.Contains(out, `"Set-Cookie":["[REDACTED]"]`) {
		t.Errorf("upstream Set-Cookie should be redacted in trace; got:\n%s", out)
	}
	// Stripped headers list should contain Origin (we set it on the inbound).
	if !strings.Contains(out, `"Origin"`) {
		t.Errorf("stripped headers list should reference Origin; got:\n%s", out)
	}
	// Non-sensitive header should pass through verbatim.
	if !strings.Contains(out, `"X-Keep":["yes"]`) {
		t.Errorf("non-sensitive X-Keep should appear verbatim in trace; got:\n%s", out)
	}
}

func TestForward_ConcurrentRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	f := New(Options{ObserverFactory: staticObserver(newRecordingObserver())})
	const n = 25
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)
			dest := newDestination(t, upstream.URL+"/v1/x")
			if _, err := f.Forward(context.Background(), rec, req, dest); err != nil {
				t.Errorf("Forward: %v", err)
			}
			if rec.Code != http.StatusOK {
				t.Errorf("status: %d", rec.Code)
			}
		}()
	}
	wg.Wait()
}
