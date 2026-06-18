package admin_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/admin"
)

func TestSPAHandler_ServesIndexHTMLAtRoot(t *testing.T) {
	h := admin.SPAHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<!doctype html>") &&
		!strings.Contains(rec.Body.String(), "<!DOCTYPE html>") {
		t.Errorf("body did not look like HTML:\n%s", rec.Body.String())
	}
}

func TestSPAHandler_DeepLinkFallsBackToIndex(t *testing.T) {
	h := admin.SPAHandler()
	rec := httptest.NewRecorder()
	// A deep link like /dashboard does not exist as a file. The SPA
	// router handles it client-side; the server must serve index.html
	// so the SPA boots and the router takes over.
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<!doctype html>") &&
		!strings.Contains(rec.Body.String(), "<!DOCTYPE html>") {
		t.Errorf("deep-link fallback did not return HTML")
	}
}

func TestSPAHandler_DeepLinkWithExtensionFallsBack(t *testing.T) {
	// A request for /assets/missing.js (a path that looks like an
	// asset but doesn't exist) should still fall back to index.html
	// — the SPA fallback rule is "any path that doesn't resolve to a
	// file returns index.html." Browsers won't request a missing
	// asset reference under normal circumstances, but this keeps the
	// behaviour predictable.
	h := admin.SPAHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
