package admin

import "time"

// MessageEntry is the wire shape of one completed-request record served
// by the admin /api/v1/messages endpoints. Mirrors the
// internal/observability/livefeed.Entry the gateway appends at OnComplete.
//
// The SPA renders one row per entry. Stable field names so the SPA can
// be updated independently of the gateway when the ring grows new
// columns later.
type MessageEntry struct {
	// EventID is the gateway-minted UUID for this entry. Stable row key
	// for the SPA and the eventual /messages/{id} body-fetch endpoint.
	EventID string `json:"event_id"`

	// At is the wall-clock time the request completed (UTC).
	At time.Time `json:"at"`

	// CorrelationID is the gateway request UUID. Joinable to logs and
	// to the X-Sluice-Correlation-Id response header.
	CorrelationID string `json:"correlation_id,omitempty"`

	// SessionID is the resolved session bundle root grouping this request
	// with its whole conversation (including subagent threads);
	// SessionIDSource is the header it came from (the bundle's provenance
	// label). Both omitted when no session header was present.
	SessionID string `json:"session_id,omitempty"`

	SessionIDSource string `json:"session_id_source,omitempty"`

	// ConversationID is the resolved conversation/thread id (the subagent
	// thread when active, else the session); ConversationIDSource is the header
	// it came from. ParentConversationID links a subagent thread back toward
	// its session (omitted for a main agent). These model the unified subagent
	// paradigm; the console nests threads under their session via them.
	ConversationID string `json:"conversation_id,omitempty"`

	ConversationIDSource string `json:"conversation_id_source,omitempty"`

	ParentConversationID string `json:"parent_conversation_id,omitempty"`

	// AgentID is the resolved id of a genuinely named agent (the gen_ai.agent.id
	// home); AgentIDSource is the header it came from. A subagent thread is NOT
	// an agent — those ride ConversationID. Both omitted when no named-agent
	// header was present.
	AgentID string `json:"agent_id,omitempty"`

	AgentIDSource string `json:"agent_id_source,omitempty"`

	// UserID is the resolved end-user id on whose behalf this request was made;
	// UserIDSource is the header it came from. Both omitted when no user header
	// was present.
	UserID string `json:"user_id,omitempty"`

	UserIDSource string `json:"user_id_source,omitempty"`

	// Provider, Protocol, Model carry the routed labels (post rule
	// mutation).
	Provider string `json:"provider,omitempty"`

	Protocol string `json:"protocol,omitempty"`

	Model string `json:"model,omitempty"`

	// Method is the inbound HTTP verb (GET, POST, …). Lets the live pane
	// distinguish a model list from a completion at a glance.
	Method string `json:"method,omitempty"`

	// Configuration is the resolved configuration name. Empty for
	// passthrough requests against an unknown configuration.
	Configuration string `json:"configuration,omitempty"`

	// StatusCode is the HTTP status the client received.
	StatusCode int `json:"status_code"`

	// DurationMs is the end-to-end request duration in milliseconds.
	DurationMs int64 `json:"duration_ms"`

	// Streaming is true iff the response was an SSE stream.
	Streaming bool `json:"streaming,omitempty"`

	// UpstreamError carries the transport-level error string when the
	// upstream tore the connection before headers arrived.
	UpstreamError string `json:"upstream_error,omitempty"`

	// TokensIn / TokensOut / TokensCached / TokensCacheCreation mirror
	// the extracted upstream usage block. Zero when the response
	// carried no usage data (interrupted stream, include_usage off,
	// upstream omitted the block, etc.).
	TokensIn int `json:"tokens_in,omitempty"`

	TokensOut int `json:"tokens_out,omitempty"`

	TokensCached int `json:"tokens_cached,omitempty"`

	TokensCacheCreation int `json:"tokens_cache_creation,omitempty"`

	// Tags is the set of rule-attached tags (AddTagAction) on this
	// request, in first-attach order. Empty when no tagging rule
	// fired.
	Tags []string `json:"tags,omitempty"`

	// RulesMatched lists every rule that fired, in evaluation order.
	RulesMatched []RuleHit `json:"rules_matched,omitempty"`

	// PolicyRef is the resilience policy this request was orchestrated
	// against. Empty for single-shot requests.
	PolicyRef string `json:"policy_ref,omitempty"`

	// Attempts is the per-attempt orchestrator record. Populated only
	// for requests bound to a resilience policy; the SPA renders an
	// expansion table in the modal when len > 1.
	Attempts []AttemptHit `json:"attempts,omitempty"`
}

