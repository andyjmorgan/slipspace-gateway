//go:build e2e

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

// SessionHeader is the inbound header the mockllm reads to scope a
// request to a specific session pool. Tests that want to multiplex
// independent scenarios through one mockllm instance set this header
// per scenario; harness.Session.Post does it automatically.
const SessionHeader = "X-Sluice-Session-Id"

// BehaviorClose and BehaviorHang mirror the mockllm transport-level
// failure modes. Tests reference these constants instead of the raw
// strings so a rename only has to happen in one place.
const (
	BehaviorClose = "close"
	BehaviorHang  = "hang"
)

// CannedStreamChunk is the test-side mirror of cmd/mockllm.StreamChunk. Kept
// here so test packages do not depend on the mockllm internal package.
type CannedStreamChunk struct {
	Data string `json:"data"`

	DelayMs int `json:"delay_ms,omitempty"`
}

// CannedResponse is the test-side mirror of cmd/mockllm.CannedResponse. Match
// criteria default to "any" when zero-valued.
//
// MaxResponses, DelayMs, and Behavior drive the resilience-testing
// primitives. See cmd/mockllm/registry.go for the full semantics.
type CannedResponse struct {
	Method string `json:"method,omitempty"`

	Path string `json:"path,omitempty"`

	RequestBodyContains string `json:"request_body_contains,omitempty"`

	Status int `json:"status,omitempty"`

	Headers map[string]string `json:"headers,omitempty"`

	Streaming bool `json:"streaming,omitempty"`

	Body string `json:"body,omitempty"`

	StreamChunks []CannedStreamChunk `json:"stream_chunks,omitempty"`

	MaxResponses int `json:"max_responses,omitempty"`

	DelayMs int `json:"delay_ms,omitempty"`

	Behavior string `json:"behavior,omitempty"`
}

// StageMockResponse POSTs a canned response to the mockllm control plane.
// Fails the test on any non-2xx reply. Adds to the global pool.
func (h *Harness) StageMockResponse(c CannedResponse) {
	h.T.Helper()
	h.stageMockResponse("", c)
}

