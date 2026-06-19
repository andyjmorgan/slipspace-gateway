package observability

import (
	"net/http"
	"strings"
)

// idResolver resolves an identifier from inbound request headers: an
// authoritative SlipSpace header is always tried first, then an ordered fallback
// chain (shipped defaults plus operator extras) walked top-down. It is the
// shared engine behind the session, conversation/thread, and parent resolvers —
// each a thin constructor over this with its own authoritative header and
// default chain. See the design note "Correlating Requests Across Turns
// (Session Bundling)".
type idResolver struct {
	// authoritative is the SlipSpace-owned header tried before any fallback. A
	// client or proxy that deliberately sets it wins over any ambient client
	// header. Empty means the resolver has no authoritative header.
	authoritative string
	// fallbacks is the ordered fallback chain (shipped defaults plus operator
	// extras), walked after the authoritative header.
	fallbacks []string
}

// newIDResolver builds a resolver whose fallback chain is defaults followed by
// extra (operator-supplied custom headers, kept in order). Blank extras are
// dropped. authoritative may be empty for axes with no SlipSpace-owned header.
func newIDResolver(authoritative string, defaults, extra []string) *idResolver {
	fb := make([]string, 0, len(defaults)+len(extra))
	fb = append(fb, defaults...)
	for _, e := range extra {
		if e = strings.TrimSpace(e); e != "" {
			fb = append(fb, e)
		}
	}
	return &idResolver{authoritative: authoritative, fallbacks: fb}
}

// resolve returns the id and its provenance (the header name it came from). The
// authoritative header wins; otherwise the fallback chain is walked top-down
// and the first present, non-empty, non-sensitive header wins. A candidate for
// which sensitive reports true is treated as absent so a promoted id can never
// resurface a value the operator added to the redaction set. Returns ("", "")
// when nothing matches.
func (r *idResolver) resolve(h http.Header, sensitive func(string) bool) (id, source string) {
	if h == nil {
		return "", ""
	}
	if r.authoritative != "" {
		if id, source = pickHeader(h, r.authoritative, sensitive); id != "" {
			return id, source
		}
	}
	for _, name := range r.fallbacks {
		if id, source = pickHeader(h, name, sensitive); id != "" {
			return id, source
		}
	}
	return "", ""
}