// AttemptHit is the wire shape of one orchestrator attempt within a
// resilience policy run. Mirrors livefeed.AttemptHit so the SPA can
// render the per-attempt breakdown without dragging the full connector
// Record shape into the admin contract.
type AttemptHit struct {
	// Target is the resilience target name attempted.
	Target string `json:"target"`

	// StartedAt is the orchestrator's wall-clock start time (UTC).
	StartedAt time.Time `json:"started_at"`

	// DurationMs is the per-attempt duration in milliseconds. Zero
	// for cb_blocked attempts.
	DurationMs int64 `json:"duration_ms,omitempty"`

	// StatusCode is the upstream-reported status. Zero for transport
	// errors and cb_blocked.
	StatusCode int `json:"status_code,omitempty"`

	// Error is the transport-level error string when the attempt
	// failed before headers. Empty on success and cb_blocked.
	Error string `json:"error,omitempty"`

	// Outcome categorises the attempt: "success", "failure_status",
	// "transport_error", or "cb_blocked".
	Outcome string `json:"outcome"`
}

// RuleHit is the compact per-rule record carried inside MessageEntry.
// Subset of events.RuleMatched — enough for the SPA to render the chain
// of matches per row without dragging the audit-trail envelope shape
// into the admin contract.
type RuleHit struct {
	// RuleName is the operator-authored rule name.
	RuleName string `json:"rule_name"`

	// ActionsApplied lists the action type discriminators that fired,
	// in execution order.
	ActionsApplied []string `json:"actions_applied,omitempty"`

	// Terminated is true iff a terminating action short-circuited the
	// pipeline.
	Terminated bool `json:"terminated,omitempty"`

	// ErrorMessage carries the human-readable error string when an
	// action failed during apply. Empty on the success path.
	ErrorMessage string `json:"error_message,omitempty"`
}

// MessagesRecentResponse is the JSON shape returned by
// GET /admin/api/v1/messages/recent. Entries are oldest-first so the
// SPA can append them to its in-memory list in arrival order.
type MessagesRecentResponse struct {
	// Capacity is the configured ring capacity (env-derived) at the
	// time of the request. The SPA uses this to surface "showing N of
	// max M" in the pane footer.
	Capacity int `json:"capacity"`

	// Entries holds the up-to-Capacity most recent entries, oldest
	// first. May be empty when the gateway has not handled any
	// requests since startup.
	Entries []MessageEntry `json:"entries"`
}

// SessionTotals is the aggregate rollup over one session's requests, shown
// in the session view's header tiles. An error is any request with a
// client-facing status >= 400. Latency percentiles and cached-token sums are
// intentionally absent — the session view derives those client-side from the
// per-request Requests slice, so no extra aggregation query is needed.
type SessionTotals struct {
	// Requests is the number of requests in the session.
	Requests int `json:"requests"`

	// Errors is the count of requests with status_code >= 400.
	Errors int `json:"errors"`

	// TokensIn / TokensOut sum the per-request usage across the session.
	TokensIn int64 `json:"tokens_in"`

	TokensOut int64 `json:"tokens_out"`
}

// SessionView is the JSON shape returned by GET /api/v1/sessions/{id}: every
// request in the session as a rich MessageEntry plus the aggregate Totals.
//
// Requests are oldest-first so the session graphs (cumulative tokens, the
// per-turn timeline) can plot the series directly; the messages table reverses
// to newest-first client-side. Same per-row shape as the messages browser, so
// the session view reuses the messages row + GenAI inspector unchanged.
type SessionView struct {
	// SessionID is the conversation/bundle id these requests share.
	SessionID string `json:"session_id"`

	// Totals is the header-tile rollup over Requests.
	Totals SessionTotals `json:"totals"`

	// Requests is every request in the session, oldest-first.
	Requests []MessageEntry `json:"requests"`
}

// SessionSummary is one row of the session-discovery list returned by
// GET /api/v1/sessions: a session's id plus the rollup the table renders. When
// the request carries tag/configuration filters the aggregates cover only the
// matching requests, so Messages/TotalTokens/Models and the StartedAt/
// LastActivity bounds reflect that subset of the session.
type SessionSummary struct {
	// SessionID is the conversation/bundle id the requests share.
	SessionID string `json:"session_id"`

	// Messages is the number of (matching) requests in the session.
	Messages int `json:"messages"`

	// Subagents is the number of distinct child conversation threads in the
	// session (subagents spawned). 0 for a plain single-agent session.
	Subagents int `json:"subagents"`

	// TotalTokens is the summed tokens_in+tokens_out across those requests.
	TotalTokens int64 `json:"total_tokens"`

	// Models is the distinct requested model names (empty strings excluded).
	Models []string `json:"models"`

	// Tags is the distinct post-rule tag set applied across the matching requests
	// in the session (empty strings excluded). Empty when no rule tagged them.
	Tags []string `json:"tags"`

	// StartedAt / LastActivity are the first and last request times — the
	// session's real bounds, which may fall outside the query window.
	StartedAt    time.Time `json:"started_at"`
	LastActivity time.Time `json:"last_activity"`
}

