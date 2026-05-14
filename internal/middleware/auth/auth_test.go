package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

type routeStub struct {
	provider string
	endpoint string
	ok       bool
}

func (s routeStub) From(_ context.Context) (string, string, bool) {
	return s.provider, s.endpoint, s.ok
}

func captureLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

func TestHTTPHandler_HappyPathManaged(t *testing.T) {
	resolver := NewResolver(fixtureConfig())
	route := routeStub{provider: "openai", endpoint: "chat_completions", ok: true}

	var captured AuthResult
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ar, ok := FromContext(r.Context())
		if !ok {
			t.Fatalf("AuthResult missing from context")
		}
		captured = ar
		w.WriteHeader(http.StatusOK)
	})

	h := HTTPHandler(resolver, route.From, next)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set(HeaderAuthorization, "Bearer sk_live_enabled")
	logger, logs := captureLogger()
	req = req.WithContext(observability.WithLogger(req.Context(), logger))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	if captured.Mode != ModeManaged {
		t.Fatalf("captured mode = %q", captured.Mode)
	}
	if captured.SetHeaders.Get(HeaderAuthorization) != "Bearer sk-openai-upstream" {
		t.Fatalf("auth swap did not propagate to context")
	}
	if !strings.Contains(logs.String(), `"result":"success"`) {
		t.Fatalf("success result not logged: %s", logs.String())
	}
}

func TestHTTPHandler_HappyPathPassthrough(t *testing.T) {
	resolver := NewResolver(fixtureConfig())
	route := routeStub{provider: "anthropic", endpoint: "messages", ok: true}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ar, _ := FromContext(r.Context())
		if ar.Mode != ModePassthrough {
			t.Fatalf("mode = %q want passthrough", ar.Mode)
		}
		if len(ar.SetHeaders) != 0 || len(ar.DropHeaders) != 0 {
			t.Fatalf("passthrough must not mutate headers; set=%v drop=%v", ar.SetHeaders, ar.DropHeaders)
		}
		w.WriteHeader(http.StatusOK)
	})

	h := HTTPHandler(resolver, route.From, next)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set(HeaderConfiguration, "prod")
	req.Header.Set(HeaderAuthorization, "Bearer byok-token-xyz")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
}

func TestHTTPHandler_Unauthorized(t *testing.T) {
	resolver := NewResolver(fixtureConfig())
	route := routeStub{provider: "openai", endpoint: "chat_completions", ok: true}

	nextCalled := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	})

	h := HTTPHandler(resolver, route.From, next)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rec.Code)
	}
	if nextCalled {
		t.Fatalf("next must not be invoked on auth failure")
	}
	assertErrorBody(t, rec.Result().Body, "unauthorized")
}

func TestHTTPHandler_DisabledKey_LogsDisabledResult(t *testing.T) {
	resolver := NewResolver(fixtureConfig())
	route := routeStub{provider: "openai", endpoint: "chat_completions", ok: true}

	h := HTTPHandler(resolver, route.From, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set(HeaderAuthorization, "Bearer sk_live_disabled")
	logger, logs := captureLogger()
	req = req.WithContext(observability.WithLogger(req.Context(), logger))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rec.Code)
	}
	if !strings.Contains(logs.String(), `"result":"disabled_key"`) {
		t.Fatalf("disabled_key result not logged: %s", logs.String())
	}
}

func TestHTTPHandler_UnknownConfiguration(t *testing.T) {
	resolver := NewResolver(fixtureConfig())
	route := routeStub{provider: "openai", endpoint: "chat_completions", ok: true}

	h := HTTPHandler(resolver, route.From, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set(HeaderConfiguration, "ghost")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d want 403", rec.Code)
	}
	assertErrorBody(t, rec.Result().Body, "unknown configuration")
}

func TestHTTPHandler_EndpointNotAllowed(t *testing.T) {
	resolver := NewResolver(fixtureConfig())
	route := routeStub{provider: "openai", endpoint: "chat_completions", ok: true}

	h := HTTPHandler(resolver, route.From, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set(HeaderConfiguration, "restricted")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d want 403", rec.Code)
	}
	assertErrorBody(t, rec.Result().Body, "endpoint not allowed for this configuration")
}

func TestHTTPHandler_NoRouteOnContext(t *testing.T) {
	resolver := NewResolver(fixtureConfig())
	route := routeStub{ok: false}

	h := HTTPHandler(resolver, route.From, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d want 500", rec.Code)
	}
}

func TestHTTPHandler_PanicsOnNilDeps(t *testing.T) {
	cases := []struct {
		name     string
		resolver *Resolver
		route    RouteFromContextFunc
		next     http.Handler
	}{
		{"nil resolver", nil, func(context.Context) (string, string, bool) { return "", "", true }, http.NotFoundHandler()},
		{"nil routeFrom", NewResolver(fixtureConfig()), nil, http.NotFoundHandler()},
		{"nil next", NewResolver(fixtureConfig()), func(context.Context) (string, string, bool) { return "", "", true }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic for %s", tc.name)
				}
			}()
			HTTPHandler(tc.resolver, tc.route, tc.next)
		})
	}
}

func TestFromContext_Empty(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Fatalf("FromContext should report false when nothing is stashed")
	}
	if _, ok := FromContext(nil); ok { //nolint:staticcheck
		t.Fatalf("FromContext(nil) should report false")
	}
}

func TestClassifyResult_UnknownErrorIsUnknownKey(t *testing.T) {
	got := classifyResult(io.EOF, AuthResult{})
	if got != ResultUnknownKey {
		t.Fatalf("classifyResult fallback = %q want %q", got, ResultUnknownKey)
	}
}

func TestWriteAuthError_FallbackIs500(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAuthError(rec, io.EOF)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d want 500", rec.Code)
	}
	assertErrorBody(t, rec.Result().Body, "internal error")
}

func TestWithAuthRoundTrip(t *testing.T) {
	ctx := withAuth(context.Background(), AuthResult{Mode: ModeManaged, ConfigurationName: "prod"})
	ar, ok := FromContext(ctx)
	if !ok || ar.ConfigurationName != "prod" {
		t.Fatalf("withAuth/FromContext round-trip failed: %+v ok=%v", ar, ok)
	}
}

func assertErrorBody(t *testing.T, body io.ReadCloser, want string) {
	t.Helper()
	defer func() { _ = body.Close() }()
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if payload.Error != want {
		t.Fatalf("error body = %q want %q", payload.Error, want)
	}
}
