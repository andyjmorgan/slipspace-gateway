package admin

import (
	"crypto/subtle"
	"net/http"

	"github.com/andyjmorgan/slipspace-gateway/contracts/admin"
)

// BasicAuth wraps next with HTTP Basic authentication. The expected
// username is hardcoded to admin.Username; the password is taken from
// pwd (already resolved via admin.Config.ResolvePassword at startup).
//
// Constant-time comparison defeats timing side-channels on both the
// username and password — the cost is fixed and worth it for a
// credential check.
//
// A request without an Authorization header, with a non-Basic scheme,
// or with mismatched credentials gets a bare 401 — NO WWW-Authenticate
// header. The SPA's login form drives the credential prompt; if we
// returned `WWW-Authenticate: Basic …` here, browsers would intercept
// the 401 and pop their native auth dialog over the SPA, then silently
// retry with whatever the user typed there. That bypass would let a
// bad sessionStorage password slip through whenever the user happened
// to type the right one into the browser prompt. curl users wanting
// `--basic` interactivity can supply credentials directly; the header
// is not required by RFC 7617 for the auth scheme to work, only for
// browser-driven challenge-response which we deliberately suppress.
func BasicAuth(pwd string, next http.Handler) http.Handler {
	expectedUser := []byte(admin.Username)
	expectedPass := []byte(pwd)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			unauthorized(w)
			return
		}
		// Both comparisons must run to keep timing flat across the
		// "wrong username" and "wrong password" branches.
		userOK := subtle.ConstantTimeCompare([]byte(user), expectedUser) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), expectedPass) == 1
		if !userOK || !passOK {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func unauthorized(w http.ResponseWriter) {
	w.WriteHeader(http.StatusUnauthorized)
}
