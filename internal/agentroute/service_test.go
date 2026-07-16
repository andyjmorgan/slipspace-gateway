package agentroute

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/contracts/advise"
	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
)

// advisorStub is an httptest-backed advisory endpoint that counts calls,
// captures the decoded requests, and answers with a fixed verdict/status.
type advisorStub struct {
	srv   *httptest.Server
	calls atomic.Int64

	mu   sync.Mutex
	reqs []advise.Request

	status  int
	verdict advise.Verdict
}

func newAdvisorStub(t *testing.T, status int, verdict advise.Verdict) *advisorStub {
	t.Helper()
	s := &advisorStub{status: status, verdict: verdict}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		var areq advise.Request
		if err := json.NewDecoder(r.Body).Decode(&areq); err != nil {
			t.Errorf("advisor stub: decode request: %v", err)
		}
		s.mu.Lock()
		s.reqs = append(s.reqs, areq)
		s.mu.Unlock()

		if s.status != http.StatusOK {
			w.WriteHeader(s.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(s.verdict); err != nil {
			t.Errorf("advisor stub: encode verdict: %v", err)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *advisorStub) requests() []advise.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]advise.Request(nil), s.reqs...)
}

// newTestService builds a Service wired to the stub's endpoint under the
// advisor name "arbiter".
func newTestService(endpoint string) *Service {
	advisors := contractsconfig.AdvisorsConfig{
		"arbiter": contractsconfig.Advisor{ //nolint:gosec // test fixture, not a credential
			Endpoint:       endpoint,
			HMACSecretFile: "/dev/null", // unused: the secret is injected directly
			GatewayID:      "gw-test",
		},
	}
	secrets := map[string][]byte{"arbiter": []byte("test-secret")}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(advisors, secrets, logger)
}

// testAgentRouting returns a policy naming the stub advisor. The apply window
// is set high so Evaluate polling cannot outrun the async verdict.
func testAgentRouting() *contractsconfig.AgentRouting {
	return &contractsconfig.AgentRouting{
		Advisor:             "arbiter",
		AllowModels:         []string{"cheap-model-a"},
		ApplyWindowRequests: 1000,
	}
}

// subagentHeaders returns headers identifying a Claude Code subagent.
func subagentHeaders() http.Header {
	h := http.Header{}
	h.Set("User-Agent", "claude-cli/2.1.209 (external, sdk-cli)")
	h.Set("X-Claude-Code-Agent-Id", "agent-abc")
	return h
}

// subagentContext places the conversation + session ids Evaluate keys on.
func subagentContext(conversationID string) context.Context {
	ctx := observability.WithConversationID(context.Background(), conversationID, "X-Claude-Code-Agent-Id")
	return observability.WithSessionID(ctx, "sess-root-1", "X-Slipspace-Session-Id")
}

// subagentRequest builds a typed messages request with tools, one user turn,
// and a string system prompt.
func subagentRequest(t *testing.T) *messages.MessagesRequest {
	t.Helper()
	var msg messages.Message
	msg.Role = "user"
	if err := msg.SetContentString("Summarize the config loader"); err != nil {
		t.Fatalf("SetContentString: %v", err)
	}
	return &messages.MessagesRequest{
		MaxTokens: 1024,
		Messages:  []messages.Message{msg},
		System:    json.RawMessage(`"You are a focused subagent"`),
		Tools:     []messages.Tool{{Name: "Read"}},
	}
}

// evaluate invokes Evaluate on the messages protocol with the standard test
// coordinates.
func evaluate(ctx context.Context, svc *Service, ar *contractsconfig.AgentRouting, req *messages.MessagesRequest, h http.Header) *Pin {
	return svc.Evaluate(ctx, "cfg-test", ar, "messages", "anthropic", "big-model-x", req, h)
}

