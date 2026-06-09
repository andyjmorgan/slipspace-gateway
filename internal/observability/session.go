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

// DefaultSessionIDHeaders is the shipped fallback chain for the session
// bundle (the stable root that groups every request of a conversation,
// including its subagents), walked in order after the authoritative Sluice
// header.
//
// Codex emits Session-Id (verified live; hyphenated, NOT the underscore
// Session_id we previously and wrongly chased) — the root session shared
// across all subagent threads. Claude Code uses X-Claude-Code-Session-Id.
// The per-turn thread/subagent id is a SEPARATE axis (the conversation),
// resolved by ConversationResolver — not part of this chain.
var DefaultSessionIDHeaders = []string{
	"Session-Id",
	"X-Claude-Code-Session-Id",
}

// SessionResolver resolves the session bundle id from inbound request
// headers. The Sluice header is always attempted first; operator-configured
// fallbacks follow the shipped defaults, in the order supplied.
type SessionResolver struct{ *idResolver }

// NewSessionResolver builds a resolver whose fallback chain is the shipped
// DefaultSessionIDHeaders followed by extra — operator-supplied custom
// headers, kept in the order given. The Sluice header is authoritative
// and need not appear in extra. Blank entries are dropped.
func NewSessionResolver(extra []string) *SessionResolver {
	return &SessionResolver{newIDResolver(SluiceSessionHeader, DefaultSessionIDHeaders, extra)}
}

// Resolve returns the session id and its provenance (the header name it
// came from, which the console uses to label the bundle). The Sluice
// header wins; otherwise the fallback chain is walked top-down and the
// first present, non-empty, non-sensitive header wins. Returns ("", "")
// when nothing matches.
func (s *SessionResolver) Resolve(h http.Header, sensitive func(string) bool) (id, source string) {
	return s.resolve(h, sensitive)
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
