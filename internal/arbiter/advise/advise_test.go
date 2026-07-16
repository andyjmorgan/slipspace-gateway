package advise

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	contractsadvise "github.com/andyjmorgan/slipspace-gateway/contracts/advise"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/registry"
)

// stubVerifier is a hand-rolled verifier that returns a fixed error.
type stubVerifier struct {
	err error
}

func (s *stubVerifier) Verify(string, []byte, string) error { return s.err }

// stubJudge is a hand-rolled judge that records its calls.
type stubJudge struct {
	verdict contractsadvise.Verdict
	err     error
	calls   int
	last    contractsadvise.Request
}

func (s *stubJudge) Judge(_ context.Context, req contractsadvise.Request) (contractsadvise.Verdict, error) {
	s.calls++
	s.last = req
	return s.verdict, s.err
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// post drives the handler with one advisory POST and the given headers.
func post(t *testing.T, h *Handler, headers map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/advise/route", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// trustedHeaders returns headers a stub verifier accepts.
func trustedHeaders() map[string]string {
	return map[string]string{
		headerGatewayID: "gw-test",
		headerSignature: "deadbeef",
	}
}

// requestBody marshals a contract request for the wire.
func requestBody(t *testing.T, req contractsadvise.Request) string {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return string(raw)
}

func TestServeHTTP_MissingHeaders(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"no headers", map[string]string{}},
		{"missing signature", map[string]string{headerGatewayID: "gw-test"}},
		{"missing gateway id", map[string]string{headerSignature: "deadbeef"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := &stubJudge{}
			h := NewHandler(&stubVerifier{}, j, time.Minute, discardLogger())
			rr := post(t, h, tc.headers, "{}")
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
			}
			if j.calls != 0 {
				t.Errorf("judge called %d times, want 0", j.calls)
			}
		})
	}
}

func TestServeHTTP_VerifierErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"bad signature", registry.ErrBadSignature, http.StatusUnauthorized},
		{"unknown gateway", registry.ErrUnknownGateway, http.StatusUnauthorized},
		{"other verify error", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := &stubJudge{}
			h := NewHandler(&stubVerifier{err: tc.err}, j, time.Minute, discardLogger())
			rr := post(t, h, trustedHeaders(), "{}")
			if rr.Code != tc.want {
				t.Errorf("status = %d, want %d", rr.Code, tc.want)
			}
			if j.calls != 0 {
				t.Errorf("judge called %d times, want 0", j.calls)
			}
		})
	}
}

func TestServeHTTP_MalformedJSON(t *testing.T) {
	j := &stubJudge{}
	h := NewHandler(&stubVerifier{}, j, time.Minute, discardLogger())
	rr := post(t, h, trustedHeaders(), "{not json")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	if j.calls != 0 {
		t.Errorf("judge called %d times, want 0", j.calls)
	}
}

func TestServeHTTP_HappyPath(t *testing.T) {
	want := contractsadvise.Verdict{Switch: true, Model: "cheap-candidate-a", Reason: "trivial", Confidence: 0.9}
	j := &stubJudge{verdict: want}
	h := NewHandler(&stubVerifier{}, j, time.Minute, discardLogger())

	req := contractsadvise.Request{
		Configuration:    "prod",
		Protocol:         "messages",
		Model:            "requested-model-internal",
		ConversationID:   "conv-1",
		AgentFamily:      "claude-code",
		Entrypoint:       "cli",
		IsSubagent:       true,
		SystemPrefix:     "You are a subagent.",
		FirstUserMessage: "List the files in the repo.",
		ToolNames:        []string{"Read", "Bash"},
	}
	rr := post(t, h, trustedHeaders(), requestBody(t, req))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	var got contractsadvise.Verdict
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode verdict: %v", err)
	}
	if got != want {
		t.Errorf("verdict = %+v, want %+v", got, want)
	}

	if j.calls != 1 {
		t.Fatalf("judge called %d times, want 1", j.calls)
	}
	if j.last.ConversationID != req.ConversationID ||
		j.last.FirstUserMessage != req.FirstUserMessage ||
		j.last.AgentFamily != req.AgentFamily ||
		!j.last.IsSubagent {
		t.Errorf("judge received %+v, want decoded copy of %+v", j.last, req)
	}
}

func TestServeHTTP_JudgeError(t *testing.T) {
	j := &stubJudge{err: errors.New("upstream down")}
	h := NewHandler(&stubVerifier{}, j, time.Minute, discardLogger())
	rr := post(t, h, trustedHeaders(), requestBody(t, contractsadvise.Request{ConversationID: "conv-err"}))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestServeHTTP_Cache(t *testing.T) {
	j := &stubJudge{verdict: contractsadvise.Verdict{Switch: true, Model: "cheap-candidate-a", Reason: "trivial"}}
	const ttl = time.Minute
	h := NewHandler(&stubVerifier{}, j, ttl, discardLogger())

	// Pin the clock so TTL expiry is deterministic (ServeHTTP is synchronous,
	// so the handler reads `current` on the test goroutine only).
	current := time.Unix(1_700_000_000, 0)
	h.now = func() time.Time { return current }

	req := contractsadvise.Request{
		ConversationID:   "conv-a",
		AgentFamily:      "claude-code",
		FirstUserMessage: "Summarize README.",
		ToolNames:        []string{"Read"},
	}

	// First request classifies.
	rr := post(t, h, trustedHeaders(), requestBody(t, req))
	if rr.Code != http.StatusOK {
		t.Fatalf("first: status = %d", rr.Code)
	}
	if j.calls != 1 {
		t.Fatalf("first: judge calls = %d, want 1", j.calls)
	}
	firstBody := rr.Body.String()

	// Identical template hits the cache — same verdict, no new judge call.
	// ConversationID is not part of the template hash.
	req2 := req
	req2.ConversationID = "conv-b"
	rr = post(t, h, trustedHeaders(), requestBody(t, req2))
	if rr.Code != http.StatusOK {
		t.Fatalf("cached: status = %d", rr.Code)
	}
	if j.calls != 1 {
		t.Errorf("cached: judge calls = %d, want 1", j.calls)
	}
	if rr.Body.String() != firstBody {
		t.Errorf("cached verdict = %s, want %s", rr.Body.String(), firstBody)
	}

	// Different task text misses the cache.
	req3 := req
	req3.FirstUserMessage = "Refactor the resilience engine."
	if rr = post(t, h, trustedHeaders(), requestBody(t, req3)); rr.Code != http.StatusOK {
		t.Fatalf("different task: status = %d", rr.Code)
	}
	if j.calls != 2 {
		t.Errorf("different task: judge calls = %d, want 2", j.calls)
	}

	// Past the TTL the original entry has expired — the judge runs again.
	current = current.Add(ttl + time.Second)
	if rr = post(t, h, trustedHeaders(), requestBody(t, req)); rr.Code != http.StatusOK {
		t.Fatalf("expired: status = %d", rr.Code)
	}
	if j.calls != 3 {
		t.Errorf("expired: judge calls = %d, want 3", j.calls)
	}
}
