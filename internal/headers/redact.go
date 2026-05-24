// Package headers carries shared helpers for inspecting and redacting
// HTTP header maps in places that surface them to operators — debug
// logs in the proxy, the live-feed body envelope, and anywhere else
// that might leak a credential into an admin-facing channel.
package headers

import (
	"net/http"
	"strings"
)

// RedactSensitive returns a shallow copy of h with values replaced by
// "[REDACTED]" for any header name that looks credential-bearing.
//
// Match is substring-based on the lowercased name so it catches every
// variant in one rule: Authorization, Proxy-Authorization, X-Api-Key,
// x-api-key, Anthropic-Api-Key, X-Goog-Api-Key, Cookie, Set-Cookie,
// X-Auth-Token, X-CSRF-Token, X-API-Secret, etc. — without enumerating
// each. Over-redacting on display is the safer error.
func RedactSensitive(h http.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		if IsSensitiveHeaderName(k) {
			masked := make([]string, len(vs))
			for i := range vs {
				masked[i] = "[REDACTED]"
			}
			out[k] = masked
			continue
		}
		cloned := make([]string, len(vs))
		copy(cloned, vs)
		out[k] = cloned
	}
	return out
}

// IsSensitiveHeaderName reports whether a header name looks credential-
// bearing under a permissive lowercase-substring match.
//
// "sluice-identity" catches X-Sluice-Identity, which carries a raw
// sk_live_ Sluice secret in passthrough mode. Without this match the
// secret would round-trip into livefeed entries and every connector
// destination's captured Record.Request.Headers.
func IsSensitiveHeaderName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "auth") ||
		strings.Contains(lower, "api-key") ||
		strings.Contains(lower, "apikey") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "cookie") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "sluice-identity")
}
