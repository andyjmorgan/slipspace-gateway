package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxRequestBody = 8 << 20

// sessionHeader is the inbound HTTP header the server reads to scope a
// request to a registry session pool. The gateway already echoes this
// header end-to-end, so tests just set it on the outbound request.
const sessionHeader = "X-Sluice-Session-Id"

type server struct {
	mux *http.ServeMux

	reg *registry

	requestCount atomic.Uint64

	captured *capturedLog
}

// CapturedRequest is the snapshot of an inbound provider-handler
// request the mock LLM took at receive time. E2E tests inspect
// these to assert the gateway's rule engine produced the expected
// outgoing request shape (headers, query, body) on the upstream-
// bound side of the wire.
type CapturedRequest struct {
	Method string `json:"method"`

	Path string `json:"path"`

	Query string `json:"query,omitempty"`

	Headers map[string]string `json:"headers"`

	Body string `json:"body"`

	// SessionID is the value of X-Sluice-Session-Id on the inbound
	// request, empty when no session header was set. Lets tests
	// scope captured-request assertions to a single scenario via
	// the ?session=<id> query on /control/captured.
	SessionID string `json:"session_id,omitempty"`
}

// capturedLog is the bounded ring of captured requests. Tests
// usually only need the latest, but holding the last few covers
// rare cases that fire multiple upstream calls per test (e.g.
// resilience retries in v1.2).
type capturedLog struct {
	mu sync.Mutex

	max int

	entries []CapturedRequest
}

func newCapturedLog(max int) *capturedLog { return &capturedLog{max: max} }

func (c *capturedLog) record(req CapturedRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, req)
	if len(c.entries) > c.max {
		drop := len(c.entries) - c.max
		c.entries = append([]CapturedRequest(nil), c.entries[drop:]...)
	}
}

func (c *capturedLog) snapshot() []CapturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]CapturedRequest, len(c.entries))
	copy(out, c.entries)
	return out
}

func (c *capturedLog) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
}

func newServer(reg *registry) *server {
	s := &server{
		mux:      http.NewServeMux(),
		reg:      reg,
		captured: newCapturedLog(32),
	}
	s.routes()
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *server) routes() {
	s.mux.HandleFunc("/healthz", s.healthz)

	s.mux.HandleFunc("/control/responses", s.controlResponses)
	s.mux.HandleFunc("/control/state", s.controlState)
	s.mux.HandleFunc("/control/captured", s.controlCaptured)

	provider := s.providerHandler()

	for _, path := range providerPaths {
		s.mux.HandleFunc(path, provider)
	}

	s.mux.HandleFunc("/v1beta/models/", provider)
	// Anthropic Message Batches live under a subtree (retrieve is
	// /v1/messages/batches/{id}); register the prefix so per-id paths
	// reach the canned-response registry. The exact /v1/messages route
	// above still claims the messages-create path.
	s.mux.HandleFunc("/v1/messages/batches/", provider)
}

var providerPaths = []string{
	"/v1/chat/completions",
	"/v1/responses",
	"/v1/models",
	"/v1/messages",
	"/v1beta/models",
	// OpenAI-compat surface on Gemini — Anthropic's compat path is
	// the same /v1/chat/completions string and is already covered
	// above. Gemini publishes its own distinct path so the mock
	// needs to claim it explicitly.
	"/v1beta/openai/chat/completions",
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *server) providerHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.requestCount.Add(1)

		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body: " + err.Error()})
			return
		}
		_ = r.Body.Close()

		sessionID := r.Header.Get(sessionHeader)

		s.captured.record(CapturedRequest{
			Method:    r.Method,
			Path:      r.URL.Path,
			Query:     r.URL.RawQuery,
			Headers:   flattenHeader(r.Header),
			Body:      string(body),
			SessionID: sessionID,
		})

		canned, ok := s.reg.match(sessionID, r.Method, r.URL.Path, string(body))
		if !ok {
			slog.Debug("no canned response matched", "method", r.Method, "path", r.URL.Path, "session", sessionID)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no canned response"})
			return
		}

		if canned.DelayMs > 0 {
			if !sleepWithContext(r.Context(), time.Duration(canned.DelayMs)*time.Millisecond) {
				return
			}
		}

		switch canned.Behavior {
		case BehaviorClose:
			closeConn(w)
			return
		case BehaviorHang:
			<-r.Context().Done()
			return
		}

		if canned.Streaming {
			s.writeStream(r.Context(), w, canned)
			return
		}

		s.writeNonStream(w, canned)
	}
}

