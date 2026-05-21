package events

// Request is the payload carried inside a `gateway.request` envelope —
// one event per completed request, emitted by the per-request reporter
// observer at OnComplete. The dashboards and the v1.1 subscriber both
// decode against this shape.
type Request struct {
	// CorrelationID is the gateway-assigned request UUID, also surfaced
	// on the response as X-Sluice-Correlation-Id.
	CorrelationID string `json:"correlation_id,omitempty"`

	// Provider is the routed upstream provider name (e.g. "openai") —
	// after rule mutation in v1.0.1+.
	Provider string `json:"provider,omitempty"`

	// Endpoint is the routed endpoint name under that provider
	// (e.g. "chat_completions") — after rule mutation in v1.0.1+.
	Endpoint string `json:"endpoint,omitempty"`

	// Model is the outbound model identifier the gateway forwarded to
	// upstream — read at destination-finalisation time so it reflects
	// any rule mutation. Empty for endpoints that carry no model.
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

	// Tags is the set of tags rules attached to the request via
	// AddTagAction. Order reflects first-attach order. Empty when no
	// addTag rule fired. Set semantics — the rules engine
	// deduplicates, so a tag appears at most once.
	Tags []string `json:"tags,omitempty"`
}
