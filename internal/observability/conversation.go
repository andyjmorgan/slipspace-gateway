package observability

import (
	"context"
	"net/http"
)

// Conversation/thread + parent resolution and context plumbing.
//
// A conversation (the gen_ai semconv home for "session, thread") is the
// most-specific thread a request belongs to: the session itself for a main
// agent, or a distinct subagent thread when one is active. It sits between the
// session bundle (SessionResolver) and the per-request CorrelationID.
//
// Codex (verified live) carries Thread-Id on every request — equal to its
// Session-Id for the main agent, a distinct value for a spawned subagent —
// plus X-Codex-Parent-Thread-Id pointing at the parent thread on subagent
// requests. Claude Code carries the subagent instance on X-Claude-Code-Agent-Id
// (an opaque per-invocation id, NOT a named agent, so it belongs here on the
// conversation axis rather than squatting on gen_ai.agent.id), with the session
// as its implicit parent.
//
// The combination rule (applied by the correlation middleware): the resolved
// conversation is the thread when present, else the session; the parent is the
// explicit parent header when present, else the session when the thread differs
// from it. When thread == session (main agent) the request has no parent.

// SluiceThreadHeader is the authoritative conversation/thread header. When a
// client or proxy deliberately sets it, it wins over any ambient client header.
const SluiceThreadHeader = "X-Sluice-Thread-Id"

// DefaultThreadIDHeaders is the shipped fallback chain for the conversation/
// thread id, walked after the authoritative Sluice header. Codex uses
// Thread-Id; Claude Code uses X-Claude-Code-Agent-Id (its subagent instance).
var DefaultThreadIDHeaders = []string{
	"Thread-Id",
	"X-Claude-Code-Agent-Id",
}

// SluiceParentHeader is the authoritative parent-conversation header.
const SluiceParentHeader = "X-Sluice-Parent-Conversation-Id"

// DefaultParentIDHeaders is the shipped fallback chain for the parent
// conversation id. Codex emits X-Codex-Parent-Thread-Id on subagent requests.
var DefaultParentIDHeaders = []string{
	"X-Codex-Parent-Thread-Id",
}

// ConversationResolver resolves the conversation/thread id from inbound
// headers. The Sluice header is attempted first; operator-configured fallbacks
// follow the shipped defaults, in the order supplied.
type ConversationResolver struct{ *idResolver }

// NewConversationResolver builds a resolver whose fallback chain is the shipped
// DefaultThreadIDHeaders followed by extra. Blank entries are dropped.
func NewConversationResolver(extra []string) *ConversationResolver {
	return &ConversationResolver{newIDResolver(SluiceThreadHeader, DefaultThreadIDHeaders, extra)}
}

// Resolve returns the conversation/thread id and the header it came from.
// Returns ("", "") when nothing matches.
func (c *ConversationResolver) Resolve(h http.Header, sensitive func(string) bool) (id, source string) {
	return c.resolve(h, sensitive)
}

// ParentResolver resolves the parent-conversation id from inbound headers.
type ParentResolver struct{ *idResolver }

// NewParentResolver builds a resolver whose fallback chain is the shipped
// DefaultParentIDHeaders followed by extra. Blank entries are dropped.
func NewParentResolver(extra []string) *ParentResolver {
	return &ParentResolver{newIDResolver(SluiceParentHeader, DefaultParentIDHeaders, extra)}
}

// Resolve returns the parent-conversation id and the header it came from.
// Returns ("", "") when nothing matches.
func (p *ParentResolver) Resolve(h http.Header, sensitive func(string) bool) (id, source string) {
	return p.resolve(h, sensitive)
}

type conversationIDKey struct{}

type conversationSourceKey struct{}

type parentConversationIDKey struct{}

// WithConversationID stores the resolved conversation/thread id and its source
// header on ctx. An empty id leaves ctx unchanged.
func WithConversationID(ctx context.Context, id, source string) context.Context {
	if id == "" {
		return ctx
	}
	ctx = context.WithValue(ctx, conversationIDKey{}, id)
	if source != "" {
		ctx = context.WithValue(ctx, conversationSourceKey{}, source)
	}
	return ctx
}

// ConversationIDFromContext returns the resolved conversation/thread id, or "".
func ConversationIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(conversationIDKey{}).(string); ok {
		return id
	}
	return ""
}

// ConversationIDSourceFromContext returns the header the conversation id was
// resolved from, or "".
func ConversationIDSourceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if s, ok := ctx.Value(conversationSourceKey{}).(string); ok {
		return s
	}
	return ""
}

// WithParentConversationID stores the resolved parent-conversation id on ctx.
// An empty id leaves ctx unchanged.
func WithParentConversationID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, parentConversationIDKey{}, id)
}

// ParentConversationIDFromContext returns the resolved parent-conversation id,
// or "".
func ParentConversationIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(parentConversationIDKey{}).(string); ok {
		return id
	}
	return ""
}
