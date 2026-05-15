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
}
