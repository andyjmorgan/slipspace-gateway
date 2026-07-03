package connector

import "encoding/json"

// SchemaVersion is the wire-format version every Record emitted by this
// build carries. Bumps are additive-only — see the package docs.
//
// v2 added the additive SessionID + SessionIDSource fields (session
// bundling). v3 added the additive AgentID + AgentIDSource fields (agent
// identification). v4 added the additive UserID + UserIDSource fields (end-user
// identification). v5 added the additive charge-accounting fields: the
// Tokens sub-buckets (Reasoning, CacheCreation5m/1h, InputAudio/
// OutputAudio) plus ServerToolUse, ServiceTier, and InferenceGeo on the
// record. Older consumers reading a newer record simply ignore the new
// keys; the change requires no migration.
const SchemaVersion = 5

// Record is one captured request/response pair as it sits inside an
// ndjson.zst batch. Consumers sort by (TsNs, InstanceID, Seq) and group by
// CorrelationID.
//
// Body bytes are mutually exclusive between [RequestPart.Body] (inline) and
// [RequestPart.BodyRef] (URL to an out-of-line blob). [RequestPart.BodyOmitted]
// is set when the body exceeded the binding's max_body_bytes and the
// oversize_behaviour was metadata_only. Same shape for [ResponsePart].
type Record struct {
	// V is the envelope schema version. Always 1 today.
	V int `json:"v"`

	// ID is a UUID (uuid.NewString) minted per request when the record is
	// built, not at segment seal. Used as the dedupe key by consumers that
	// see retried deliveries.
	ID string `json:"id"`

	// TsNs is the request start time in nanoseconds since the Unix epoch.
	// Primary sort key.
	TsNs int64 `json:"ts_ns"`

	// Seq is the per-gateway-instance monotonic counter used as the
	// tiebreaker when TsNs collides across records from the same instance.
	Seq uint64 `json:"seq"`

	// InstanceID identifies the gateway instance, populated from
	// os.Hostname() (the pod name under Kubernetes). Distinguishes records
	// from concurrent replicas with overlapping wall-clock times.
	InstanceID string `json:"instance_id"`

	// CorrelationID groups records that belong to one logical request
	// (initial + retries + tool follow-ups). Stable across the request's
	// lifetime.
	CorrelationID string `json:"correlation_id"`

	// SessionID is the resolved session bundle root — the stable id grouping
	// every request of one conversation, including all of its subagent threads,
	// one level above CorrelationID. Resolved from Session-Id (Codex) /
	// X-Claude-Code-Session-Id (Claude Code). Empty when no session header was
	// present. Consumers bundle on the (Configuration, SessionID) tuple, never
	// the bare SessionID, since client-controlled ids can collide across
	// configurations.
	SessionID string `json:"session_id,omitempty"`

	// SessionIDSource is the header name SessionID was resolved from
	// (e.g. "X-Slipspace-Session-Id", "Session-Id") — the provenance the
	// console uses to label a bundle. Empty when SessionID is empty.
	SessionIDSource string `json:"session_id_source,omitempty"`

	// ConversationID is the resolved conversation/thread id — the
	// most-specific thread the request belongs to: equal to SessionID for a
	// main agent, or a distinct subagent thread when one is active. Resolved
	// from Thread-Id (Codex) / X-Claude-Code-Agent-Id (Claude Code). This is
	// the value emitted as gen_ai.conversation.id. Empty when no thread header
	// was present.
	ConversationID string `json:"conversation_id,omitempty"`

	// ConversationIDSource is the header name ConversationID was resolved from
	// (e.g. "Thread-Id", "X-Claude-Code-Agent-Id"). Empty when ConversationID
	// is empty.
	ConversationIDSource string `json:"conversation_id_source,omitempty"`

	// ParentConversationID is the parent of a subagent thread — the hierarchy
	// edge linking ConversationID back toward its session. Set only when the
	// conversation is a subagent thread (distinct from SessionID); empty for a
	// main agent. Codex supplies it explicitly via X-Codex-Parent-Thread-Id;
	// otherwise it is the SessionID.
	ParentConversationID string `json:"parent_conversation_id,omitempty"`

	// AgentID is the resolved id of a genuinely NAMED agent (the gen_ai.agent.id
	// semconv home, paired with agent.name/description), resolved only from the
	// authoritative X-Slipspace-Agent-Id. NOT a subagent thread — those ride
	// ConversationID. Empty when no named-agent header was present.
	AgentID string `json:"agent_id,omitempty"`

	// AgentIDSource is the header name AgentID was resolved from (e.g.
	// "X-Slipspace-Agent-Id") — the provenance the console uses to label the
	// agent. Empty when AgentID is empty.
	AgentIDSource string `json:"agent_id_source,omitempty"`

	// UserID is the resolved end-user id on whose behalf the request was made —
	// orthogonal to SessionID and AgentID. Empty when no user header was
	// present.
	UserID string `json:"user_id,omitempty"`

	// UserIDSource is the header name UserID was resolved from (e.g.
	// "X-Slipspace-User-Id") — the provenance the console uses to label the user.
	// Empty when UserID is empty.
	UserIDSource string `json:"user_id_source,omitempty"`

	// Configuration is the resolved configuration name (e.g. "production").
	Configuration string `json:"configuration"`

	// APIKeyName is the human-readable name of the API key that authorised
	// the request (managed mode) or "passthrough" when in passthrough mode.
	APIKeyName string `json:"api_key_name,omitempty"`

	// Provider is the resolved (post-rule) upstream provider name.
	Provider string `json:"provider"`

	// Protocol is the resolved (post-rule) protocol on the provider (e.g.
	// "chat", "messages"), or the passthrough family name for opaque
	// requests (e.g. "messages_batches").
	Protocol string `json:"protocol"`

	// Model is the resolved (post-rule) model name. May be empty for
	// protocols that don't carry a model (e.g. /v1/models).
	Model string `json:"model,omitempty"`

	// Tags accumulated by addTag rule actions during evaluation.
	Tags []string `json:"tags,omitempty"`

	// Request captures the request as it left the client.
	Request RequestPart `json:"request"`

	// Response captures what the client received.
	Response ResponsePart `json:"response"`

	// Tokens records input/output token counts when the provider returned
	// usage info. Nil when usage is not parseable.
	Tokens *Tokens `json:"tokens,omitempty"`

	// ServerToolUse counts server-executed tool invocations, keyed by the
	// provider's own wire vocabulary (Anthropic web_search_requests,
	// OpenAI web_search_call, Gemini web_search_queries, ...). These bill
	// per call/query outside the token buckets. Nil when the request
	// invoked no server tools.
	ServerToolUse map[string]int `json:"server_tool_use,omitempty"`

	// ServiceTier is the provider-reported processing tier the request
	// was billed under (OpenAI/Anthropic service_tier) — a whole-request
	// pricing multiplier. Empty when the provider reported none.
	ServiceTier string `json:"service_tier,omitempty"`

	// InferenceGeo is Anthropic's usage.inference_geo — the inference
	// region, which carries its own pricing multiplier. Empty for other
	// providers.
	InferenceGeo string `json:"inference_geo,omitempty"`

	// RulesFired is the ordered list of rules whose conditions matched.
	RulesFired []RuleFired `json:"rules_fired,omitempty"`

	// UpstreamStatus is the HTTP status the provider returned (may differ
	// from the status the client saw if a rule rewrote it).
	UpstreamStatus int `json:"upstream_status,omitempty"`

	// UpstreamError describes a transport-layer failure talking to the
	// upstream (DNS, TLS, timeout). Empty when the request reached the
	// provider, regardless of provider-side errors.
	UpstreamError string `json:"upstream_error,omitempty"`

	// PolicyRef is the name of the resilience policy that orchestrated
	// this request, when one was attached. Empty for single-shot
	// requests that bypassed the orchestrator.
	PolicyRef string `json:"policy_ref,omitempty"`

	// Attempts is the per-attempt outcome record for multi-target
	// resilience runs (failover walk-down, load-balance pool, cb-block
	// skips). Empty for single-shot.
	Attempts []Attempt `json:"attempts,omitempty"`

	// SchemaVersion is the per-record version. Always equals [SchemaVersion].
	SchemaVersion int `json:"schema_version"`
}