// pollUntil re-runs cond every few milliseconds until it returns true or 2s
// pass; reports whether cond succeeded.
func pollUntil(cond func() bool) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func TestService_Evaluate_PinLifecycle(t *testing.T) {
	stub := newAdvisorStub(t, http.StatusOK, advise.Verdict{Switch: true, Model: "cheap-model-a", Reason: "trivial"})
	svc := newTestService(stub.srv.URL)
	ar := testAgentRouting()
	ctx := subagentContext("conv-pin-1")
	req := subagentRequest(t)
	h := subagentHeaders()

	// First sighting: fires the advisory call async, request proceeds unpinned.
	if pin := evaluate(ctx, svc, ar, req, h); pin != nil {
		t.Fatalf("first Evaluate = %+v, want nil (verdict is async)", pin)
	}

	// The verdict lands out of band; subsequent requests pick up the pin.
	var pin *Pin
	ok := pollUntil(func() bool {
		pin = evaluate(ctx, svc, ar, req, h)
		return pin != nil
	})
	if !ok {
		t.Fatal("Evaluate never returned a pin within 2s")
	}
	if pin.Model != "cheap-model-a" {
		t.Errorf("pin.Model = %q, want cheap-model-a", pin.Model)
	}
	if pin.Reason != "trivial" {
		t.Errorf("pin.Reason = %q, want trivial", pin.Reason)
	}

	if n := stub.calls.Load(); n != 1 {
		t.Fatalf("advisor received %d calls, want exactly 1", n)
	}
	got := stub.requests()[0]
	want := advise.Request{
		Configuration:    "cfg-test",
		Protocol:         "messages",
		Provider:         "anthropic",
		Model:            "big-model-x",
		ConversationID:   "conv-pin-1",
		SessionID:        "sess-root-1",
		AgentFamily:      FamilyClaudeCode,
		Entrypoint:       "sdk-cli",
		IsSubagent:       true,
		SystemPrefix:     "You are a focused subagent",
		FirstUserMessage: "Summarize the config loader",
		ToolNames:        []string{"Read"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("advise request = %+v, want %+v", got, want)
	}
}

func TestService_Evaluate_AllowlistRejectsVerdict(t *testing.T) {
	stub := newAdvisorStub(t, http.StatusOK, advise.Verdict{Switch: true, Model: "not-allowed-model", Reason: "trivial"})
	svc := newTestService(stub.srv.URL)
	ar := testAgentRouting()
	ctx := subagentContext("conv-allow-1")
	req := subagentRequest(t)
	h := subagentHeaders()

	if pin := evaluate(ctx, svc, ar, req, h); pin != nil {
		t.Fatalf("first Evaluate = %+v, want nil", pin)
	}

	// Wait for the single advisory round-trip to land.
	if !pollUntil(func() bool { return stub.calls.Load() == 1 }) {
		t.Fatal("advisor never received the classification call")
	}

	// The out-of-allowlist verdict is decided as "continue": no pin ever
	// appears and no further advisory calls fire.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if pin := evaluate(ctx, svc, ar, req, h); pin != nil {
			t.Fatalf("Evaluate returned pin %+v for disallowed model, want nil", pin)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := stub.calls.Load(); n != 1 {
		t.Fatalf("advisor received %d calls, want exactly 1 (decided as continue)", n)
	}
}

func TestService_Evaluate_Ineligible(t *testing.T) {
	stub := newAdvisorStub(t, http.StatusOK, advise.Verdict{Switch: true, Model: "cheap-model-a"})
	svc := newTestService(stub.srv.URL)
	ar := testAgentRouting()

	noAgentID := subagentHeaders()
	noAgentID.Del("X-Claude-Code-Agent-Id")

	noTools := subagentRequest(t)
	noTools.Tools = nil

	tests := []struct {
		name     string
		ctx      context.Context
		ar       *contractsconfig.AgentRouting
		protocol string
		req      *messages.MessagesRequest
		headers  http.Header
	}{
		{
			name:     "not a subagent (no agent-id header)",
			ctx:      subagentContext("conv-inel-1"),
			ar:       ar,
			protocol: "messages",
			req:      subagentRequest(t),
			headers:  noAgentID,
		},
		{
			name:     "no tools declared",
			ctx:      subagentContext("conv-inel-2"),
			ar:       ar,
			protocol: "messages",
			req:      noTools,
			headers:  subagentHeaders(),
		},
		{
			name:     "wrong protocol",
			ctx:      subagentContext("conv-inel-3"),
			ar:       ar,
			protocol: "chat",
			req:      subagentRequest(t),
			headers:  subagentHeaders(),
		},
		{
			name:     "nil agent routing policy",
			ctx:      subagentContext("conv-inel-4"),
			ar:       nil,
			protocol: "messages",
			req:      subagentRequest(t),
			headers:  subagentHeaders(),
		},
		{
			name:     "no conversation id on context",
			ctx:      context.Background(),
			ar:       ar,
			protocol: "messages",
			req:      subagentRequest(t),
			headers:  subagentHeaders(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if pin := svc.Evaluate(tc.ctx, "cfg-test", tc.ar, tc.protocol, "anthropic", "big-model-x", tc.req, tc.headers); pin != nil {
				t.Fatalf("Evaluate = %+v, want nil", pin)
			}
		})
	}

	// Ineligible requests never open a register entry, so no advisory call
	// can be in flight; a short settle window catches any stray dispatch.
	time.Sleep(50 * time.Millisecond)
	if n := stub.calls.Load(); n != 0 {
		t.Fatalf("advisor received %d calls for ineligible requests, want 0", n)
	}
}

func TestService_Evaluate_AdvisorFailureRetries(t *testing.T) {
	stub := newAdvisorStub(t, http.StatusInternalServerError, advise.Verdict{})
	svc := newTestService(stub.srv.URL)
	ar := testAgentRouting()
	ctx := subagentContext("conv-retry-1")
	req := subagentRequest(t)
	h := subagentHeaders()

	// First sighting fires attempt one, which fails (500) and clears the entry.
	if pin := evaluate(ctx, svc, ar, req, h); pin != nil {
		t.Fatalf("first Evaluate = %+v, want nil", pin)
	}
	if !pollUntil(func() bool { return stub.calls.Load() >= 1 }) {
		t.Fatal("advisor never received the first attempt")
	}

	// Because Fail removed the entry, a later Evaluate fires a fresh attempt.
	ok := pollUntil(func() bool {
		if pin := evaluate(ctx, svc, ar, req, h); pin != nil {
			t.Fatalf("Evaluate returned pin %+v from a failing advisor, want nil", pin)
		}
		return stub.calls.Load() >= 2
	})
	if !ok {
		t.Fatalf("advisor calls = %d, want >= 2 (retry after Fail)", stub.calls.Load())
	}
}
