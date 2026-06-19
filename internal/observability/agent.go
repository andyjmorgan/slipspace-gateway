package observability

import (
	"context"
	"net/http"
	"strings"
)

// Agent-id resolution and context plumbing. An agent id identifies the agent
// (or sub-agent) making a request — one axis below the session/conversation
// that groups a turn's requests. Resolution mirrors session id: SlipSpace-first,
// then a configured fallback chain walked top-down. See the design note
// "Correlating Requests Across Turns (Session Bundling)" → Agent id.

// SlipSpaceAgentHeader is the authoritative agent header. When a client or proxy
// deliberately sets it, it wins over any ambient client header, so it is
// always tried before the fallback chain.
const SlipSpaceAgentHeader = "X-Slipspace-Agent-Id"

// DefaultAgentIDHeaders is the shipped fallback chain, walked in order after
// the authoritative SlipSpace header. It is intentionally EMPTY: gen_ai.agent.id
// is reserved for a genuinely named agent (the semconv pairs it with
// agent.name / agent.description), so only the authoritative X-Slipspace-Agent-Id
// — or an operator-supplied SLIPSPACE_AGENT_ID_HEADERS entry that truly names an
// agent — feeds it.
//
// X-Claude-Code-Agent-Id was previously here, but its values are opaque
// per-invocation instance ids (one per session), not named agents — the same
// instance-id-on-agent.id abuse we reject for Codex's Thread-Id. It now feeds
// the conversation/thread axis (DefaultThreadIDHeaders) instead, so the
// subagent is modelled coherently across clients and gen_ai.agent.id is freed.
var DefaultAgentIDHeaders = []string{}

// AgentResolver resolves an agent id from inbound request headers. The SlipSpace
// header is always attempted first; operator-configured fallbacks follow the
// shipped defaults, in the order supplied.
type AgentResolver struct {
	// fallbacks is the ordered fallback chain (shipped defaults plus
	// operator extras). The SlipSpace header is implicit and always first.
	fallbacks []string
}

// NewAgentResolver builds a resolver whose fallback chain is the shipped
// DefaultAgentIDHeaders followed by extra — operator-supplied custom headers,
// kept in the order given. The SlipSpace header is authoritative and need not
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
// from, which the console uses to label the value). The SlipSpace header wins;
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
	if id, source = pickHeader(h, SlipSpaceAgentHeader, sensitive); id != "" {
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
