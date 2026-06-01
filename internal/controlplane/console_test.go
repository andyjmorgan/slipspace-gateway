package controlplane

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleHandler_ServesPage(t *testing.T) {
	rec := httptest.NewRecorder()
	ConsoleHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Fleet") || !strings.Contains(body, "/api/v1/fleet") {
		t.Fatalf("console page missing expected markers")
	}
}

func TestConsoleHandler_NotFoundOffRoot(t *testing.T) {
	rec := httptest.NewRecorder()
	ConsoleHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestConsoleHandler_MethodNotAllowed(t *testing.T) {
	rec := httptest.NewRecorder()
	ConsoleHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
