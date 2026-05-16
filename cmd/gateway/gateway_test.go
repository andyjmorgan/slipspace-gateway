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
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
	"github.com/andyjmorgan/sluice-gateway/internal/routing"
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
	resolved, err := config.Load(ctx, dir)
	if err != nil {
		upstream.Close()
		cancel()
		t.Fatalf("config.Load: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	router, err := routing.New(resolved)
	if err != nil {
		upstream.Close()
		cancel()
		t.Fatalf("routing.New: %v", err)
	}

	resolver := auth.NewResolver(resolved)
	meters, err := observability.NewMeters(noop.NewMeterProvider().Meter("test"))
	if err != nil {
		upstream.Close()
		cancel()
		t.Fatalf("NewMeters: %v", err)
	}
	reporter := newReporterFactory(nil, logger, meters)
	forwarder := proxy.New(proxy.Options{Logger: logger, ObserverFactory: reporter.Factory()})
	evaluator := rules.NewEvaluator(resolved.PerConfigurationRules, 8, meters)

	errs := httperr.New(meters.ErrorResponsesTotal, logger)
	dataPlane := buildDataPlaneHandler(router, resolver, forwarder, evaluator, reporter.Factory(), resolved.Providers, meters, errs, logger)
	root := correlationMiddleware(logger, dataPlane)

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

	providersYAML := fmt.Sprintf(`providers:
  openai:
    prefix: openai
    prefix_required: false
    base_url: %s
    endpoints:
      chat_completions:
        path: /v1/chat/completions
        method: [POST]
        accepted_paths: [/v1/chat/completions, /chat/completions]
        accepts_streaming: true
        request_kind: chat
      models:
        path: /v1/models
        method: [GET]
        accepted_paths: [/v1/models, /models]
        request_kind: passthrough
  anthropic:
    prefix: anthropic
    prefix_required: true
    base_url: %s
    required_headers:
      anthropic-version: "2023-06-01"
    endpoints:
      messages:
        path: /v1/messages
        method: [POST]
        accepted_paths: [/v1/messages]
        accepts_streaming: true
        request_kind: messages
      chat_completions:
        path: /v1/chat/completions
        method: [POST]
        accepted_paths: [/v1/chat/completions]
        accepts_streaming: true
        request_kind: chat
        auth_header: Authorization
        auth_format: "Bearer {key}"
  gemini:
    prefix: gemini
    prefix_required: true
    base_url: %s
    endpoints:
      chat_completions:
        path: /v1beta/openai/chat/completions
        method: [POST]
        accepted_paths: [/v1beta/openai/chat/completions]
        accepts_streaming: true
        request_kind: chat
        auth_header: Authorization
        auth_format: "Bearer {key}"
`, upstreamURL, upstreamURL, upstreamURL)

	//nolint:gosec // test fixture keys; not real credentials
	policyYAML := `configurations:
  dev:
    allowed_endpoints:
      - openai.chat_completions
      - openai.models
      - anthropic.messages
      - anthropic.chat_completions
      - gemini.chat_completions
    upstream_credentials:
      openai: sk-upstream-openai
      anthropic: sk-upstream-anthropic
      gemini: gm-upstream-gemini
  empty:
    allowed_endpoints: []
    upstream_credentials: {}

api_keys:
  - secret: sk_dev_local
    name: local
    configuration: dev
    enabled: true
  - secret: sk_dev_disabled
    name: disabled
    configuration: dev
    enabled: false
  - secret: sk_dev_restricted
    name: restricted
    configuration: empty
    enabled: true
`

	for name, body := range map[string]string{
		"providers.yaml": providersYAML,
		"policy.yaml":    policyYAML,
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

func TestGateway_EndpointNotAllowed(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", `{}`)
	req.Header.Set("Authorization", "Bearer sk_dev_restricted")
	req.Header.Set("Content-Type", "application/json")

	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestGateway_UnknownPath(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodGet, env.gatewayURL+"/does/not/exist", "")
	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGateway_MethodNotAllowed(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodGet, env.gatewayURL+"/v1/chat/completions", "")
	resp := doReq(t, req)
	defer closeBody(resp)

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestGateway_PassthroughMode(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/v1/chat/completions", `{}`)
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

// TestGateway_AnthropicChatCompletions_OpenAICompatSurface exercises the
// new v1.0.2 surface: the gateway routes /anthropic/v1/chat/completions
// to Anthropic's OpenAI-compatible chat completions endpoint, swapping
// the upstream credential into Authorization: Bearer (not the native
// x-api-key).
func TestGateway_AnthropicChatCompletions_OpenAICompatSurface(t *testing.T) {
	env := newTestEnv(t)

	body := `{"model":"claude-haiku-4-5","messages":[{"role":"user","content":"hi"}]}`
	req := newReq(t, http.MethodPost, env.gatewayURL+"/anthropic/v1/chat/completions", body)
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
	req := newReq(t, http.MethodPost, env.gatewayURL+"/gemini/v1beta/openai/chat/completions", body)
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

func TestGateway_AnthropicPrefixRouting(t *testing.T) {
	env := newTestEnv(t)

	req := newReq(t, http.MethodPost, env.gatewayURL+"/anthropic/v1/messages", `{}`)
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
