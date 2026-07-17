//go:build e2e

// Package agentroute_test proves the agent-aware routing feature end-to-end
// through the real gateway binary: a Claude Code subagent conversation
// (identified purely from headers) on the messages protocol with tools fires
// exactly one async HMAC-signed advisory POST at its first request, the
// verdict is validated against the configuration's allow_models list, and an
// accepted verdict pins the conversation so subsequent requests have their
// body model rewritten before forwarding. The test plays Arbiter via an
// in-process httptest advisor stub.
package agentroute_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// Synthetic model names (never real ones — negative probes on real names
// break when the policy library grows a matching rule).
const (
	defaultModel    = "e2e-default-model"
	cheapModel      = "e2e-cheap-model"
	notAllowedModel = "e2e-not-allowed"

	// haikuCheapModel is synthetic but carries the claude-haiku prefix the
	// gateway's hardcoded beta-reconciliation predicate matches — pinning to
	// it must strip context-1m-* tokens from Anthropic-Beta.
	haikuCheapModel = "claude-haiku-e2e-cheap"

	// betaHeader is the Anthropic beta opt-in header a Claude Code client
	// forwards; the context-1m token is the one a haiku pin cannot serve.
	betaHeader           = "Anthropic-Beta"
	betaWithContext1M    = "claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07,interleaved-thinking-2025-05-14"
	betaWithoutContext1M = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14"

	gatewayID  = "e2e-gw"
	hmacSecret = "e2e-agentroute-hmac-secret" //nolint:gosec // test fixture, not a credential

	claudeCLIUserAgent = "claude-cli/2.1.209 (external, sdk-cli)"

	headerAgentID   = "X-Claude-Code-Agent-Id"
	headerSessionID = "X-Claude-Code-Session-Id"

	headerGatewayID = "X-Slipspace-Gateway-Id"
	headerSignature = "X-Slipspace-Signature"
)

// agentRoutePolicy is the dev configuration the harness's hardcoded API key
// resolves to, with an agent_routing block and a top-level advisors block.
// The messages binding matches "e2e-*" so BOTH the client-sent default model
// and the pinned cheap model resolve to the same provider — a pin rewrite
// must still route.
//
// Placeholders: %s advisor endpoint, %s hmac_secret_file path.
const agentRoutePolicy = `
configurations:
  dev:
    credentials:
      anthropic: sk-ant-dev-mock

    bindings:
      - { protocol: messages, models: ["e2e-*", "claude-haiku-e2e-*"], provider: anthropic }

    agent_routing:
      advisor: arbiter
      allow_models: ["e2e-cheap-model", "claude-haiku-e2e-cheap"]
      apply_window_requests: 3

    tags:
      tier: dev

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

advisors:
  arbiter:
    endpoint: %s
    hmac_secret_file: %s
    gateway_id: e2e-gw
    timeout_ms: 2000
`

// advisorCall is one advisory POST the stub observed.
type advisorCall struct {
	// body is the raw request body the gateway signed.
	body []byte

	// gatewayID is the X-Slipspace-Gateway-Id header value.
	gatewayID string

	// sigOK reports whether X-Slipspace-Signature verified as the hex
	// HMAC-SHA256 of body under the shared secret.
	sigOK bool
}

// advisorStub is the in-test Arbiter: an httptest.Server that records each
// advisory request, verifies its HMAC, and answers with a fixed verdict.
type advisorStub struct {
	mu      sync.Mutex
	calls   []advisorCall
	verdict string

	srv *httptest.Server
}

// newAdvisorStub starts the stub returning verdictJSON on every call and
// registers cleanup.
func newAdvisorStub(t *testing.T, verdictJSON string) *advisorStub {
	t.Helper()
	s := &advisorStub{verdict: verdictJSON}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}

		mac := hmac.New(sha256.New, []byte(hmacSecret))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))

		s.mu.Lock()
		s.calls = append(s.calls, advisorCall{
			body:      body,
			gatewayID: r.Header.Get(headerGatewayID),
			sigOK:     hmac.Equal([]byte(want), []byte(r.Header.Get(headerSignature))),
		})
		verdict := s.verdict
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(verdict))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// URL returns the stub's base URL — the advisor endpoint in the policy.
func (s *advisorStub) URL() string { return s.srv.URL }

// Calls returns a snapshot of the observed advisory calls.
func (s *advisorStub) Calls() []advisorCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]advisorCall, len(s.calls))
	copy(out, s.calls)
	return out
}

