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

	"github.com/andyjmorgan/slipspace-gateway/internal/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

func captureLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

func TestHTTPHandler_HappyPathManaged(t *testing.T) {
	resolver := NewResolver(config.NewStore(fixtureConfig()))

	var captured AuthResult
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ar, ok := FromContext(r.Context())
		if !ok {
			t.Fatalf("AuthResult missing from context")
		}
		captured = ar
		w.WriteHeader(http.StatusOK)
	})

	h := HTTPHandler(resolver, next)

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
	if captured.Configuration == nil {
		t.Fatal("Configuration should propagate on success")
	}
	if got := captured.Configuration.Credentials["openai"]; got != "sk-openai-upstream" {
		t.Fatalf("Configuration.Credentials[openai] = %q, want sk-openai-upstream", got)
	}
	if !strings.Contains(logs.String(), `"result":"success"`) {
		t.Fatalf("success result not logged: %s", logs.String())
	}
}

func TestHTTPHandler_HappyPathPassthrough(t *testing.T) {
	resolver := NewResolver(config.NewStore(fixtureConfig()))

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ar, _ := FromContext(r.Context())
		if ar.Mode != ModePassthrough {
			t.Fatalf("mode = %q want passthrough", ar.Mode)
		}
		// Passthrough drops both selector headers (X-Sluice-Identity +
		// X-Sluice-Configuration); it must NOT inject any
		// credential-bearing header.
		if !containsAll(ar.DropHeaders, HeaderIdentity, HeaderConfiguration) {
			t.Fatalf("passthrough DropHeaders must include both selectors, got %v", ar.DropHeaders)
		}
		w.WriteHeader(http.StatusOK)
	})

	h := HTTPHandler(resolver, next)

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
	resolver := NewResolver(config.NewStore(fixtureConfig()))

	nextCalled := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	})

	h := HTTPHandler(resolver, next)
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
	resolver := NewResolver(config.NewStore(fixtureConfig()))

	h := HTTPHandler(resolver, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

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
	resolver := NewResolver(config.NewStore(fixtureConfig()))

	h := HTTPHandler(resolver, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set(HeaderConfiguration, "ghost")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d want 403", rec.Code)
	}
	assertErrorBody(t, rec.Result().Body, "unknown configuration")
}

func TestHTTPHandler_PanicsOnNilDeps(t *testing.T) {
	cases := []struct {
		name     string
		resolver *Resolver
		next     http.Handler
	}{
		{"nil resolver", nil, http.NotFoundHandler()},
		{"nil next", NewResolver(config.NewStore(fixtureConfig())), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic for %s", tc.name)
				}
			}()
			HTTPHandler(tc.resolver, tc.next)
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

func TestHTTPHandler_IdentityPassthroughHappyPath(t *testing.T) {
	resolver := NewResolver(config.NewStore(fixtureConfig()))

	var captured AuthResult
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ar, _ := FromContext(r.Context())
		captured = ar
		w.WriteHeader(http.StatusOK)
	})

	h := HTTPHandler(resolver, next)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set(HeaderIdentity, "sk_live_enabled")
	req.Header.Set(HeaderAuthorization, "Bearer client-byok-anthropic-token")
	logger, logs := captureLogger()
	req = req.WithContext(observability.WithLogger(req.Context(), logger))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	if captured.Mode != ModePassthrough {
		t.Fatalf("mode = %q want passthrough", captured.Mode)
	}
	if captured.APIKey == nil || captured.APIKey.Name != "enabled-key" {
		t.Fatalf("APIKey not propagated from identity lookup, got %+v", captured.APIKey)
	}
	if !strings.Contains(logs.String(), `"mode":"passthrough"`) {
		t.Fatalf("passthrough mode not logged: %s", logs.String())
	}
	if strings.Contains(logs.String(), "deprecated header in use") {
		t.Fatalf("identity-only resolution must not emit the deprecation warning: %s", logs.String())
	}
}

func TestHTTPHandler_LegacyConfigurationHeaderLogsDeprecation(t *testing.T) {
	resolver := NewResolver(config.NewStore(fixtureConfig()))

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := HTTPHandler(resolver, next)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set(HeaderConfiguration, "prod")
	req.Header.Set(HeaderAuthorization, "Bearer byok-token")
	logger, logs := captureLogger()
	req = req.WithContext(observability.WithLogger(req.Context(), logger))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	out := logs.String()
	if !strings.Contains(out, "deprecated header in use") {
		t.Fatalf("legacy passthrough must emit deprecation warning, logs:\n%s", out)
	}
	if !strings.Contains(out, `"replacement":"X-Sluice-Identity"`) {
		t.Fatalf("deprecation log must name the replacement header, logs:\n%s", out)
	}
}

func containsAll(haystack []string, needles ...string) bool {
	set := make(map[string]struct{}, len(haystack))
	for _, h := range haystack {
		set[h] = struct{}{}
	}
	for _, n := range needles {
		if _, ok := set[n]; !ok {
			return false
		}
	}
	return true
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
