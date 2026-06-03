package controlplane

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

// spaFS holds the control-plane console's Vite build output. Production
// builds expect internal/controlplane/webdist/ to contain a freshly-built
// SPA (Vite emits here — see web/vite.cp.config.ts). When the build hasn't
// run (fresh checkout, CI before `make web-cp`), only the committed
// placeholder.html exists; SPAHandler serves that so the listener still
// produces a sensible response.
//
//go:embed all:webdist
var spaFS embed.FS

const (
	indexFile       = "index.html"
	placeholderFile = "placeholder.html"
)

// SPAHandler serves the embedded control-plane console SPA. It resolves real
// assets (hashed JS/CSS produced by Vite) by relative path and falls back to
// index.html for any path that doesn't resolve to a file — how a SPA router
// (basename "/") keeps deep links working on hard reload. When the real
// index.html is absent (SPA not built into this binary), every path is served
// the placeholder.html stub.
//
// The handler never sees /api/v1/* paths; those are mounted on the mux ahead
// of it, so it only ever serves SPA traffic.
func SPAHandler() http.Handler {
	sub, _ := fs.Sub(spaFS, "webdist")
	return spaHandlerFromFS(sub)
}

func spaHandlerFromFS(sub fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(sub))

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
			serveEntry(w, sub, entry)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func serveEntry(w http.ResponseWriter, sub fs.FS, name string) {
	f, err := sub.Open(name)
	if err != nil {
		http.Error(w, "spa entry missing", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.Copy(w, f)
}
