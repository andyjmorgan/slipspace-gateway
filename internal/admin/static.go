package admin

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

// spaFS holds the Vite build output. Production builds expect
// internal/admin/webdist/ to contain a freshly-built SPA (Vite is
// configured to emit here — see web/vite.config.ts). When the build
// hasn't run (fresh checkout, CI before `make web`), only the committed
// placeholder.html exists; SPAHandler serves that as a fallback so the
// listener still produces a sensible response.
//
//go:embed all:webdist
var spaFS embed.FS

const (
	indexFile       = "index.html"
	placeholderFile = "placeholder.html"
)

// SPAHandler returns an http.Handler that serves the embedded SPA. It
// resolves real assets (hashed JS/CSS produced by Vite) by relative path
// and falls back to index.html for any path that doesn't resolve to a
// file — which is how a SPA router (basename `/`) keeps deep links
// working on hard reload.
//
// When the real index.html is absent (SPA not built into this binary),
// every path is served the placeholder.html stub instead.
//
// The handler never touches /api/v1/* paths; those are mounted on a
// separate mux ahead of this one so the static handler only ever sees
// SPA traffic.
//
// fs.Sub on a static embed path is infallible in practice — the error
// return would only fire if the embed directive itself was malformed,
// which is a compile-time failure. We ignore it.
func SPAHandler() http.Handler {
	sub, _ := fs.Sub(spaFS, "webdist")
	return spaHandlerFromFS(sub)
}

// spaHandlerFromFS builds the SPA handler around an arbitrary fs.FS.
// Split out so tests can inject a fstest.MapFS to exercise the
// placeholder-fallback path that doesn't fire when both index.html and
// placeholder.html are present in the real embed.
func spaHandlerFromFS(sub fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(sub))

	// Determine the SPA entry filename once at construction. If the
	// real index.html shipped, use it; else the placeholder takes its
	// place for every fallback path.
	entry := indexFile
	if _, err := sub.Open(indexFile); err != nil {
		entry = placeholderFile
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			serveEntry(w, sub, entry)
			return
		}
		if _, err := sub.Open(path); err != nil {
			// SPA fallback: serve the entry document so the
			// client-side router can pick up the path.
			serveEntry(w, sub, entry)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// serveEntry writes the SPA entry document (index.html or
// placeholder.html) to w. A missing entry is a build-system
// misconfiguration — neither real builds nor the placeholder fallback
// should ever hit it — but we report 500 with a clear body rather than
// returning an empty 200.
func serveEntry(w http.ResponseWriter, sub fs.FS, name string) {
	f, err := sub.Open(name)
	if err != nil {
		http.Error(w, "spa entry missing", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.Copy(w, f)
}
