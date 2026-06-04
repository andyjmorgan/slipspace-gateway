package server

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

// spaFS holds the Vite build output for the telemetry console. Production builds
// expect internal/telemetry/server/webdist/ to contain a freshly-built SPA (see
// web/vite.telemetry.config.ts). When the build hasn't run, only the committed
// placeholder.html exists and the handler serves that as a fallback.
//
//go:embed all:webdist
var spaFS embed.FS

const (
	spaEntryFile  = "index.telemetry.html"
	placeholderFn = "placeholder.html"
)

// spaHandler serves the embedded telemetry console SPA. Real assets (hashed
// JS/CSS) resolve by path; anything that doesn't resolve falls back to the SPA
// entry so the client-side router keeps deep links working on hard reload. When
// the SPA hasn't been built into the binary, the placeholder stub stands in.
//
// The handler is mounted as the catch-all behind the API + probe routes, so it
// only ever sees console/asset traffic. The SPA itself is public; the API it
// calls is Basic-auth gated.
func spaHandler() http.Handler {
	sub, _ := fs.Sub(spaFS, "webdist")
	fileServer := http.FileServer(http.FS(sub))

	entry := spaEntryFile
	if _, err := sub.Open(spaEntryFile); err != nil {
		entry = placeholderFn
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			serveSPAEntry(w, sub, entry)
			return
		}
		if _, err := sub.Open(path); err != nil {
			serveSPAEntry(w, sub, entry)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func serveSPAEntry(w http.ResponseWriter, sub fs.FS, name string) {
	f, err := sub.Open(name)
	if err != nil {
		http.Error(w, "spa entry missing", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.Copy(w, f)
}