// RequestPart is the request half of a [Record].
type RequestPart struct {
	Method string `json:"method"`

	Path string `json:"path"`

	// Headers is a flat single-value-per-key snapshot of the request
	// headers as they entered the gateway. The upstream-credential header
	// the gateway minted on the way out is never present here.
	Headers map[string]string `json:"headers,omitempty"`

	// BodySha256 is the hex SHA-256 of the captured body bytes. Set even
	// when Body is omitted or by-reference, so consumers can verify
	// identity later.
	BodySha256 string `json:"body_sha256,omitempty"`

	// BodyBytes is the uncompressed body length in bytes. Populated even
	// when the body itself is omitted.
	BodyBytes int `json:"body_bytes,omitempty"`

	// Body is the inline body (raw JSON or string). Mutually exclusive
	// with BodyRef and BodyOmitted.
	Body json.RawMessage `json:"body,omitempty"`

	// BodyRef is a URL to an out-of-line blob (used by S3 / Azure
	// destinations when body exceeds the inline threshold). Mutually
	// exclusive with Body and BodyOmitted.
	BodyRef string `json:"body_ref,omitempty"`

	// BodyOmitted is true when the body exceeded max_body_bytes and the
	// binding's oversize_behaviour is metadata_only. Body and BodyRef are
	// both empty in this case.
	BodyOmitted bool `json:"body_omitted,omitempty"`
}

