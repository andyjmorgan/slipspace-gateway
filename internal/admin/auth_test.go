package admin_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/admin"
)

func ok(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func TestBasicAuth_NoHeader(t *testing.T) {
	h := admin.BasicAuth("secret", http.HandlerFunc(ok))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	// 401 must NOT carry WWW-Authenticate — that header makes browsers
	// pop their native auth dialog over the SPA, which silently
	// bypasses the SPA's "Invalid credentials" path.
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("WWW-Authenticate = %q, want empty (browser dialog suppression)", got)
	}
}

func TestBasicAuth_WrongUser(t *testing.T) {
	h := admin.BasicAuth("secret", http.HandlerFunc(ok))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("operator", "secret")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestBasicAuth_WrongPassword(t *testing.T) {
	h := admin.BasicAuth("secret", http.HandlerFunc(ok))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "wrong")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestBasicAuth_Success(t *testing.T) {
	h := admin.BasicAuth("secret", http.HandlerFunc(ok))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "secret")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Errorf("body = %q, want ok", got)
	}
}

func TestBasicAuth_NonBasicScheme(t *testing.T) {
	h := admin.BasicAuth("secret", http.HandlerFunc(ok))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
