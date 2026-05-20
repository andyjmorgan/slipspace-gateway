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

	// Provider, Endpoint, Model carry the routed labels (post rule
	// mutation).
	Provider string `json:"provider,omitempty"`

	Endpoint string `json:"endpoint,omitempty"`

	Model string `json:"model,omitempty"`

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

	// RulesMatched lists every rule that fired, in evaluation order.
	RulesMatched []RuleHit `json:"rules_matched,omitempty"`
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
