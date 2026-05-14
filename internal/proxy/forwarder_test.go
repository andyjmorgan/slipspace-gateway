package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
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
	f := New(Options{Observer: obs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/models", nil)
	dest := newDestination(t, upstream.URL+"/v1/models")

	if err := f.Forward(context.Background(), rec, req, dest); err != nil {
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

	dest := newDestination(t, upstream.URL+"/v1/messages")
	dest.DropHeaders = []string{"X-Custom-Drop"}
	dest.OutgoingHeaders = http.Header{
		"Authorization":     []string{"Bearer upstream-token"},
		"X-Provider-Header": []string{"abc"},
	}

	if err := f.Forward(context.Background(), rec, req, dest); err != nil {
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
	f := New(Options{Observer: obs})

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dest := newDestination(t, upstream.URL+"/v1/stream")
		if err := f.Forward(r.Context(), w, r, dest); err != nil {
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
	f := New(Options{Observer: obs})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)
	dest := newDestination(t, upstream.URL+"/v1/x")

	if err := f.Forward(context.Background(), rec, req, dest); err != nil {
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
	f := New(Options{Observer: obs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)

	upstreamURL, _ := url.Parse("http://127.0.0.1:1/v1/x")
	base, _ := url.Parse("http://127.0.0.1:1")
	dest := Destination{BaseURL: base, UpstreamURL: upstreamURL}

	if err := f.Forward(context.Background(), rec, req, dest); err != nil {
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
	if err := f.Forward(context.Background(), rec, req, dest); err != nil {
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
	err := f.Forward(context.Background(), rec, req, Destination{})
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

	if err := f.Forward(context.Background(), rec, req, dest); err != nil {
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
	err := f.Forward(context.Background(), rec, req, Destination{UpstreamURL: upstreamURL})
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
}

func TestNew_DefaultsApplied(t *testing.T) {
	f := New(Options{})
	if f.logger == nil {
		t.Fatalf("default logger should be set")
	}
	if f.observer == nil {
		t.Fatalf("default observer should be set")
	}
}

func TestForward_ConcurrentRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	f := New(Options{Observer: newRecordingObserver()})
	const n = 25
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "http://gateway.local/v1/x", nil)
			dest := newDestination(t, upstream.URL+"/v1/x")
			if err := f.Forward(context.Background(), rec, req, dest); err != nil {
				t.Errorf("Forward: %v", err)
			}
			if rec.Code != http.StatusOK {
				t.Errorf("status: %d", rec.Code)
			}
		}()
	}
	wg.Wait()
}
