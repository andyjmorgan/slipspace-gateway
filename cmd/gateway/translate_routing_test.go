package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/andyjmorgan/slipspace-gateway/internal/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/httperr"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/auth"
	resiliencemw "github.com/andyjmorgan/slipspace-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
	"github.com/andyjmorgan/slipspace-gateway/internal/proxy"
	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
	openaichat "github.com/andyjmorgan/slipspace-gateway/protocols/openai/chat"
)

// writeTranslateConfig writes a config whose `dev`/`bad` configurations bind the
// Anthropic Messages protocol but attach a translate rule. The anthropic
// provider also serves the chat protocol, so a translate->chat action resolves
// anthropic's chat endpoint (invariant #7: endpoint re-resolved on the provider
// for the post-rule protocol).
func writeTranslateConfig(t *testing.T, upstreamURL string) string {
	t.Helper()
	dir := t.TempDir()

	providersYAML := fmt.Sprintf(`providers:
  anthropic:
    base_url: %s
    protocols:
      messages:
        path: /v1/messages
        auth: { header: x-api-key, format: "{key}" }
      chat:
        path: /v1/chat/completions
        auth: { header: Authorization, format: "Bearer {key}" }
`, upstreamURL)

	//nolint:gosec // test fixture keys; not real credentials
	policyYAML := `configurations:
  xlate:
    credentials: { anthropic: sk-upstream-anthropic }
    bindings:
      - { protocol: messages, models: ["claude-*"], provider: anthropic }
    rule_names: [to-chat]
  xlatebad:
    credentials: { anthropic: sk-upstream-anthropic }
    bindings:
      - { protocol: messages, models: ["claude-*"], provider: anthropic }
    rule_names: [to-responses]

api_keys:
  - { secret: sk_xlate_local_only, name: xlate, configuration: xlate, enabled: true }
  - { secret: sk_bad_local_only, name: bad, configuration: xlatebad, enabled: true }

rules:
  - name: to-chat
    condition: { type: protocol, operator: Equals, expectedProtocol: messages }
    actions:
      - { type: translate, targetProtocol: chat }
  - name: to-responses
    condition: { type: protocol, operator: Equals, expectedProtocol: messages }
    actions:
      - { type: translate, targetProtocol: responses }
`

	for name, body := range map[string]string{"providers.yaml": providersYAML, "policy.yaml": policyYAML} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// newTranslateEnv builds the full data-plane handler against writeTranslateConfig
// and a path-recording upstream. Mirrors newTestEnv but with the translate
// config.
func newTranslateEnv(t *testing.T) *testEnv {
	t.Helper()

	cap := &capturedUpstream{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cap.mu.Lock()
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.headers = r.Header.Clone()
		cap.body = body
		cap.count++
		cap.mu.Unlock()

		// When the (translated) request asks for streaming, reply with an OpenAI
		// Chat SSE stream; otherwise a non-streaming OpenAI Chat response. The
		// response leg translates either back to Anthropic Messages shape.
		if bytes.Contains(body, []byte(`"stream":true`)) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fl, _ := w.(http.Flusher)
			for _, c := range []string{
				`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
				`{"choices":[{"index":0,"delta":{"content":"hi stream"}}]}`,
				`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				`[DONE]`,
			} {
				_, _ = w.Write([]byte("data: " + c + "\n\n"))
				if fl != nil {
					fl.Flush()
				}
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// A valid OpenAI Chat Completions response so the response leg can
		// translate it back to Anthropic Messages shape.
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hello from openai"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`))
	}))

	dir := writeTranslateConfig(t, upstream.URL)
	ctx, cancel := context.WithCancel(context.Background())
	resolved, err := config.Load(ctx, dir)
	if err != nil {
		upstream.Close()
		cancel()
		t.Fatalf("config.Load: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := config.NewStore(resolved)
	resolver := auth.NewResolver(store)
	meters, err := observability.NewMeters(noop.NewMeterProvider().Meter("test"))
	if err != nil {
		upstream.Close()
		cancel()
		t.Fatalf("NewMeters: %v", err)
	}
	reporter := newReporterFactory(nil, nil, logger, meters, nil, nil, nil, nil, false, testDefaultCaps(), nil)
	// Lossy header enabled in tests so drop reporting is observable.
	forwarder := proxy.New(proxy.Options{Logger: logger, ObserverFactory: reporter.Factory(), ResponseBodyTransform: newResponseBodyTransform(meters, "", true)})
	evaluator := rules.NewEvaluator(store, 8, meters)
	errs := httperr.New(meters.ErrorResponsesTotal, logger)
	dataPlane := buildDataPlaneHandler(resolver, forwarder, evaluator, reporter.Factory(), store, resiliencemw.NewInMemoryBreakerStore(nil), nil, meters, errs, nil, logger)
	root := correlationMiddleware(logger, observability.NewSessionResolver(nil), observability.NewConversationResolver(nil), observability.NewParentResolver(nil), observability.NewAgentResolver(nil), observability.NewUserResolver(nil), nil, dataPlane)

	gateway := httptest.NewServer(root)
	env := &testEnv{
		gatewayURL:  gateway.URL,
		upstreamURL: upstream.URL,
		upstream:    cap,
		shutdown: func() {
			gateway.Close()
			upstream.Close()
			cancel()
		},
	}
	t.Cleanup(env.shutdown)
	return env
}

func TestGateway_TranslateResolvesTargetEndpoint(t *testing.T) {
	env := newTranslateEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/messages", `{"model":"claude-x","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer sk_xlate_local_only")
	resp := doReq(t, req)
	defer closeBody(resp)

	// PR4 wires endpoint resolution only (no response translation yet), so the
	// upstream's 200 flows back to the client unchanged.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// translate->chat must re-resolve onto anthropic's chat endpoint, so the
	// upstream sees the request at the chat path, not the messages path.
	_, path, _, _, count := env.upstream.snapshot()
	if count != 1 {
		t.Fatalf("upstream hits = %d, want 1", count)
	}
	if path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions (translate->chat endpoint)", path)
	}
}

func TestGateway_TranslateRoundTrip(t *testing.T) {
	env := newTranslateEnv(t)

	// top_k has no OpenAI equivalent — it must be dropped and reported.
	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/messages", `{"model":"claude-x","max_tokens":16,"top_k":40,"messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer sk_xlate_local_only")
	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// The dropped feature is reported on the lossy header (enabled in tests).
	if lossy := resp.Header.Get("X-Slipspace-Translation-Lossy"); !strings.Contains(lossy, "top_k") {
		t.Errorf("X-Slipspace-Translation-Lossy = %q, want it to list top_k", lossy)
	}

	// The upstream must have received an OpenAI Chat request (translated from
	// the inbound Anthropic Messages body).
	_, path, _, upBody, _ := env.upstream.snapshot()
	if path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", path)
	}
	var chatReq openaichat.ChatCompletionRequest
	if err := json.Unmarshal(upBody, &chatReq); err != nil {
		t.Fatalf("upstream body is not an OpenAI chat request: %v\nbody: %s", err, upBody)
	}
	if len(chatReq.Messages) != 1 || chatReq.Messages[0].Role() != "user" {
		t.Errorf("upstream messages = %+v, want one user message", chatReq.Messages)
	}
	if chatReq.MaxTokens == nil || *chatReq.MaxTokens != 16 {
		t.Errorf("upstream max_tokens = %v, want 16", chatReq.MaxTokens)
	}

	// The client must have received an Anthropic Messages response (translated
	// back from the OpenAI Chat response).
	clientBody, _ := io.ReadAll(resp.Body)
	var msgResp messages.MessagesResponse
	if err := json.Unmarshal(clientBody, &msgResp); err != nil {
		t.Fatalf("client body is not an Anthropic Messages response: %v\nbody: %s", err, clientBody)
	}
	if msgResp.Type != "message" || msgResp.Role != "assistant" {
		t.Errorf("client envelope = %q/%q, want message/assistant", msgResp.Type, msgResp.Role)
	}
	if len(msgResp.Content) != 1 {
		t.Fatalf("client content blocks = %d, want 1", len(msgResp.Content))
	}
	tb, ok := msgResp.Content[0].(*messages.TextBlock)
	if !ok || tb.Text != "hello from openai" {
		t.Errorf("client content[0] = %+v, want TextBlock 'hello from openai'", msgResp.Content[0])
	}
	if msgResp.StopReason == nil || *msgResp.StopReason != "end_turn" {
		t.Errorf("client stop_reason = %v, want end_turn", msgResp.StopReason)
	}
	if msgResp.Usage.InputTokens != 5 || msgResp.Usage.OutputTokens != 3 {
		t.Errorf("client usage = %d/%d, want 5/3", msgResp.Usage.InputTokens, msgResp.Usage.OutputTokens)
	}
}