func (s *server) writeNonStream(w http.ResponseWriter, c CannedResponse) {
	for k, v := range c.Headers {
		w.Header().Set(k, v)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	status := c.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if c.Body != "" {
		_, _ = io.Copy(w, strings.NewReader(c.Body))
	}
}

// writeStream replays canned SSE chunks. Chunks are written verbatim wrapped
// in "data: ...\n\n" framing, flushing after every chunk. Inter-chunk delays
// honor request-context cancellation so client disconnect terminates promptly.
func (s *server) writeStream(ctx context.Context, w http.ResponseWriter, c CannedResponse) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}

	for k, v := range c.Headers {
		w.Header().Set(k, v)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-cache")
	}

	status := c.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)

	for _, chunk := range c.StreamChunks {
		if chunk.DelayMs > 0 {
			if !sleepWithContext(ctx, time.Duration(chunk.DelayMs)*time.Millisecond) {
				return
			}
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", chunk.Data); err != nil {
			return
		}
		flusher.Flush()
	}
}

// closeConn hijacks the response's underlying TCP connection and
// closes it without writing any status. Models a transport-error
// condition for resilience tests. A handler that cannot hijack falls
// back to writing a 500 so the caller sees the failure rather than
// silently nothing.
func closeConn(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "behavior=close requires hijack support"})
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		slog.Error("hijack for behavior=close failed", "err", err)
		return
	}
	_ = conn.Close()
}

// sleepWithContext sleeps d, returning true on normal completion and
// false if ctx is cancelled first. Used by DelayMs and per-chunk
// delays so client disconnect terminates the handler promptly.
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (s *server) controlResponses(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	switch r.Method {
	case http.MethodPost:
		s.controlAdd(w, r, session)
	case http.MethodDelete:
		if session == "" {
			s.reg.clearAll()
		} else {
			s.reg.clearSession(session)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "POST, DELETE")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *server) controlAdd(w http.ResponseWriter, r *http.Request, session string) {
	defer func() { _ = r.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Errorf("read body: %w", err).Error()})
		return
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var c CannedResponse
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty body"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "decode: " + err.Error()})
		return
	}

	s.reg.add(session, c)
	w.WriteHeader(http.StatusCreated)
}

func (s *server) controlState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	state := struct {
		Responses    []CannedResponse            `json:"responses"`
		Sessions     map[string][]CannedResponse `json:"sessions,omitempty"`
		RequestCount uint64                      `json:"request_count"`
	}{
		Responses:    s.reg.snapshotGlobal(),
		Sessions:     s.reg.snapshotSessions(),
		RequestCount: s.requestCount.Load(),
	}
	writeJSON(w, http.StatusOK, state)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// controlCaptured exposes the captured-request log to e2e tests.
//
//	GET    — returns {"captured": [...]} for inspection. Optional
//	         ?session=<id> filters to one session's traffic.
//	DELETE — clears the log (used by tests that rerun within one harness)
func (s *server) controlCaptured(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		captured := s.captured.snapshot()
		if session := r.URL.Query().Get("session"); session != "" {
			filtered := make([]CapturedRequest, 0, len(captured))
			for _, c := range captured {
				if c.SessionID == session {
					filtered = append(filtered, c)
				}
			}
			captured = filtered
		}
		writeJSON(w, http.StatusOK, struct {
			Captured []CapturedRequest `json:"captured"`
		}{Captured: captured})
	case http.MethodDelete:
		s.captured.clear()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, DELETE")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// flattenHeader collapses http.Header to a single-value map. Tests
// don't currently assert on multi-value headers; if that changes
// we'd switch to map[string][]string here.
func flattenHeader(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) == 1 {
			out[k] = vs[0]
		} else if len(vs) > 1 {
			out[k] = strings.Join(vs, ", ")
		}
	}
	return out
}
