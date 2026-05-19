package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// TestSPAHandler_PlaceholderFallback exercises the path where the SPA
// hasn't been built into the binary — only placeholder.html exists.
// The committed embed always carries an index.html (Vite's build is
// gitignored but the real fs sees the developer's last build), so this
// test injects a stub FS with only placeholder.html present.
func TestSPAHandler_PlaceholderFallback(t *testing.T) {
	fs := fstest.MapFS{
		"placeholder.html": &fstest.MapFile{Data: []byte("<html>SPA not built</html>")},
	}
	h := spaHandlerFromFS(fs)

	cases := []struct {
		name string
		path string
	}{
		{"root", "/"},
		{"deep-link", "/dashboard"},
		{"asset-style", "/assets/anything.js"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "SPA not built") {
				t.Errorf("expected placeholder body, got %q", rec.Body.String())
			}
		})
	}
}

// TestServeEntry_MissingFileReturns500 covers the defensive error
// branch where the entry document the handler was constructed against
// has somehow disappeared. Should never fire in real deployments —
// neither real builds nor placeholder fallbacks reach it — but the
// branch is reachable when an empty FS is passed.
func TestServeEntry_MissingFileReturns500(t *testing.T) {
	fs := fstest.MapFS{}
	rec := httptest.NewRecorder()
	serveEntry(rec, fs, "index.html")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "spa entry missing") {
		t.Errorf("body = %q, want spa entry missing", rec.Body.String())
	}
}
