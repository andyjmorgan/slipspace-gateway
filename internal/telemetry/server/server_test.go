package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/config"
)

// stubPinger is a hand-rolled readiness dependency (no gomock/testify).
type stubPinger struct{ err error }

func (s stubPinger) Ping(context.Context) error { return s.err }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestServer builds a server whose console password is "hunter2".
func newTestServer(t *testing.T, ping error) *Server {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("hunter2"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	console := config.Console{Username: "admin", PasswordHash: string(hash)}
	return New(console, stubPinger{err: ping}, nil, nil, config.DefaultSpanFieldMaxBytes, discardLogger())
}

func doRequest(t *testing.T, h http.Handler, path string, auth *[2]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if auth != nil {
		req.SetBasicAuth(auth[0], auth[1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthz_Open(t *testing.T) {
	h := newTestServer(t, nil).Handler()
	resp := doRequest(t, h, "/healthz", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
}

func TestReadyz_OK(t *testing.T) {
	h := newTestServer(t, nil).Handler()
	resp := doRequest(t, h, "/readyz", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
}

func TestReadyz_StoreDown(t *testing.T) {
	h := newTestServer(t, errors.New("connection refused")).Handler()
	resp := doRequest(t, h, "/readyz", nil)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.Code)
	}
}

func TestConsoleSPA_Public(t *testing.T) {
	// The SPA shell + assets are public (no Basic prompt); auth is on the API.
	h := newTestServer(t, nil).Handler()
	// Root serves the SPA entry (placeholder in the test binary — built assets
	// are gitignored and not embedded).
	if resp := doRequest(t, h, "/", nil); resp.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.Code)
	}
	// An unknown deep link falls back to the SPA entry (client-side routing).
	if resp := doRequest(t, h, "/messages", nil); resp.Code != http.StatusOK {
		t.Fatalf("SPA fallback status = %d, want 200", resp.Code)
	}
	// A real embedded file is served directly.
	if resp := doRequest(t, h, "/placeholder.html", nil); resp.Code != http.StatusOK {
		t.Fatalf("static file status = %d, want 200", resp.Code)
	}
}

// TestAPIAuthMatrix exercises the Basic-auth guard on a gated API route (the
// SPA shell is public, so the guard is verified on the API instead).
func TestAPIAuthMatrix(t *testing.T) {
	h := newQueryServer(t, &fakeQueries{})
	cases := []struct {
		name string
		user string
		pass string
		want int
	}{
		{"correct", "admin", "hunter2", http.StatusOK},
		{"wrong password", "admin", "nope", http.StatusUnauthorized},
		{"wrong username", "root", "hunter2", http.StatusUnauthorized},
		{"both wrong", "root", "nope", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doRequest(t, h, "/api/v1/dashboard/summary", &[2]string{tc.user, tc.pass})
			if resp.Code != tc.want {
				t.Fatalf("status = %d, want %d", resp.Code, tc.want)
			}
			// A rejected request must NOT carry WWW-Authenticate, or the
			// browser pops its native Basic dialog over the SPA's own login
			// form and re-fires on every poll. The SPA drives auth itself.
			if tc.want == http.StatusUnauthorized && resp.Header().Get("WWW-Authenticate") != "" {
				t.Errorf("401 carried WWW-Authenticate %q; must be a bare 401", resp.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

// TestAuthCache_MemoizesSuccess proves the bcrypt result is cached: after one
// successful verify we rotate the stored hash out from under the server, and the
// already-verified credential still passes (it can only do so by skipping the
// now-mismatched bcrypt), while the new password also works via a fresh hash.
func TestAuthCache_MemoizesSuccess(t *testing.T) {
	s := newTestServer(t, nil)

	if !s.credentialsValid("admin", "hunter2") {
		t.Fatal("correct credential rejected")
	}
	// Rotate the configured hash to a DIFFERENT password. bcrypt against this
	// hash would now reject "hunter2".
	rotated, err := bcrypt.GenerateFromPassword([]byte("changed"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	s.console.PasswordHash = string(rotated)

	if !s.credentialsValid("admin", "hunter2") {
		t.Error("previously-verified credential rejected after hash rotation; cache not consulted")
	}
	if !s.credentialsValid("admin", "changed") {
		t.Error("new password rejected; bcrypt miss path broken")
	}
}

// TestAuthCache_OnlyCachesSuccess confirms wrong credentials never populate the
// cache, so an attacker cannot grow the map with distinct guesses.
func TestAuthCache_OnlyCachesSuccess(t *testing.T) {
	s := newTestServer(t, nil)

	for _, pass := range []string{"nope", "wrong", "guess"} {
		if s.credentialsValid("admin", pass) {
			t.Fatalf("wrong password %q accepted", pass)
		}
	}
	if n := len(s.auth.entries); n != 0 {
		t.Errorf("cache holds %d entries after only-failed attempts, want 0", n)
	}
	if !s.credentialsValid("admin", "hunter2") {
		t.Fatal("correct credential rejected")
	}
	if n := len(s.auth.entries); n != 1 {
		t.Errorf("cache holds %d entries after one success, want 1", n)
	}
}

// TestAuthCache_Expiry covers the lazy-expiry lookup: a live entry is valid, an
// expired one reports invalid and is dropped, a missing key is invalid.
func TestAuthCache_Expiry(t *testing.T) {
	var c authCache
	if c.valid("absent") {
		t.Error("missing key reported valid")
	}
	c.store("live", time.Now().Add(time.Minute))
	if !c.valid("live") {
		t.Error("live entry reported invalid")
	}
	c.store("stale", time.Now().Add(-time.Second))
	if c.valid("stale") {
		t.Error("expired entry reported valid")
	}
	if _, ok := c.entries["stale"]; ok {
		t.Error("expired entry not dropped on lookup")
	}
}

// TestCredentialKey_Distinct verifies the NUL-join prevents (user, pass) pairs
// that concatenate to the same string from colliding.
func TestCredentialKey_Distinct(t *testing.T) {
	first := credentialKey("admin", "hunter2")
	again := credentialKey("admin", "hunter2")
	if first != again {
		t.Error("same credential produced different keys")
	}
	// Without the NUL separator, ("ab","c") and ("a","bc") would collide.
	if credentialKey("ab", "c") == credentialKey("a", "bc") {
		t.Error("ambiguous concatenation collided")
	}
}

func TestHTTPServer_Wires(t *testing.T) {
	srv := newTestServer(t, nil).HTTPServer("127.0.0.1:0")
	if srv.Addr != "127.0.0.1:0" {
		t.Errorf("Addr = %q", srv.Addr)
	}
	if srv.Handler == nil {
		t.Error("Handler is nil")
	}
	if srv.ReadHeaderTimeout == 0 {
		t.Error("ReadHeaderTimeout not set")
	}
}
