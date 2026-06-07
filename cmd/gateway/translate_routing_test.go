package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/httperr"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	resiliencemw "github.com/andyjmorgan/sluice-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
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

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
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
	forwarder := proxy.New(proxy.Options{Logger: logger, ObserverFactory: reporter.Factory()})
	evaluator := rules.NewEvaluator(store, 8, meters)
	errs := httperr.New(meters.ErrorResponsesTotal, logger)
	dataPlane := buildDataPlaneHandler(resolver, forwarder, evaluator, reporter.Factory(), store, resiliencemw.NewInMemoryBreakerStore(nil), meters, errs, nil, logger)
	root := correlationMiddleware(logger, observability.NewSessionResolver(nil), observability.NewAgentResolver(nil), observability.NewUserResolver(nil), nil, dataPlane)

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
