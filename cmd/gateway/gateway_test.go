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
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/httperr"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	resiliencemw "github.com/andyjmorgan/sluice-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
)

type capturedUpstream struct {
	mu      sync.Mutex
	method  string
	path    string
	headers http.Header
	body    []byte
	count   int
}

func (c *capturedUpstream) snapshot() (string, string, http.Header, []byte, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := c.headers.Clone()
	return c.method, c.path, h, append([]byte(nil), c.body...), c.count
}

type testEnv struct {
	gatewayURL  string
	upstreamURL string
	upstream    *capturedUpstream
	shutdown    func()
}

func newTestEnv(t *testing.T) *testEnv {
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

	dir := writeTestConfig(t, upstream.URL)

	ctx, cancel := context.WithCancel(context.Background())
	resolved, err := config.LoadV2(ctx, dir)
	if err != nil {
		upstream.Close()
		cancel()
		t.Fatalf("config.LoadV2: %v", err)
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
	reporter := newReporterFactory(nil, nil, logger, meters, nil, nil, nil, nil, false, testDefaultCaps())
	forwarder := proxy.New(proxy.Options{Logger: logger, ObserverFactory: reporter.Factory()})
	evaluator := rules.NewEvaluator(store, 8, meters)

	errs := httperr.New(meters.ErrorResponsesTotal, logger)
	dataPlane := buildDataPlaneHandler(resolver, forwarder, evaluator, reporter.Factory(), store, resiliencemw.NewInMemoryBreakerStore(nil), meters, errs, nil, logger)
	root := correlationMiddleware(logger, observability.NewSessionResolver(nil), nil, dataPlane)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/", root)

	gateway := httptest.NewServer(mux)

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

func writeTestConfig(t *testing.T, upstreamURL string) string {
	t.Helper()
	dir := t.TempDir()

	backendsYAML := fmt.Sprintf(`backends:
  openai:
    base_url: %s
    protocols:
      chat:
        path: /v1/chat/completions
        auth: { header: Authorization, format: "Bearer {key}" }
  anthropic:
    base_url: %s
    required_headers:
      anthropic-version: "2023-06-01"
    protocols:
      messages:
        path: /v1/messages
        auth: { header: x-api-key, format: "{key}" }
      chat:
        path: /v1/chat/completions
        auth: { header: Authorization, format: "Bearer {key}" }
    passthrough:
      models:
        paths:
          - { match: /v1/models, methods: [GET] }
  gemini:
    base_url: %s
    protocols:
      chat:
        path: /v1beta/openai/chat/completions
        auth: { header: Authorization, format: "Bearer {key}" }
`, upstreamURL, upstreamURL, upstreamURL)

	//nolint:gosec // test fixture keys; not real credentials
	policyYAML := `configurations:
  dev:
    credentials:
      openai: sk-upstream-openai
      anthropic: sk-upstream-anthropic
      gemini: gm-upstream-gemini
    bindings:
      - { protocol: chat, models: ["gpt-*"], backend: openai }
      - { protocol: chat, models: ["claude-*"], backend: anthropic }
      - { protocol: messages, models: ["claude-*"], backend: anthropic }
      - { protocol: chat, models: ["gemini-*"], backend: gemini }
    passthrough_bindings:
      - { family: models, backend: anthropic }

api_keys:
  - secret: sk_dev_local
    name: local
    configuration: dev
    enabled: true
  - secret: sk_dev_disabled
    name: disabled
    configuration: dev
    enabled: false
`

	for name, body := range map[string]string{
		"backends.yaml": backendsYAML,
		"policy.yaml":   policyYAML,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestGateway_ManagedHappyPath(t *testing.T) {
	env := newTestEnv(t)

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", body)
	req.Header.Set("Authorization", "Bearer sk_dev_local")
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if corr := resp.Header.Get("X-Sluice-Correlation-Id"); corr == "" {
		t.Errorf("missing X-Sluice-Correlation-Id header")
	}

	method, path, headers, got, _ := env.upstream.snapshot()
	if method != http.MethodPost {
		t.Errorf("upstream method = %q, want POST", method)
	}
	if path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", path)
	}
	if auth := headers.Get("Authorization"); auth != "Bearer sk-upstream-openai" {
		t.Errorf("upstream Authorization = %q, want managed swap", auth)
	}
	if string(got) != body {
		t.Errorf("upstream body mismatch: %q != %q", string(got), body)
	}
}

func TestGateway_MissingBearer(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", `{}`)
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if _, _, _, _, count := env.upstream.snapshot(); count != 0 {
		t.Errorf("upstream call count = %d, want 0", count)
	}
}

func TestGateway_UnknownBearer(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", `{}`)
	req.Header.Set("Authorization", "Bearer sk_does_not_exist")
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestGateway_DisabledBearer(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", `{}`)
	req.Header.Set("Authorization", "Bearer sk_dev_disabled")
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestGateway_UnknownPath(t *testing.T) {
	env := newTestEnv(t)

	// Auth runs before selection in v2, so an unknown path still needs a
	// valid credential to reach the 404 (no passthrough family claims it).
	req := newReq(t, http.MethodGet, env.gatewayURL+"/does/not/exist", "")
	req.Header.Set("Authorization", "Bearer sk_dev_local")
	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGateway_MethodNotAllowed(t *testing.T) {
	env := newTestEnv(t)

	// The models passthrough family accepts GET only; a POST to its path is
	// 405. Method enforcement now lives on passthrough families.
	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/models", `{}`)
	req.Header.Set("Authorization", "Bearer sk_dev_local")
	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestGateway_PassthroughMode(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", `{"model":"gpt-4o"}`)
	req.Header.Set("Authorization", "Bearer customer-supplied-token")
	req.Header.Set("X-Sluice-Configuration", "dev")
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	_, _, headers, _, _ := env.upstream.snapshot()
	if got := headers.Get("Authorization"); got != "Bearer customer-supplied-token" {
		t.Errorf("upstream Authorization = %q, want untouched", got)
	}
	if got := headers.Get("X-Sluice-Configuration"); got != "" {
		t.Errorf("upstream X-Sluice-Configuration = %q, want stripped", got)
	}
}

// TestGateway_IdentityPassthroughMode covers the unguessable replacement
// for X-Sluice-Configuration: the client supplies a Sluice api-key in
// X-Sluice-Identity to pick a policy, and an arbitrary upstream credential
// in Authorization that the gateway forwards verbatim. Both selector
// headers are stripped before the request reaches upstream.
func TestGateway_IdentityPassthroughMode(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", `{"model":"gpt-4o"}`)
	req.Header.Set("Authorization", "Bearer customer-supplied-token")
	req.Header.Set("X-Sluice-Identity", "sk_dev_local")
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	_, _, headers, _, _ := env.upstream.snapshot()
	if got := headers.Get("Authorization"); got != "Bearer customer-supplied-token" {
		t.Errorf("upstream Authorization = %q, want untouched", got)
	}
	if got := headers.Get("X-Sluice-Identity"); got != "" {
		t.Errorf("upstream X-Sluice-Identity = %q, want stripped", got)
	}
	if got := headers.Get("X-Sluice-Configuration"); got != "" {
		t.Errorf("upstream X-Sluice-Configuration = %q, want stripped (always)", got)
	}
}

func TestGateway_IdentityPassthroughMode_UnknownKey(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", `{}`)
	req.Header.Set("Authorization", "Bearer customer-supplied-token")
	req.Header.Set("X-Sluice-Identity", "sk_dev_does_not_exist")
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown identity must 401, got %d", resp.StatusCode)
	}
}

// TestGateway_AnthropicChatCompletions_OpenAICompatSurface exercises the v2
// model-keyed redirect: a chat request whose model is claude-* binds to the
// anthropic backend's chat protocol, swapping the upstream credential into
// Authorization: Bearer (not the native x-api-key).
func TestGateway_AnthropicChatCompletions_OpenAICompatSurface(t *testing.T) {
	env := newTestEnv(t)

	body := `{"model":"claude-haiku-4-5","messages":[{"role":"user","content":"hi"}]}`
	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", body)
	req.Header.Set("Authorization", "Bearer sk_dev_local")
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	_, path, headers, _, _ := env.upstream.snapshot()
	if path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions (prefix stripped)", path)
	}
	if got := headers.Get("Authorization"); got != "Bearer sk-upstream-anthropic" {
		t.Errorf("upstream Authorization = %q, want Bearer sk-upstream-anthropic", got)
	}
	if got := headers.Get("x-api-key"); got != "" {
		t.Errorf("upstream x-api-key = %q, want empty (override is Authorization)", got)
	}
}

// TestGateway_GeminiChatCompletions_OpenAICompatSurface mirrors the
// anthropic case for Gemini's distinct OpenAI-compat path.
func TestGateway_GeminiChatCompletions_OpenAICompatSurface(t *testing.T) {
	env := newTestEnv(t)

	body := `{"model":"gemini-2.0-flash-001","messages":[{"role":"user","content":"hi"}]}`
	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", body)
	req.Header.Set("Authorization", "Bearer sk_dev_local")
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	_, path, headers, _, _ := env.upstream.snapshot()
	if path != "/v1beta/openai/chat/completions" {
		t.Errorf("upstream path = %q, want /v1beta/openai/chat/completions (prefix stripped)", path)
	}
	if got := headers.Get("Authorization"); got != "Bearer gm-upstream-gemini" {
		t.Errorf("upstream Authorization = %q, want Bearer gm-upstream-gemini", got)
	}
	if got := headers.Get("x-goog-api-key"); got != "" {
		t.Errorf("upstream x-goog-api-key = %q, want empty (override is Authorization)", got)
	}
}

func TestGateway_AnthropicMessagesRouting(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/messages", `{"model":"claude-3-5-sonnet"}`)
	req.Header.Set("Authorization", "Bearer sk_dev_local")
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	_, path, headers, _, _ := env.upstream.snapshot()
	if path != "/v1/messages" {
		t.Errorf("upstream path = %q, want /v1/messages (prefix stripped)", path)
	}
	if got := headers.Get("x-api-key"); got != "sk-upstream-anthropic" {
		t.Errorf("upstream x-api-key = %q, want sk-upstream-anthropic", got)
	}
	if got := headers.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("upstream anthropic-version = %q, want 2023-06-01", got)
	}
	if got := headers.Get("Authorization"); got != "" {
		t.Errorf("upstream Authorization = %q, want dropped on anthropic", got)
	}
}

func TestGateway_CorrelationIDPropagated(t *testing.T) {
	env := newTestEnv(t)

	id := "test-correlation-7c3"
	req := newReq(t, http.MethodGet, env.gatewayURL+"/v1/models", "")
	req.Header.Set("Authorization", "Bearer sk_dev_local")
	req.Header.Set("X-Sluice-Correlation-Id", id)

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Sluice-Correlation-Id"); got != id {
		t.Errorf("X-Sluice-Correlation-Id = %q, want %q", got, id)
	}
}

func TestGateway_SessionIDEchoed(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodGet, env.gatewayURL+"/v1/models", "")
	req.Header.Set("Authorization", "Bearer sk_dev_local")
	req.Header.Set("X-Sluice-Session-Id", "sess-abc")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Sluice-Session-Id"); got != "sess-abc" {
		t.Errorf("X-Sluice-Session-Id = %q, want sess-abc", got)
	}
}

func TestGateway_MalformedBody(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", `{not-json`)
	req.Header.Set("Authorization", "Bearer sk_dev_local")
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestGateway_UnknownConfigurationPassthrough(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", `{}`)
	req.Header.Set("Authorization", "Bearer whatever")
	req.Header.Set("X-Sluice-Configuration", "does-not-exist")
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestGateway_OptionsTimeout(t *testing.T) {
	env := newTestEnv(t)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(env.gatewayURL + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	defer closeBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", resp.StatusCode)
	}
}

func newReq(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}

func doReq(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func closeBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_ = resp.Body.Close()
}