func TestGateway_TranslateStreamingRoundTrip(t *testing.T) {
	env := newTranslateEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/messages", `{"model":"claude-x","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer sk_xlate_local_only")
	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	// The upstream must have received a streaming OpenAI Chat request.
	_, path, _, upBody, _ := env.upstream.snapshot()
	if path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", path)
	}
	if !bytes.Contains(upBody, []byte(`"stream":true`)) {
		t.Errorf("upstream request not streaming: %s", upBody)
	}

	// The client must receive an Anthropic Messages SSE stream.
	body, _ := io.ReadAll(resp.Body)
	got := sseEventTypes(string(body))
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("client SSE event types = %v\nwant %v", got, want)
	}
	if !strings.Contains(string(body), `"text":"hi stream"`) {
		t.Errorf("client stream missing translated text delta:\n%s", body)
	}
}

// sseEventTypes extracts the ordered `event:` labels from an SSE body.
func sseEventTypes(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "event: ") {
			out = append(out, strings.TrimPrefix(line, "event: "))
		}
	}
	return out
}

func TestGateway_TranslateFailClosedNoTranslator(t *testing.T) {
	env := newTranslateEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/messages", `{"model":"claude-x","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer sk_bad_local_only")
	resp := doReq(t, req)
	defer closeBody(resp)

	// No messages->responses translator is registered, so the request must fail
	// closed (501) before any forward.
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501 for unsupported translation", resp.StatusCode)
	}
	if _, _, _, _, count := env.upstream.snapshot(); count != 0 {
		t.Errorf("upstream hits = %d, want 0 (fail closed before forward)", count)
	}
}
