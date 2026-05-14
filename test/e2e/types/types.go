//go:build e2e

// Package types holds test-local decode shapes used across e2e packages.
// Kept separate from harness/ so the harness import surface stays narrow.
package types

// RequestEvent mirrors cmd/gateway.requestEvent (the JSON payload inside
// gateway.request envelopes). The real type is unexported in package main;
// rather than promote it for the test, we redeclare the shape here. Any
// new field added to the gateway side should land here too.
type RequestEvent struct {
	CorrelationID string `json:"correlation_id,omitempty"`

	Provider string `json:"provider,omitempty"`

	Endpoint string `json:"endpoint,omitempty"`

	StatusCode int `json:"status_code"`

	DurationMs int64 `json:"duration_ms"`

	Streaming bool `json:"streaming,omitempty"`

	UpstreamError string `json:"upstream_error,omitempty"`
}