// SessionList is the JSON shape returned by GET /api/v1/sessions: a keyset page
// of session summaries (newest activity first) plus the cursor for the next
// page. NextCursor is empty on the last page.
type SessionList struct {
	// Sessions is the page of session summaries, ordered by last activity desc.
	Sessions []SessionSummary `json:"sessions"`

	// NextCursor advances to the next page; empty means the last page.
	NextCursor string `json:"next_cursor"`
}

// MessageBodyDetail is the JSON shape returned by
// GET /admin/api/v1/messages/{event_id}/body. Holds the captured
// request and response bodies plus, for streamed responses, the
// per-provider accumulator's assembled text + structured tool calls.
//
// Body fields are raw UTF-8 strings — JSON, SSE, plain text all
// round-trip as-is. Operators inspecting binary payloads (rare here)
// see invalid UTF-8 replacement characters; not worth a separate
// base64 encoding for the live-tail use case.
type MessageBodyDetail struct {
	// EventID is the gateway-minted UUID this body belongs to.
	EventID string `json:"event_id"`

	// Request is the inbound request body bytes captured at the
	// gateway's edge.
	Request string `json:"request,omitempty"`

	// RequestTotalBytes is the size of the request body as the
	// client sent it. Equal to len(Request) when not truncated.
	RequestTotalBytes int64 `json:"request_total_bytes"`

	// RequestTruncated is true when the request exceeded the
	// per-body cap and Request holds only the head bytes.
	RequestTruncated bool `json:"request_truncated,omitempty"`

	// Response is the outbound response body bytes as they left the
	// gateway. For streamed responses, these are the raw SSE event
	// bytes; for non-streamed, the full JSON.
	Response string `json:"response,omitempty"`

	// ResponseTotalBytes is the size of the response body as the
	// gateway emitted it.
	ResponseTotalBytes int64 `json:"response_total_bytes"`

	// ResponseTruncated is true when the response exceeded the
	// per-body cap and Response holds only the head bytes.
	ResponseTruncated bool `json:"response_truncated,omitempty"`

	// ResponseAssembled is the JSON-encoded reconstruction of the
	// response the provider would have returned non-streaming, built
	// by the per-provider accumulator from the streamed chunks. Shape
	// matches the provider's non-streaming response type (OpenAI
	// ChatCompletion, Anthropic MessagesResponse, Gemini
	// GenerateContentResponse). Empty for non-streaming responses and
	// for streams the accumulator could not parse.
	ResponseAssembled string `json:"response_assembled,omitempty"`

	// AssemblyPartial is true when the accumulator hit a malformed
	// chunk or unknown delta type mid-stream and could not complete
	// reassembly. ResponseAssembled holds whatever was parseable up
	// to that point.
	AssemblyPartial bool `json:"assembly_partial,omitempty"`

	// RequestHeaders is the inbound HTTP header snapshot with
	// credential-bearing values redacted server-side. Wire shape is
	// {name: [value, ...]} to preserve multi-value headers without
	// pulling http.Header into the SPA's typing.
	RequestHeaders map[string][]string `json:"request_headers,omitempty"`

	// ResponseHeaders is the outbound HTTP header snapshot, taken just
	// before the gateway wrote the response status, redacted with the
	// same rules as RequestHeaders.
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`

	// GenAIContent is the bounded gen_ai.* content captured for this
	// request (system instructions, the latest input turn, the model
	// output, tool definitions) as a JSON object, when content capture
	// was enabled on the gateway. Empty otherwise. Sourced from the
	// request_events.gen_ai_content column, not a payload row.
	GenAIContent string `json:"gen_ai_content,omitempty"`

	// SpanEvent is the COMPLETE OTel gen_ai span as captured — every
	// attribute the gateway emitted (model, provider, usage, request
	// params, response metadata, sluice.* facts, server/service, the
	// gen_ai content) as a raw JSON object. The console's raw telemetry
	// pane renders this verbatim so operators see the whole span, not just
	// the gen_ai content. Sourced from the request_events.span_event
	// column; empty when the request had no OTLP span (record-only).
	SpanEvent string `json:"span_event,omitempty"`
}
