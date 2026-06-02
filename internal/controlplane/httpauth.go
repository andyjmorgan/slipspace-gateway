package controlplane

import (
	"net/http"
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
