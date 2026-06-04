package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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
	return New(console, stubPinger{err: ping}, discardLogger())
}

func doRequest(t *testing.T, h http.Handler, method, path string, auth *[2]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if auth != nil {
		req.SetBasicAuth(auth[0], auth[1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestHealthz_Open(t *testing.T) {
	h := newTestServer(t, nil).Handler()
	resp := doRequest(t, h, http.MethodGet, "/healthz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestReadyz_OK(t *testing.T) {
	h := newTestServer(t, nil).Handler()
	resp := doRequest(t, h, http.MethodGet, "/readyz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestReadyz_StoreDown(t *testing.T) {
	h := newTestServer(t, errors.New("connection refused")).Handler()
	resp := doRequest(t, h, http.MethodGet, "/readyz", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestConsole_RequiresAuth(t *testing.T) {
	h := newTestServer(t, nil).Handler()
	resp := doRequest(t, h, http.MethodGet, "/", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("missing WWW-Authenticate challenge")
	}
}

func TestConsole_AuthMatrix(t *testing.T) {
	h := newTestServer(t, nil).Handler()
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
			resp := doRequest(t, h, http.MethodGet, "/", &[2]string{tc.user, tc.pass})
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
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
