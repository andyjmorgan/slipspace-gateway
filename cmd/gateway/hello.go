package main

import (
	"net/http"
)

// helloPath is the Anthropic connectivity probe. Claude Code fires
// `HEAD /api/hello` against ANTHROPIC_BASE_URL at session start to warm the
// TLS connection — a bare fetch with no API key and no X-Slipspace-* headers,
// so it can never resolve a configuration and would otherwise 401 at auth.
const helloPath = "/api/hello"

// helloBody mirrors api.anthropic.com's answer byte-for-byte so clients that
// do inspect the body see the same thing they would upstream.
const helloBody = `{"message": "hello"}`

// helloMiddleware answers GET/HEAD /api/hello locally, in front of the whole
// data plane. There is nothing to forward — the probe is credential-free and
// carries no policy — so answering here keeps it out of auth, selection,
// telemetry, and the live feed. Every other path (and every other method on
// this path) falls through untouched.
func helloMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != helloPath || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(helloBody))
		}
	})
}