// CallCount returns how many advisory calls arrived so far.
func (s *advisorStub) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// newAgentRouteHarness writes the HMAC secret to a temp file, starts the
// advisor stub with the given verdict, and boots a gateway whose dev
// configuration carries the agent_routing policy pointed at the stub.
func newAgentRouteHarness(t *testing.T, verdictJSON string) (*harness.Harness, *advisorStub) {
	t.Helper()

	stub := newAdvisorStub(t, verdictJSON)

	secretPath := filepath.Join(t.TempDir(), "advisor-hmac-secret")
	if err := os.WriteFile(secretPath, []byte(hmacSecret), 0o600); err != nil {
		t.Fatalf("write hmac secret file: %v", err)
	}

	h := harness.NewWithOptions(t, harness.Options{
		PolicyYAML: fmt.Sprintf(agentRoutePolicy, stub.URL(), secretPath),
	})
	return h, stub
}

// stageMessagesResponse stages an unlimited-use canned Anthropic messages
// response on the mock upstream.
func stageMessagesResponse(h *harness.Harness) {
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Status: http.StatusOK,
		Body:   `{"id":"msg_agentroute","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"e2e-default-model","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
	})
}

// messagesBody builds a minimal messages request. withTools controls whether
// the tools array (the birth-capture eligibility gate) is present.
func messagesBody(withTools bool) map[string]any {
	body := map[string]any{
		"model":      defaultModel,
		"max_tokens": 64,
		"messages":   []map[string]string{{"role": "user", "content": "summarise the readme"}},
	}
	if withTools {
		body["tools"] = []map[string]any{
			{"name": "Read", "input_schema": map[string]any{"type": "object"}},
		}
	}
	return body
}

// subagentHeaders builds the Claude Code subagent header set for one
// conversation. Empty agentID omits the subagent discriminator header.
func subagentHeaders(agentID string) http.Header {
	headers := http.Header{}
	headers.Set("User-Agent", claudeCLIUserAgent)
	headers.Set(headerSessionID, "e2e-sess-1")
	if agentID != "" {
		headers.Set(headerAgentID, agentID)
	}
	return headers
}

// assertMessagesOK fails the test unless resp is a 200 whose body parses as
// an Anthropic message.
func assertMessagesOK(t *testing.T, resp *harness.Response, label string) {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status=%d body=%s", label, resp.StatusCode, resp.Body)
	}
	var msg struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Role string `json:"role"`
	}
	if err := resp.JSONUnmarshal(&msg); err != nil {
		t.Fatalf("%s: response is not JSON: %v (body=%s)", label, err, resp.Body)
	}
	if msg.Type != "message" || msg.Role != "assistant" || msg.ID == "" {
		t.Fatalf("%s: not a well-formed messages response: %s", label, resp.Body)
	}
}

// capturedModel extracts the model field from an upstream-captured body.
func capturedModel(t *testing.T, c harness.CapturedRequest) string {
	t.Helper()
	var body struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(c.Body), &body); err != nil {
		t.Fatalf("captured body is not JSON: %v (body=%s)", err, c.Body)
	}
	return body.Model
}

// waitForAdvisorCalls polls until the stub has observed at least want calls.
func waitForAdvisorCalls(t *testing.T, stub *advisorStub, want int, deadline time.Duration) {
	t.Helper()
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		if stub.CallCount() >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("advisor stub received %d calls, want >= %d within %s", stub.CallCount(), want, deadline)
}

// TestAgentRoute_PinLifecycle covers the full happy path: request 1 forwards
// with the original model and fires exactly one signed advisory call; once
// the {switch:true, model:e2e-cheap-model} verdict lands, subsequent requests
// of the same conversation are rewritten to the pinned model.
func TestAgentRoute_PinLifecycle(t *testing.T) {
	t.Parallel()
	h, stub := newAgentRouteHarness(t,
		`{"switch":true,"model":"e2e-cheap-model","reason":"trivial","confidence":0.9}`)
	stageMessagesResponse(h)

	headers := subagentHeaders("e2e-conv-1")

	// Request 1: always forwards with the original model (the verdict is
	// async and cannot have landed yet).
	resp1 := h.PostJSON("/v1/messages", messagesBody(true), headers)
	assertMessagesOK(t, resp1, "request 1")

	captured := h.FetchCapturedRequests()
	if len(captured) != 1 {
		t.Fatalf("upstream captured %d requests after request 1, want 1", len(captured))
	}
	if got := capturedModel(t, captured[0]); got != defaultModel {
		t.Fatalf("request 1 upstream model = %q, want %q (first request must forward as sent)", got, defaultModel)
	}

	// The advisory call is async — wait for it to arrive before issuing
	// request 2, so the verdict lands while the conversation is still
	// within the apply window.
	waitForAdvisorCalls(t, stub, 1, 5*time.Second)

	calls := stub.Calls()
	if len(calls) != 1 {
		t.Fatalf("advisor stub got %d calls, want exactly 1", len(calls))
	}
	call := calls[0]
	if !call.sigOK {
		t.Errorf("advisory HMAC signature did not verify against the shared secret")
	}
	if call.gatewayID != gatewayID {
		t.Errorf("advisory %s = %q, want %q", headerGatewayID, call.gatewayID, gatewayID)
	}

	var areq struct {
		Configuration  string   `json:"configuration"`
		Protocol       string   `json:"protocol"`
		Model          string   `json:"model"`
		ConversationID string   `json:"conversation_id"`
		AgentFamily    string   `json:"agent_family"`
		IsSubagent     bool     `json:"is_subagent"`
		ToolNames      []string `json:"tool_names"`
	}
	if err := json.Unmarshal(call.body, &areq); err != nil {
		t.Fatalf("advisory body is not JSON: %v (body=%s)", err, call.body)
	}
	if areq.ConversationID != "e2e-conv-1" {
		t.Errorf("advisory conversation_id = %q, want e2e-conv-1", areq.ConversationID)
	}
	if areq.AgentFamily != "claude-code" {
		t.Errorf("advisory agent_family = %q, want claude-code", areq.AgentFamily)
	}
	if !areq.IsSubagent {
		t.Errorf("advisory is_subagent = false, want true")
	}
	if len(areq.ToolNames) != 1 || areq.ToolNames[0] != "Read" {
		t.Errorf(`advisory tool_names = %v, want ["Read"]`, areq.ToolNames)
	}
	if areq.Configuration != "dev" {
		t.Errorf("advisory configuration = %q, want dev", areq.Configuration)
	}
	if areq.Protocol != "messages" {
		t.Errorf("advisory protocol = %q, want messages", areq.Protocol)
	}
	if areq.Model != defaultModel {
		t.Errorf("advisory model = %q, want %q", areq.Model, defaultModel)
	}

	// Verdict application is also async relative to the stub's response —
	// poll request 2 (same conversation) until the upstream sees the
	// pinned model.
	deadline := time.Now().Add(5 * time.Second)
	pinnedSeen := false
	for time.Now().Before(deadline) {
		resp2 := h.PostJSON("/v1/messages", messagesBody(true), headers)
		assertMessagesOK(t, resp2, "request 2")
		last := h.LastCapturedRequest()
		if last == nil {
			t.Fatal("no upstream capture for request 2")
		}
		if capturedModel(t, *last) == cheapModel {
			pinnedSeen = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !pinnedSeen {
		t.Fatalf("upstream never received the pinned model %q; captured models: %v",
			cheapModel, allCapturedModels(t, h))
	}

	// The pin came from ONE classification: no additional advisory calls.
	if n := stub.CallCount(); n != 1 {
		t.Errorf("advisor stub got %d calls total, want exactly 1", n)
	}
}

// TestAgentRoute_AllowlistRejection covers the enforcement point: a verdict
// whose model is not in allow_models is discarded, the conversation keeps its
// original model, and no re-classification fires (the verdict decided the
// conversation).
func TestAgentRoute_AllowlistRejection(t *testing.T) {
	t.Parallel()
	h, stub := newAgentRouteHarness(t,
		`{"switch":true,"model":"e2e-not-allowed","reason":"trivial","confidence":0.9}`)
	stageMessagesResponse(h)

	headers := subagentHeaders("e2e-conv-2")

	resp := h.PostJSON("/v1/messages", messagesBody(true), headers)
	assertMessagesOK(t, resp, "request 1")

	waitForAdvisorCalls(t, stub, 1, 5*time.Second)

	// Keep the conversation active briefly; every forwarded request must
	// retain the original model — the disallowed verdict never pins.
	stop := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(stop) {
		followUp := h.PostJSON("/v1/messages", messagesBody(true), headers)
		assertMessagesOK(t, followUp, "follow-up")
		time.Sleep(150 * time.Millisecond)
	}

	for i, c := range h.FetchCapturedRequests() {
		if got := capturedModel(t, c); got != defaultModel {
			t.Errorf("upstream request %d model = %q, want %q (disallowed verdict must not pin)", i, got, defaultModel)
		}
	}
	if n := stub.CallCount(); n != 1 {
		t.Errorf("advisor stub got %d calls, want exactly 1 (rejection still decides the conversation)", n)
	}
}

// TestAgentRoute_Ineligible covers the birth-capture gates: a claude-cli
// request without the agent-id header (not a subagent) and a subagent request
// without tools (sidecar/utility call) must fire zero advisory calls and
// forward unchanged.
func TestAgentRoute_Ineligible(t *testing.T) {
	t.Parallel()
	h, stub := newAgentRouteHarness(t,
		`{"switch":true,"model":"e2e-cheap-model","reason":"trivial","confidence":0.9}`)
	stageMessagesResponse(h)

	// (a) claude-cli UA, tools present, but NO X-Claude-Code-Agent-Id.
	respA := h.PostJSON("/v1/messages", messagesBody(true), subagentHeaders(""))
	assertMessagesOK(t, respA, "no-agent-id request")

	// (b) subagent headers present but NO tools.
	respB := h.PostJSON("/v1/messages", messagesBody(false), subagentHeaders("e2e-conv-3"))
	assertMessagesOK(t, respB, "no-tools request")

	// The advisory call is async, so give a straggler time to surface
	// before asserting silence.
	assertNoAdvisorCalls(t, stub, 1*time.Second)

	captured := h.FetchCapturedRequests()
	if len(captured) != 2 {
		t.Fatalf("upstream captured %d requests, want 2", len(captured))
	}
	for i, c := range captured {
		if got := capturedModel(t, c); got != defaultModel {
			t.Errorf("upstream request %d model = %q, want %q (ineligible requests must forward unchanged)", i, got, defaultModel)
		}
	}
}

// TestAgentRoute_PlainSDKUserAgent covers total header absence: a vanilla SDK
// user agent with tools is not a recognised agent and must never trigger an
// advisory call.
func TestAgentRoute_PlainSDKUserAgent(t *testing.T) {
	t.Parallel()
	h, stub := newAgentRouteHarness(t,
		`{"switch":true,"model":"e2e-cheap-model","reason":"trivial","confidence":0.9}`)
	stageMessagesResponse(h)

	headers := http.Header{}
	headers.Set("User-Agent", "python-httpx/0.27.0")

	resp := h.PostJSON("/v1/messages", messagesBody(true), headers)
	assertMessagesOK(t, resp, "plain SDK request")

	assertNoAdvisorCalls(t, stub, 1*time.Second)

	last := h.LastCapturedRequest()
	if last == nil {
		t.Fatal("no upstream capture")
	}
	if got := capturedModel(t, *last); got != defaultModel {
		t.Errorf("upstream model = %q, want %q", got, defaultModel)
	}
}

// TestAgentRoute_HaikuPinStripsContext1MBeta covers beta reconciliation on a
// model substitution: a subagent request opting in to the 1M long-context
// beta forwards the header verbatim while unpinned, but once the
// conversation is pinned to a haiku-family model the context-1m token is
// stripped (the upstream rejects it on haiku with a 400) while every other
// beta token survives in order.
func TestAgentRoute_HaikuPinStripsContext1MBeta(t *testing.T) {
	t.Parallel()
	h, stub := newAgentRouteHarness(t,
		fmt.Sprintf(`{"switch":true,"model":%q,"reason":"trivial","confidence":0.9}`, haikuCheapModel))
	stageMessagesResponse(h)

	headers := subagentHeaders("e2e-conv-beta-1")
	headers.Set(betaHeader, betaWithContext1M)

	// Request 1 is always unpinned: the opt-in must reach upstream verbatim.
	resp1 := h.PostJSON("/v1/messages", messagesBody(true), headers)
	assertMessagesOK(t, resp1, "request 1")
	first := h.LastCapturedRequest()
	if first == nil {
		t.Fatal("no upstream capture for request 1")
	}
	if got := first.Headers[betaHeader]; got != betaWithContext1M {
		t.Fatalf("request 1 upstream %s = %q, want verbatim %q", betaHeader, got, betaWithContext1M)
	}

	waitForAdvisorCalls(t, stub, 1, 5*time.Second)

	// Poll same-conversation requests until the pin lands, then assert the
	// upstream-received header on the pinned request.
	deadline := time.Now().Add(5 * time.Second)
	var pinnedCapture *harness.CapturedRequest
	for time.Now().Before(deadline) {
		resp := h.PostJSON("/v1/messages", messagesBody(true), headers)
		assertMessagesOK(t, resp, "pinned request")
		last := h.LastCapturedRequest()
		if last == nil {
			t.Fatal("no upstream capture for pinned request")
		}
		if capturedModel(t, *last) == haikuCheapModel {
			pinnedCapture = last
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pinnedCapture == nil {
		t.Fatalf("upstream never received the pinned model %q; captured models: %v",
			haikuCheapModel, allCapturedModels(t, h))
	}
	if got := pinnedCapture.Headers[betaHeader]; got != betaWithoutContext1M {
		t.Fatalf("pinned request upstream %s = %q, want %q (context-1m stripped, other tokens intact)",
			betaHeader, got, betaWithoutContext1M)
	}
}

// TestAgentRoute_NonHaikuPinKeepsBetaHeader is the counter-case: a pin to a
// non-haiku model must not touch the client's beta opt-ins at all.
func TestAgentRoute_NonHaikuPinKeepsBetaHeader(t *testing.T) {
	t.Parallel()
	h, stub := newAgentRouteHarness(t,
		`{"switch":true,"model":"e2e-cheap-model","reason":"trivial","confidence":0.9}`)
	stageMessagesResponse(h)

	headers := subagentHeaders("e2e-conv-beta-2")
	headers.Set(betaHeader, betaWithContext1M)

	resp1 := h.PostJSON("/v1/messages", messagesBody(true), headers)
	assertMessagesOK(t, resp1, "request 1")

	waitForAdvisorCalls(t, stub, 1, 5*time.Second)

	deadline := time.Now().Add(5 * time.Second)
	var pinnedCapture *harness.CapturedRequest
	for time.Now().Before(deadline) {
		resp := h.PostJSON("/v1/messages", messagesBody(true), headers)
		assertMessagesOK(t, resp, "pinned request")
		last := h.LastCapturedRequest()
		if last == nil {
			t.Fatal("no upstream capture for pinned request")
		}
		if capturedModel(t, *last) == cheapModel {
			pinnedCapture = last
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pinnedCapture == nil {
		t.Fatalf("upstream never received the pinned model %q; captured models: %v",
			cheapModel, allCapturedModels(t, h))
	}
	if got := pinnedCapture.Headers[betaHeader]; got != betaWithContext1M {
		t.Fatalf("pinned request upstream %s = %q, want verbatim %q (non-haiku pin must not touch betas)",
			betaHeader, got, betaWithContext1M)
	}
}

// assertNoAdvisorCalls watches the stub for window and fails if any advisory
// call arrives — the async-channel mirror of ExpectNoEvent.
func assertNoAdvisorCalls(t *testing.T, stub *advisorStub, window time.Duration) {
	t.Helper()
	stop := time.Now().Add(window)
	for time.Now().Before(stop) {
		if n := stub.CallCount(); n != 0 {
			t.Fatalf("advisor stub received %d calls, want 0", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// allCapturedModels lists every model the upstream saw — diagnostic output
// for pin-never-applied failures.
func allCapturedModels(t *testing.T, h *harness.Harness) []string {
	t.Helper()
	captured := h.FetchCapturedRequests()
	models := make([]string, 0, len(captured))
	for _, c := range captured {
		models = append(models, capturedModel(t, c))
	}
	return models
}

// messagesBodyWithEffort is messagesBody plus the adaptive-thinking
// output_config a fable/opus Claude Code client sends with every request.
func messagesBodyWithEffort(extra map[string]any) map[string]any {
	body := messagesBody(true)
	oc := map[string]any{"effort": "high"}
	for k, v := range extra {
		oc[k] = v
	}
	body["output_config"] = oc
	return body
}

// capturedOutputConfig returns the raw output_config object the upstream
// received, or nil when the key is absent.
func capturedOutputConfig(t *testing.T, c harness.CapturedRequest) map[string]any {
	t.Helper()
	var body struct {
		OutputConfig map[string]any `json:"output_config"`
	}
	if err := json.Unmarshal([]byte(c.Body), &body); err != nil {
		t.Fatalf("captured body is not JSON: %v (body=%s)", err, c.Body)
	}
	return body.OutputConfig
}

// pinnedCaptureFor polls same-conversation requests until the upstream sees
// the pinned model, returning that capture.
func pinnedCaptureFor(t *testing.T, h *harness.Harness, body map[string]any, headers http.Header, wantModel string) harness.CapturedRequest {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp := h.PostJSON("/v1/messages", body, headers)
		assertMessagesOK(t, resp, "pinned request")
		last := h.LastCapturedRequest()
		if last == nil {
			t.Fatal("no upstream capture for pinned request")
		}
		if capturedModel(t, *last) == wantModel {
			return *last
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("upstream never received the pinned model %q; captured models: %v",
		wantModel, allCapturedModels(t, h))
	return harness.CapturedRequest{}
}

// TestAgentRoute_HaikuPinStripsEffort is the body-side sibling of the
// context-1m beta case: haiku rejects output_config.effort, so a haiku pin
// must strip it before forwarding. When effort is the only output_config
// content the whole block disappears rather than leaving {} behind.
func TestAgentRoute_HaikuPinStripsEffort(t *testing.T) {
	t.Parallel()
	h, stub := newAgentRouteHarness(t,
		fmt.Sprintf(`{"switch":true,"model":%q,"reason":"trivial","confidence":0.9}`, haikuCheapModel))
	stageMessagesResponse(h)

	headers := subagentHeaders("e2e-conv-effort-1")
	body := messagesBodyWithEffort(nil)

	// Request 1 is always unpinned: effort must reach upstream verbatim.
	resp1 := h.PostJSON("/v1/messages", body, headers)
	assertMessagesOK(t, resp1, "request 1")
	first := h.LastCapturedRequest()
	if first == nil {
		t.Fatal("no upstream capture for request 1")
	}
	if oc := capturedOutputConfig(t, *first); oc == nil || oc["effort"] != "high" {
		t.Fatalf("request 1 upstream output_config = %v, want effort high verbatim", oc)
	}

	waitForAdvisorCalls(t, stub, 1, 5*time.Second)

	pinned := pinnedCaptureFor(t, h, body, headers, haikuCheapModel)
	if oc := capturedOutputConfig(t, pinned); oc != nil {
		t.Fatalf("pinned request upstream output_config = %v, want the effort-only block removed entirely", oc)
	}
}

// TestAgentRoute_HaikuPinKeepsFormatWhenStrippingEffort proves the strip is
// surgical: structured-output format survives a haiku pin, only effort goes.
func TestAgentRoute_HaikuPinKeepsFormatWhenStrippingEffort(t *testing.T) {
	t.Parallel()
	h, stub := newAgentRouteHarness(t,
		fmt.Sprintf(`{"switch":true,"model":%q,"reason":"trivial","confidence":0.9}`, haikuCheapModel))
	stageMessagesResponse(h)

	headers := subagentHeaders("e2e-conv-effort-2")
	body := messagesBodyWithEffort(map[string]any{
		"format": map[string]any{"type": "json_schema", "schema": map[string]any{"type": "object"}},
	})

	resp1 := h.PostJSON("/v1/messages", body, headers)
	assertMessagesOK(t, resp1, "request 1")

	waitForAdvisorCalls(t, stub, 1, 5*time.Second)

	pinned := pinnedCaptureFor(t, h, body, headers, haikuCheapModel)
	oc := capturedOutputConfig(t, pinned)
	if oc == nil {
		t.Fatal("pinned request upstream lost output_config entirely; format must survive")
	}
	if _, hasEffort := oc["effort"]; hasEffort {
		t.Fatalf("pinned request upstream output_config = %v, want effort stripped", oc)
	}
	if _, hasFormat := oc["format"]; !hasFormat {
		t.Fatalf("pinned request upstream output_config = %v, want format intact", oc)
	}
}

// TestAgentRoute_NonHaikuPinKeepsEffort is the counter-case: a pin to a
// non-haiku model must forward the client's output_config untouched.
func TestAgentRoute_NonHaikuPinKeepsEffort(t *testing.T) {
	t.Parallel()
	h, stub := newAgentRouteHarness(t,
		`{"switch":true,"model":"e2e-cheap-model","reason":"trivial","confidence":0.9}`)
	stageMessagesResponse(h)

	headers := subagentHeaders("e2e-conv-effort-3")
	body := messagesBodyWithEffort(nil)

	resp1 := h.PostJSON("/v1/messages", body, headers)
	assertMessagesOK(t, resp1, "request 1")

	waitForAdvisorCalls(t, stub, 1, 5*time.Second)

	pinned := pinnedCaptureFor(t, h, body, headers, cheapModel)
	if oc := capturedOutputConfig(t, pinned); oc == nil || oc["effort"] != "high" {
		t.Fatalf("pinned request upstream output_config = %v, want effort high verbatim (non-haiku pin must not touch the body)", oc)
	}
}