func (h *Harness) stageMockResponse(session string, c CannedResponse) {
	h.T.Helper()

	body, err := json.Marshal(c)
	if err != nil {
		h.T.Fatalf("harness: marshal canned response: %v", err)
	}

	u := h.MockLLMURL + "/control/responses"
	if session != "" {
		u += "?session=" + url.QueryEscape(session)
	}
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		h.T.Fatalf("harness: build control request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.HTTP.Do(req)
	if err != nil {
		h.T.Fatalf("harness: POST /control/responses: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		buf, _ := io.ReadAll(resp.Body)
		h.T.Fatalf("harness: /control/responses status=%d body=%s", resp.StatusCode, string(buf))
	}
}

// ResetMockResponses clears all canned responses from the mockllm registry.
// Tests that stage multiple responses across t.Run subtests should call this
// in t.Cleanup to keep iterations hermetic.
func (h *Harness) ResetMockResponses() {
	h.T.Helper()
	h.resetMockResponses("")
}

func (h *Harness) resetMockResponses(session string) {
	h.T.Helper()

	u := h.MockLLMURL + "/control/responses"
	if session != "" {
		u += "?session=" + url.QueryEscape(session)
	}
	req, err := http.NewRequest(http.MethodDelete, u, http.NoBody)
	if err != nil {
		h.T.Fatalf("harness: build control DELETE: %v", err)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		h.T.Fatalf("harness: DELETE /control/responses: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		buf, _ := io.ReadAll(resp.Body)
		h.T.Fatalf("harness: /control/responses delete status=%d body=%s", resp.StatusCode, string(buf))
	}
}

// MockState returns the mockllm's current control state (canned responses +
// request count). Useful for tests that assert "upstream was called".
type MockState struct {
	Responses []CannedResponse `json:"responses"`

	Sessions map[string][]CannedResponse `json:"sessions,omitempty"`

	RequestCount uint64 `json:"request_count"`
}

// FetchMockState reads /control/state from mockllm.
func (h *Harness) FetchMockState() MockState {
	h.T.Helper()

	resp, err := h.HTTP.Get(h.MockLLMURL + "/control/state")
	if err != nil {
		h.T.Fatalf("harness: GET /control/state: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var state MockState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		h.T.Fatalf("harness: decode mock state: %v", err)
	}
	return state
}

// MockRequestCount is a convenience wrapper around FetchMockState.
func (h *Harness) MockRequestCount() uint64 {
	return h.FetchMockState().RequestCount
}

// CapturedRequest mirrors cmd/mockllm.CapturedRequest. Kept here so
// test packages do not depend on the mockllm internal package.
type CapturedRequest struct {
	Method string `json:"method"`

	Path string `json:"path"`

	Query string `json:"query,omitempty"`

	Headers map[string]string `json:"headers"`

	Body string `json:"body"`

	SessionID string `json:"session_id,omitempty"`
}

// FetchCapturedRequests pulls every upstream-bound request the mock
// LLM observed. E2E tests inspect these to verify rule-driven
// mutations (changeUrl, changeApiKey, setHeader, appendQueryString)
// produced the expected outgoing request shape.
func (h *Harness) FetchCapturedRequests() []CapturedRequest {
	h.T.Helper()
	return h.fetchCapturedRequests("")
}

func (h *Harness) fetchCapturedRequests(session string) []CapturedRequest {
	h.T.Helper()
	u := h.MockLLMURL + "/control/captured"
	if session != "" {
		u += "?session=" + url.QueryEscape(session)
	}
	resp, err := h.HTTP.Get(u)
	if err != nil {
		h.T.Fatalf("harness: GET /control/captured: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		buf, _ := io.ReadAll(resp.Body)
		h.T.Fatalf("harness: /control/captured status=%d body=%s", resp.StatusCode, string(buf))
	}
	var wrap struct {
		Captured []CapturedRequest `json:"captured"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrap); err != nil {
		h.T.Fatalf("harness: decode captured: %v", err)
	}
	return wrap.Captured
}

// LastCapturedRequest returns the most recent upstream-bound
// request the mock LLM observed, or nil when none have arrived.
// Convenience for tests that fire one request and assert on its
// outgoing shape.
func (h *Harness) LastCapturedRequest() *CapturedRequest {
	all := h.FetchCapturedRequests()
	if len(all) == 0 {
		return nil
	}
	return &all[len(all)-1]
}

// Session is a session-scoped view onto the mock LLM. Multiple
// sessions can run in parallel against one mockllm instance — each
// session's staged canned responses, MaxResponses pop counters, and
// captured-request inspections are independent. Sessions are created
// by Harness.NewSession and self-clean via t.Cleanup.
type Session struct {
	h *Harness

	id string
}

// NewSession mints a fresh UUID-keyed session and registers
// t.Cleanup to drop the session's pool on test teardown. The session
// ID is set on every outbound request the returned helper builds, so
// tests can string together multi-turn scenarios without juggling
// the X-Sluice-Session-Id header manually.
func (h *Harness) NewSession(t *testing.T) *Session {
	t.Helper()
	s := &Session{h: h, id: uuid.NewString()}
	t.Cleanup(func() { h.resetMockResponses(s.id) })
	return s
}

// ID returns the session's UUID. Useful for tests that need to set
// X-Sluice-Session-Id directly (e.g. PostStream or custom request
// builders).
func (s *Session) ID() string { return s.id }

// Stage adds a canned response to this session's pool only.
func (s *Session) Stage(c CannedResponse) {
	s.h.T.Helper()
	s.h.stageMockResponse(s.id, c)
}

// Captured returns the upstream-bound requests scoped to this
// session.
func (s *Session) Captured() []CapturedRequest {
	s.h.T.Helper()
	return s.h.fetchCapturedRequests(s.id)
}

// Reset drops every canned response in this session. Called
// automatically by t.Cleanup; callers usually do not need to invoke
// it manually.
func (s *Session) Reset() {
	s.h.T.Helper()
	s.h.resetMockResponses(s.id)
}

// Post fires a JSON POST against the gateway with this session's
// X-Sluice-Session-Id header attached. Wraps Harness.PostJSON; caller
// gets the same buffered Response. Extra headers (e.g. X-Sluice-
// Configuration) can be passed via header; nil is fine.
func (s *Session) Post(path string, body any, header http.Header) *Response {
	s.h.T.Helper()
	headers := cloneOrEmpty(header)
	headers.Set(SessionHeader, s.id)
	return s.h.PostJSON(path, body, headers)
}

// PostStream fires a streaming POST against the gateway under this
// session. Wraps Harness.PostStream.
func (s *Session) PostStream(path string, body any, header http.Header) *SSEReader {
	s.h.T.Helper()
	headers := cloneOrEmpty(header)
	headers.Set(SessionHeader, s.id)
	return s.h.PostStream(path, body, headers)
}

func cloneOrEmpty(h http.Header) http.Header {
	if h == nil {
		return http.Header{}
	}
	return h.Clone()
}

// FetchStashObject reads a payload from the NATS Object Store. Used by tests
// asserting the large-payload stash code path published a fetchable object.
func (h *Harness) FetchStashObject(bucket, key string) ([]byte, error) {
	if h.NATS == nil {
		return nil, fmt.Errorf("harness: nats connection not initialized")
	}
	js, err := jetstream.New(h.NATS)
	if err != nil {
		return nil, fmt.Errorf("harness: jetstream: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := js.ObjectStore(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("harness: object store %s: %w", bucket, err)
	}
	data, err := store.GetBytes(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("harness: object %s/%s: %w", bucket, key, err)
	}
	return data, nil
}
