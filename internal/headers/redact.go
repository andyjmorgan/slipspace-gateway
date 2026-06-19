// Package headers carries shared helpers for inspecting and redacting
// HTTP header maps in places that surface them to operators — debug
// logs in the proxy, the live-feed body envelope, and anywhere else
// that might leak a credential into an admin-facing channel.
package headers

import (
	"net/http"
	"strings"
)

// builtinSensitiveSubstrings is the always-active lowercase-substring
// list. Catches the common credential header conventions across the
// providers we forward to plus our own X-Slipspace-Identity:
// Authorization, Proxy-Authorization, X-Api-Key, x-api-key,
// Anthropic-Api-Key, X-Goog-Api-Key, Cookie, Set-Cookie,
// X-Auth-Token, X-CSRF-Token, X-API-Secret, X-Slipspace-Identity.
// Over-redacting on display is the safer error.
var builtinSensitiveSubstrings = []string{
	"auth",
	"api-key",
	"apikey",
	"token",
	"cookie",
	"secret",
	"slipspace-identity",
}

// Redactor masks credential-bearing headers. Construct one via
// NewRedactor at startup with the operator-supplied extras; the
// built-in substring list is always active on top of those.
//
// Methods are safe for concurrent use — the substring slices are
// read-only after construction.
type Redactor struct {
	// substrings is the resolved match list: built-ins followed by
	// the operator-supplied extras, all lowercased and deduped.
	substrings []string
}

// NewRedactor returns a Redactor that masks the built-in sensitive
// headers plus any whose lowercased name contains one of the extra
// substrings. Each extra is trimmed, lowercased, and deduped
// against itself and the built-ins; empty entries are dropped.
// Passing nil or an empty slice yields a Redactor with default
// behaviour identical to the pre-config version of this package.
func NewRedactor(extra []string) *Redactor {
	subs := make([]string, 0, len(builtinSensitiveSubstrings)+len(extra))
	subs = append(subs, builtinSensitiveSubstrings...)
	seen := make(map[string]struct{}, len(subs))
	for _, s := range builtinSensitiveSubstrings {
		seen[s] = struct{}{}
	}
	for _, e := range extra {
		v := strings.ToLower(strings.TrimSpace(e))
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		subs = append(subs, v)
	}
	return &Redactor{substrings: subs}
}

// Extras returns the operator-supplied substrings, in canonical
// (lowercase, deduped) form. The built-in matches are not included —
// this surface exists so observability code can log "what did the
// operator add", not "what gets redacted total". The returned slice
// is a fresh copy; callers may modify it without affecting r.
func (r *Redactor) Extras() []string {
	if r == nil {
		return nil
	}
	builtinSet := make(map[string]struct{}, len(builtinSensitiveSubstrings))
	for _, s := range builtinSensitiveSubstrings {
		builtinSet[s] = struct{}{}
	}
	out := make([]string, 0, len(r.substrings))
	for _, s := range r.substrings {
		if _, isBuiltin := builtinSet[s]; isBuiltin {
			continue
		}
		out = append(out, s)
	}
	return out
}

// IsSensitive reports whether name looks credential-bearing under
// the redactor's substring set. A nil receiver falls back to the
// built-in list so test fixtures that construct headers from
// literals don't need a Redactor handle.
func (r *Redactor) IsSensitive(name string) bool {
	lower := strings.ToLower(name)
	subs := builtinSensitiveSubstrings
	if r != nil && len(r.substrings) > 0 {
		subs = r.substrings
	}
	for _, s := range subs {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// Redact returns a shallow copy of h with values replaced by
// "[REDACTED]" for any header name IsSensitive reports as
// credential-bearing. Empty input yields a nil result so callers can
// short-circuit at the assignment.
func (r *Redactor) Redact(h http.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		if r.IsSensitive(k) {
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
