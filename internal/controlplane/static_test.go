package controlplane

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSPAHandler_ServesHTMLAtRoot(t *testing.T) {
	rec := httptest.NewRecorder()
	SPAHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "<!doctype html>") {
		t.Errorf("body did not look like HTML:\n%s", rec.Body.String())
	}
}

func TestSPAHandler_DeepLinkFallsBack(t *testing.T) {
	rec := httptest.NewRecorder()
	SPAHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fleet", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "<!doctype html>") {
		t.Errorf("deep-link fallback did not return HTML")
	}
}

// TestSPAHandler_PlaceholderFallback exercises the no-index.html branch via a
// MapFS that holds only placeholder.html — the fresh-checkout / pre-build case
// where the Vite bundle hasn't been embedded yet.
func TestSPAHandler_PlaceholderFallback(t *testing.T) {
	sub := fstest.MapFS{
		"placeholder.html": {Data: []byte("<!doctype html><title>placeholder</title>")},
	}
	h := spaHandlerFromFS(sub)
	for _, path := range []string{"/", "/fleet", "/assets/missing.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "placeholder") {
			t.Errorf("%s: did not serve placeholder", path)
		}
	}
}

func TestSPAHandler_ServesRealAsset(t *testing.T) {
	sub := fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html><div id=root></div>")},
		"assets/app.js":    {Data: []byte("console.log(1)")},
		"placeholder.html": {Data: []byte("placeholder")},
	}
	h := spaHandlerFromFS(sub)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("real asset not served: code=%d body=%q", rec.Code, rec.Body.String())
	}
}
