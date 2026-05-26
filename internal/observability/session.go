package observability

import (
	"context"
	"net/http"
	"strings"
)

// Session-id resolution and context plumbing. A session (also: bundle or
// conversation) groups the many HTTP requests of one agent conversation
// under a single client-supplied identifier — one level above the
// per-request CorrelationID. Resolution is Sluice-first, then a configured
// fallback chain walked top-down. See the design note "Correlating
// Requests Across Turns (Session Bundling)".

// SluiceSessionHeader is the authoritative session header. When a client
// or proxy deliberately sets it, it wins over any ambient client header,
// so it is always tried before the fallback chain.
const SluiceSessionHeader = "X-Sluice-Session-Id"

// DefaultSessionIDHeaders is the shipped fallback chain, walked in order
// after the authoritative Sluice header. Codex exposes Thread_id (the
// durable conversation id) with Session_id carrying the same UUID as a
// fallback; Claude Code uses x-claude-code-session-id.
//
// Underscore header names (Codex's Thread_id / Session_id) survive Go's
// net/http and Traefik but are stripped by nginx-class proxies by
// default; that is an infra requirement, not a gateway concern.
var DefaultSessionIDHeaders = []string{
	"Thread_id",
	"Session_id",
	"x-claude-code-session-id",
}

// SessionResolver resolves a session id from inbound request headers. The
// Sluice header is always attempted first; operator-configured fallbacks
// follow the shipped defaults, in the order supplied.
type SessionResolver struct {
	// fallbacks is the ordered fallback chain (shipped defaults plus
	// operator extras). The Sluice header is implicit and always first.
	fallbacks []string
}

// NewSessionResolver builds a resolver whose fallback chain is the shipped
// DefaultSessionIDHeaders followed by extra — operator-supplied custom
// headers, kept in the order given. The Sluice header is authoritative
// and need not appear in extra. Blank entries are dropped.
func NewSessionResolver(extra []string) *SessionResolver {
	fb := make([]string, 0, len(DefaultSessionIDHeaders)+len(extra))
	fb = append(fb, DefaultSessionIDHeaders...)
	for _, e := range extra {
		if e = strings.TrimSpace(e); e != "" {
			fb = append(fb, e)
		}
	}
	return &SessionResolver{fallbacks: fb}
}

// Resolve returns the session id and its provenance (the header name it
// came from, which the console uses to label the bundle). The Sluice
// header wins; otherwise the fallback chain is walked top-down and the
// first present, non-empty header wins.
//
// A candidate for which sensitive reports true is treated as absent and
// resolution falls through to the next — so a promoted session id can
// never resurface a value the operator added to the redaction set.
// Returns ("", "") when nothing matches.
func (s *SessionResolver) Resolve(h http.Header, sensitive func(string) bool) (id, source string) {
	if h == nil {
		return "", ""
	}
	if id, source = pickHeader(h, SluiceSessionHeader, sensitive); id != "" {
		return id, source
	}
	for _, name := range s.fallbacks {
		if id, source = pickHeader(h, name, sensitive); id != "" {
			return id, source
		}
	}
	return "", ""
}

// pickHeader returns the trimmed value of name and name itself when the
// header is present, non-empty, and not redaction-sensitive; otherwise
// ("", "").
func pickHeader(h http.Header, name string, sensitive func(string) bool) (string, string) {
	if sensitive != nil && sensitive(name) {
		return "", ""
	}
	if v := strings.TrimSpace(h.Get(name)); v != "" {
		return v, name
	}
	return "", ""
}

type sessionIDKey struct{}

type sessionSourceKey struct{}

// WithSessionID stores the resolved session id and its source header on
// ctx. An empty id leaves ctx unchanged so callers can pipe a resolution
// result through unconditionally.
func WithSessionID(ctx context.Context, id, source string) context.Context {
	if id == "" {
		return ctx
	}
	ctx = context.WithValue(ctx, sessionIDKey{}, id)
	if source != "" {
		ctx = context.WithValue(ctx, sessionSourceKey{}, source)
	}
	return ctx
}

// SessionIDFromContext returns the resolved session id on ctx, or "".
func SessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(sessionIDKey{}).(string); ok {
		return id
	}
	return ""
}

// SessionIDSourceFromContext returns the header name the session id was
// resolved from, or "".
func SessionIDSourceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if s, ok := ctx.Value(sessionSourceKey{}).(string); ok {
		return s
	}
	return ""
}
