package controlplane

import (
	"crypto/subtle"
	"net/http"

	contractsadmin "github.com/andyjmorgan/sluice-gateway/contracts/admin"
)

// BasicAuth wraps next with HTTP Basic authentication, mirroring the gateway
// admin console (internal/admin.BasicAuth) so the control plane and the
// appliance share one credential convention. The username is the shared
// contractsadmin.Username ("admin"); the password is supplied by the operator.
//
// Both fields are compared in constant time, and a failed check returns a bare
// 401 with NO WWW-Authenticate header — that header would make a browser pop
// its native auth dialog over the console. (Re-implemented here rather than
// importing internal/admin, which embeds the gateway SPA and would bloat the
// control-plane binary.)
func BasicAuth(password string, next http.Handler) http.Handler {
	expectedUser := []byte(contractsadmin.Username)
	expectedPass := []byte(password)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Run both comparisons regardless so timing is flat across the
		// wrong-username and wrong-password branches.
		userOK := subtle.ConstantTimeCompare([]byte(user), expectedUser) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), expectedPass) == 1
		if !userOK || !passOK {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
