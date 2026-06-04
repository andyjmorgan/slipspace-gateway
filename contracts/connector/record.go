package connector

import "encoding/json"

// SchemaVersion is the wire-format version every Record emitted by this
// build carries. Bumps are additive-only — see the package docs.
//
// v2 added the additive SessionID + SessionIDSource fields (session
// bundling). Older consumers reading a v2 record simply ignore the new
// keys; the change requires no migration.
const SchemaVersion = 2

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

	// ID is a ULID generated when the record is sealed into a segment.
	// Used as the dedupe key by consumers that see retried deliveries.
	ID string `json:"id"`

	// TsNs is the request start time in nanoseconds since the Unix epoch.
	// Primary sort key.
	TsNs int64 `json:"ts_ns"`

	// Seq is the per-gateway-instance monotonic counter used as the
	// tiebreaker when TsNs collides across records from the same instance.
	Seq uint64 `json:"seq"`

	// InstanceID is the gateway pod's stable UUID. Distinguishes records
	// from concurrent replicas with overlapping wall-clock times.
	InstanceID string `json:"instance_id"`

	// CorrelationID groups records that belong to one logical request
	// (initial + retries + tool follow-ups). Stable across the request's
	// lifetime.
	CorrelationID string `json:"correlation_id"`

	// SessionID is the resolved session/bundle id grouping every request
	// of one agent conversation, one level above CorrelationID. Empty when
	// no session header was present. Consumers bundle on the
	// (Configuration, SessionID) tuple, never the bare SessionID, since
	// client-controlled ids can collide across configurations.
	SessionID string `json:"session_id,omitempty"`

	// SessionIDSource is the header name SessionID was resolved from
	// (e.g. "X-Sluice-Session-Id", "Thread_id") — the provenance the
	// console uses to label a bundle. Empty when SessionID is empty.
	SessionIDSource string `json:"session_id_source,omitempty"`

	// Configuration is the resolved configuration name (e.g. "production").
	Configuration string `json:"configuration"`

	// APIKeyName is the human-readable name of the API key that authorised
	// the request (managed mode) or "passthrough" when in passthrough mode.
	APIKeyName string `json:"api_key_name,omitempty"`

	// Provider is the resolved (post-rule) upstream provider name.
	Provider string `json:"provider"`

	// Endpoint is the endpoint key on the provider (e.g. "chat_completions",
	// "messages"). Resolved post-rule.
	Endpoint string `json:"endpoint"`

	// Model is the resolved (post-rule) model name. May be empty for
	// endpoints that don't carry a model (e.g. /v1/models).
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

	// BodyTruncated is true when Body holds only a head prefix because the
	// captured bytes hit the per-body capture cap. This is the authoritative
	// truncation signal: consumers must not infer truncation from
	// len(Body) < BodyBytes, since the inline Body may be re-encoded
	// (compacted JSON) relative to the raw wire bytes BodyBytes counts.
	BodyTruncated bool `json:"body_truncated,omitempty"`
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

	// BodyTruncated is true when Body holds only a head prefix because the
	// captured bytes hit the per-body capture cap. This is the authoritative
	// truncation signal: consumers must not infer truncation from
	// len(Body) < BodyBytes, since the inline Body may be re-encoded
	// (compacted JSON) relative to the raw wire bytes BodyBytes counts.
	BodyTruncated bool `json:"body_truncated,omitempty"`

	// FirstByteNs is the wall-clock time the first response byte was
	// written to the client. For non-streaming responses, equals the time
	// the full body was flushed.
	FirstByteNs int64 `json:"first_byte_ns,omitempty"`

	// LastByteNs is the wall-clock time the response stream completed.
	LastByteNs int64 `json:"last_byte_ns,omitempty"`

	// StreamChunks counts the number of SSE chunks observed. Zero for
	// non-streaming responses.
	StreamChunks int `json:"stream_chunks,omitempty"`

	// Assembled is the JSON-encoded reconstruction of the non-streaming
	// response the provider would have returned, rebuilt from the streamed
	// chunks by the per-provider accumulator. Shape matches the provider's
	// non-streaming response type (OpenAI ChatCompletion, Anthropic
	// MessagesResponse, Gemini GenerateContentResponse). Empty for
	// non-streaming responses and for streams the accumulator could not
	// parse — Body still holds the raw SSE bytes in every case.
	Assembled string `json:"assembled,omitempty"`

	// AssemblyPartial is true when the accumulator hit a malformed chunk or
	// an unknown delta type mid-stream and stopped before EOF. Assembled
	// holds whatever was parseable up to that point.
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
