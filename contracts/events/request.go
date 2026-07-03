package events

import "time"

// Request is the payload carried inside a `gateway.request` envelope —
// one event per completed request, emitted by the per-request reporter
// observer at OnComplete. The dashboards and the v1.1 subscriber both
// decode against this shape.
type Request struct {
	// CorrelationID is the gateway-assigned request UUID, also surfaced
	// on the response as X-Slipspace-Correlation-Id.
	CorrelationID string `json:"correlation_id,omitempty"`

	// Method is the HTTP verb of the inbound client request (GET, POST,
	// DELETE, …). Captured pre-rule from the wire — rules never rewrite
	// the method — so a model-list (GET /models) is distinguishable from
	// a completion (POST /chat/completions) in audit feeds and the live
	// console.
	Method string `json:"method,omitempty"`

	// Provider is the routed upstream provider name (e.g. "openai") —
	// after rule mutation in v1.0.1+.
	Provider string `json:"provider,omitempty"`

	// Protocol is the resolved protocol under that provider (e.g. "chat",
	// "messages"), or the passthrough family name — after rule mutation
	// in v1.0.1+.
	Protocol string `json:"protocol,omitempty"`

	// Model is the outbound model identifier the gateway forwarded to
	// upstream — read at destination-finalisation time so it reflects
	// any rule mutation. Empty for protocols that carry no model.
	Model string `json:"model,omitempty"`

	// StatusCode is the HTTP status the client received. Synthetic
	// responses produced by terminating rule actions carry the rule-
	// derived status here.
	StatusCode int `json:"status_code"`

	// DurationMs is the end-to-end request duration in milliseconds.
	DurationMs int64 `json:"duration_ms"`

	// Streaming is true iff the upstream Content-Type was
	// text/event-stream (SSE).
	Streaming bool `json:"streaming,omitempty"`

	// UpstreamError carries the transport-level error string when the
	// upstream could not be reached or tore the connection before
	// headers arrived. Empty on the success path.
	UpstreamError string `json:"upstream_error,omitempty"`

	// TokensIn is the gross prompt-token total billed for the request,
	// extracted from the upstream `usage` block. Includes any tokens
	// served from a prompt cache (those are also reported separately
	// in TokensCached). Zero when the upstream did not return usage
	// data — e.g. a streaming request that omitted include_usage, a
	// stream the client cancelled before the terminal chunk arrived,
	// or a provider response that omitted usage (some Gemini preview
	// models).
	TokensIn int `json:"tokens_in,omitempty"`

	// TokensOut is the count of tokens the model generated. For
	// providers that bill reasoning tokens separately (OpenAI
	// reasoning models, Gemini thoughts), they are included here —
	// the field reflects what the customer pays for, not just
	// user-visible output.
	TokensOut int `json:"tokens_out,omitempty"`

	// TokensCached is the share of TokensIn that the provider served
	// from its prompt cache and billed at the cached-read price.
	// Informational — already counted in TokensIn, not a separate
	// deduction.
	TokensCached int `json:"tokens_cached,omitempty"`

	// TokensCacheCreation is the share of TokensIn billed at the
	// cache-write premium. Anthropic-only today (their cache-write
	// path costs more than uncached tokens); OpenAI and Gemini cache
	// writes are implicit and not separately billed, so this field
	// stays zero for them.
	TokensCacheCreation int `json:"tokens_cache_creation,omitempty"`

	// TokensCacheCreation5m / TokensCacheCreation1h split
	// TokensCacheCreation by cache TTL (Anthropic's nested
	// cache_creation breakdown). The tiers carry different write
	// premiums (1.25× vs 2× input), so per-request costing needs them.
	// Zero when the provider reported only the flat total — the flat
	// TokensCacheCreation is then authoritative. When present they sum
	// to TokensCacheCreation.
	TokensCacheCreation5m int `json:"tokens_cache_creation_5m,omitempty"`
	TokensCacheCreation1h int `json:"tokens_cache_creation_1h,omitempty"`

	// TokensInputAudio / TokensOutputAudio are the audio-modality
	// shares of TokensIn / TokensOut, billed at audio rates where the
	// provider prices audio separately (OpenAI audio_tokens details,
	// Gemini modality breakdowns). Sub-buckets — already counted in
	// the gross totals.
	TokensInputAudio  int `json:"tokens_input_audio,omitempty"`
	TokensOutputAudio int `json:"tokens_output_audio,omitempty"`

	// TokensReasoning is the reasoning/thinking share of TokensOut
	// (OpenAI reasoning_tokens, Anthropic thinking_tokens, Gemini
	// thoughtsTokenCount). Informational for attribution — billed
	// inside TokensOut at the output rate on every current provider.
	TokensReasoning int `json:"tokens_reasoning,omitempty"`

	// ServerToolUse counts server-executed tool invocations, keyed by
	// the provider's own wire vocabulary (Anthropic
	// web_search_requests, OpenAI web_search_call, Gemini
	// web_search_queries, ...). These bill per call/query outside the
	// token buckets. Nil when the request invoked no server tools.
	ServerToolUse map[string]int `json:"server_tool_use,omitempty"`

	// ServiceTier is the provider-reported processing tier the request
	// was billed under (OpenAI/Anthropic service_tier) — a
	// whole-request pricing multiplier (batch/flex discount, priority
	// premium). Empty when the provider reported none.
	ServiceTier string `json:"service_tier,omitempty"`

	// InferenceGeo is Anthropic's usage.inference_geo — the inference
	// region, which carries its own pricing multiplier. Empty for
	// other providers.
	InferenceGeo string `json:"inference_geo,omitempty"`

	// Tags is the set of tags rules attached to the request via
	// AddTagAction. Order reflects first-attach order. Empty when no
	// addTag rule fired. Set semantics — the rules engine
	// deduplicates, so a tag appears at most once.
	Tags []string `json:"tags,omitempty"`

	// PolicyRef is the name of the resilience policy this request
	// was orchestrated against, when the rules engine bound one via
	// the useResiliencePolicy action. Empty for single-shot requests
	// — i.e. requests where no resilience policy was selected, which
	// is the path most v1.1 traffic still takes.
	PolicyRef string `json:"policy_ref,omitempty"`

	// Attempts is the orchestrator's per-attempt outcome record,
	// populated when PolicyRef is set and the request ran through
	// runFailover / runLoadBalance. Order is attempt order; the
	// final entry is the attempt that committed (success path) or
	// the last attempt before exhaustion (all-failed path).
	// cb_blocked entries appear inline for targets the breaker
	// filtered before the attempt ran.
	//
	// Single-shot requests omit Attempts so the existing v1.1
	// event shape is preserved byte-equivalent.
	Attempts []AttemptRecord `json:"attempts,omitempty"`
}

