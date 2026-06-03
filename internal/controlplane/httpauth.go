package controlplane

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// BasicAuth wraps next with HTTP Basic authentication. verify decides whether a
// presented (username, password) pair is valid — production passes
// AdminAuthenticator.Verify, which checks a bcrypt hash sourced from Postgres.
//
// A failed check returns a bare 401 with NO WWW-Authenticate header — that
// header would make a browser pop its native auth dialog over the console.
// (Re-implemented here rather than importing internal/admin, which embeds the
// gateway SPA and would bloat the control-plane binary.)
func BasicAuth(verify func(username, password string) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || !verify(user, pass) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// BearerAuth wraps next with bootstrap-token auth: the request must carry
// "Authorization: Bearer <token>" matching token (constant-time compare). This
// gates gateway-facing HTTP endpoints (segment ingest) with the same credential
// the gRPC fleet channel uses (TokenAuthInterceptor) — gateways present the
// bootstrap token, never the admin console password.
//
// An empty token disables the check (trusted-network dev only), mirroring the
// gRPC interceptor's behaviour. A failed check returns a bare 401.
func BearerAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
