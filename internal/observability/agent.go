package observability

import (
	"context"
	"net/http"
	"strings"
)

// Agent-id resolution and context plumbing. An agent id identifies the agent
// (or sub-agent) making a request — one axis below the session/conversation
// that groups a turn's requests. Resolution mirrors session id: Sluice-first,
// then a configured fallback chain walked top-down. See the design note
// "Correlating Requests Across Turns (Session Bundling)" → Agent id.

// SluiceAgentHeader is the authoritative agent header. When a client or proxy
// deliberately sets it, it wins over any ambient client header, so it is
// always tried before the fallback chain.
const SluiceAgentHeader = "X-Sluice-Agent-Id"

// DefaultAgentIDHeaders is the shipped fallback chain, walked in order after
// the authoritative Sluice header. Claude Code identifies the acting agent
// with X-Claude-Code-Agent-Id; operators bundle additional client headers via
// SLUICE_AGENT_ID_HEADERS.
var DefaultAgentIDHeaders = []string{
	"X-Claude-Code-Agent-Id",
}

// AgentResolver resolves an agent id from inbound request headers. The Sluice
// header is always attempted first; operator-configured fallbacks follow the
// shipped defaults, in the order supplied.
type AgentResolver struct {
	// fallbacks is the ordered fallback chain (shipped defaults plus
	// operator extras). The Sluice header is implicit and always first.
	fallbacks []string
}

// NewAgentResolver builds a resolver whose fallback chain is the shipped
// DefaultAgentIDHeaders followed by extra — operator-supplied custom headers,
// kept in the order given. The Sluice header is authoritative and need not
// appear in extra. Blank entries are dropped.
func NewAgentResolver(extra []string) *AgentResolver {
	fb := make([]string, 0, len(DefaultAgentIDHeaders)+len(extra))
	fb = append(fb, DefaultAgentIDHeaders...)
	for _, e := range extra {
		if e = strings.TrimSpace(e); e != "" {
			fb = append(fb, e)
		}
	}
	return &AgentResolver{fallbacks: fb}
}

// Resolve returns the agent id and its provenance (the header name it came
// from, which the console uses to label the value). The Sluice header wins;
// otherwise the fallback chain is walked top-down and the first present,
// non-empty header wins.
//
// A candidate for which sensitive reports true is treated as absent and
// resolution falls through to the next — so a promoted agent id can never
// resurface a value the operator added to the redaction set. Returns ("", "")
// when nothing matches. Shares pickHeader with the session resolver.
func (a *AgentResolver) Resolve(h http.Header, sensitive func(string) bool) (id, source string) {
	if h == nil {
		return "", ""
	}
	if id, source = pickHeader(h, SluiceAgentHeader, sensitive); id != "" {
		return id, source
	}
	for _, name := range a.fallbacks {
		if id, source = pickHeader(h, name, sensitive); id != "" {
			return id, source
		}
	}
	return "", ""
}

type agentIDKey struct{}

type agentSourceKey struct{}

// WithAgentID stores the resolved agent id and its source header on ctx. An
// empty id leaves ctx unchanged so callers can pipe a resolution result
// through unconditionally.
func WithAgentID(ctx context.Context, id, source string) context.Context {
	if id == "" {
		return ctx
	}
	ctx = context.WithValue(ctx, agentIDKey{}, id)
	if source != "" {
		ctx = context.WithValue(ctx, agentSourceKey{}, source)
	}
	return ctx
}

// AgentIDFromContext returns the resolved agent id on ctx, or "".
func AgentIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(agentIDKey{}).(string); ok {
		return id
	}
	return ""
}

// AgentIDSourceFromContext returns the header name the agent id was resolved
// from, or "".
func AgentIDSourceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if s, ok := ctx.Value(agentSourceKey{}).(string); ok {
		return s
	}
	return ""
}