// AttemptRecord captures one resilience-orchestrator attempt outcome.
// The terminal gateway.request event aggregates these into Attempts[]
// so operators can see the per-attempt journey of a multi-target
// policy (failover walk-down, load_balance pool shrink, cb_blocked
// skips).
type AttemptRecord struct {
	// Target is the resilience target name attempted, drawn from
	// the target's Name field in the policy YAML.
	Target string `json:"target"`

	// StartedAt is the wall-clock UTC time the orchestrator began
	// this attempt. For cb_blocked entries this is the time the
	// orchestrator decided to skip the target — useful as a coarse
	// time anchor when the attempt itself never ran.
	StartedAt time.Time `json:"started_at"`

	// DurationMs is the orchestrator-measured wall-clock duration
	// of the attempt in milliseconds. Zero for cb_blocked entries
	// (no attempt was made).
	DurationMs int64 `json:"duration_ms,omitempty"`

	// StatusCode is the upstream-reported HTTP status. Zero when
	// the attempt produced a transport error (no headers received)
	// or when the breaker blocked the attempt.
	StatusCode int `json:"status_code,omitempty"`

	// Error is the transport-level error string when the attempt
	// failed before headers. Empty on success and cb_blocked.
	Error string `json:"error,omitempty"`

	// Outcome categorises the attempt: "success", "failure_status",
	// "transport_error", or "cb_blocked". The same string the
	// PR-10b gateway.resilience.attempts.total counter will emit
	// as its outcome label.
	Outcome string `json:"outcome"`
}