// ResponsePart is the response half of a [Record].
type ResponsePart struct {
	// Status is the HTTP status the client received.
	Status int `json:"status"`

	Headers map[string]string `json:"headers,omitempty"`

	BodySha256 string `json:"body_sha256,omitempty"`

	BodyBytes int `json:"body_bytes,omitempty"`

	Body json.RawMessage `json:"body,omitempty"`

	BodyRef string `json:"body_ref,omitempty"`

	BodyOmitted bool `json:"body_omitted,omitempty"`

	// FirstByteNs is the wall-clock time the first response byte was
	// written to the client. For non-streaming responses, equals the time
	// the full body was flushed.
	FirstByteNs int64 `json:"first_byte_ns,omitempty"`

	// LastByteNs is the wall-clock time the response stream completed.
	LastByteNs int64 `json:"last_byte_ns,omitempty"`

	// StreamChunks counts the number of SSE chunks observed. Zero for
	// non-streaming responses.
	StreamChunks int `json:"stream_chunks,omitempty"`

	// Assembled is the JSON-encoded reconstruction of a streamed
	// response, produced by the gateway's per-provider accumulator from
	// the SSE chunks — the same rollup the admin live-feed renders. Its
	// shape matches the provider's non-streaming response type. Empty for
	// non-streaming responses and for streams no accumulator recognised.
	// This is what lets the telemetry inspector show the assembled
	// response instead of only the raw SSE bytes (which ride in Body).
	Assembled json.RawMessage `json:"assembled,omitempty"`

	// AssemblyPartial is true when the accumulator hit a malformed chunk
	// or unknown delta mid-stream and could not finish reassembly;
	// Assembled then holds whatever parsed up to that point.
	AssemblyPartial bool `json:"assembly_partial,omitempty"`
}

// Tokens carries usage-style accounting parsed from the upstream response.
type Tokens struct {
	Input int `json:"input"`

	Output int `json:"output"`

	// Cached counts input tokens served from a provider-side prompt
	// cache. Anthropic surfaces this via `cache_read_input_tokens`;
	// OpenAI returns it under `prompt_tokens_details.cached_tokens`.
	// Zero when the response doesn't carry cache info.
	Cached int `json:"cached,omitempty"`

	// CacheCreation counts input tokens written to the prompt cache
	// by this request. Anthropic-only field
	// (cache_creation_input_tokens); zero otherwise.
	CacheCreation int `json:"cache_creation,omitempty"`

	// CacheCreation5m / CacheCreation1h split CacheCreation by cache
	// TTL (Anthropic's nested cache_creation breakdown). The tiers
	// bill at different write premiums, so costing needs the split.
	// Zero when the provider reported only the flat total; when
	// present they sum to CacheCreation.
	CacheCreation5m int `json:"cache_creation_5m,omitempty"`
	CacheCreation1h int `json:"cache_creation_1h,omitempty"`

	// InputAudio / OutputAudio are the audio-modality shares of
	// Input / Output, billed at audio rates where the provider prices
	// audio separately. Sub-buckets — already counted in the gross
	// totals.
	InputAudio  int `json:"input_audio,omitempty"`
	OutputAudio int `json:"output_audio,omitempty"`

	// Reasoning is the reasoning/thinking share of Output (OpenAI
	// reasoning_tokens, Anthropic thinking_tokens, Gemini
	// thoughtsTokenCount). Informational — billed inside Output on
	// every current provider.
	Reasoning int `json:"reasoning,omitempty"`
}

// Attempt is one entry in [Record.Attempts] when the request ran
// under a resilience policy. Mirrors the events.AttemptRecord shape
// the in-process resilience orchestrator builds.
type Attempt struct {
	// Target is the resilience policy target name attempted.
	Target string `json:"target"`

	// StartedAtNs is the wall-clock UTC start of the attempt in
	// nanoseconds since the Unix epoch. Same encoding as
	// [Record.TsNs].
	StartedAtNs int64 `json:"started_at_ns"`

	// DurationMs is the orchestrator-measured wall-clock duration of
	// the attempt in milliseconds. Zero for cb_blocked entries.
	DurationMs int64 `json:"duration_ms,omitempty"`

	// StatusCode is the upstream-reported HTTP status. Zero on
	// transport-error / cb-blocked.
	StatusCode int `json:"status_code,omitempty"`

	// Error is the transport-level error message when the attempt
	// failed before headers.
	Error string `json:"error,omitempty"`

	// Outcome is one of "success", "failure_status",
	// "transport_error", "cb_blocked".
	Outcome string `json:"outcome"`
}

// RuleFired records one rule that matched during evaluation. The ordered
// slice on [Record.RulesFired] preserves evaluation order.
type RuleFired struct {
	Name string `json:"name"`

	// TookUs is the rule's condition-evaluation cost in microseconds.
	// Useful for spotting hot rules; not required by consumers.
	TookUs int64 `json:"took_us,omitempty"`

	// ActionsApplied lists the action types that fired for this match
	// (e.g. ["setHeader", "changeProvider"]). Useful for downstream
	// audit pipelines that group by intent rather than rule name.
	ActionsApplied []string `json:"actions_applied,omitempty"`

	// Terminated is true when this rule's action chain stopped further
	// rule evaluation (terminating actions like returnStatusCode or
	// llmImpersonation).
	Terminated bool `json:"terminated,omitempty"`

	// ErrorMessage carries the failure detail when a rule's action
	// errored at apply time. Empty on the success path.
	ErrorMessage string `json:"error_message,omitempty"`
}
