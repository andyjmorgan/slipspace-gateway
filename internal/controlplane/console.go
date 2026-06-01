package controlplane

import (
	_ "embed"
	"net/http"
)

//go:embed console.html
var consoleHTML []byte

// ConsoleHandler serves the read-only fleet console — a single self-contained
// page that polls GET /api/v1/fleet and renders the registry. It serves the
// page only at the root path; any other path under it is a 404, so it composes
// as the catch-all behind the JSON read endpoints on the same mux.
func ConsoleHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(consoleHTML)
	})
}
