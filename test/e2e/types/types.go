//go:build e2e

// Package types holds test-local decode shapes used across e2e packages.
// Kept separate from harness/ so the harness import surface stays narrow.
package types

import "time"

// RequestEvent mirrors cmd/gateway.requestEvent (the JSON payload inside
// gateway.request envelopes). The real type is unexported in package main;
// rather than promote it for the test, we redeclare the shape here. Any
// new field added to the gateway side should land here too.
type RequestEvent struct {
	CorrelationID string `json:"correlation_id,omitempty"`

	Provider string `json:"provider,omitempty"`

	Protocol string `json:"protocol,omitempty"`

	Model string `json:"model,omitempty"`

	Method string `json:"method,omitempty"`

	StatusCode int `json:"status_code"`

	DurationMs int64 `json:"duration_ms"`

	Streaming bool `json:"streaming,omitempty"`

	UpstreamError string `json:"upstream_error,omitempty"`

	TokensIn int `json:"tokens_in,omitempty"`

	TokensOut int `json:"tokens_out,omitempty"`

	TokensCached int `json:"tokens_cached,omitempty"`

	TokensCacheCreation int `json:"tokens_cache_creation,omitempty"`

	TokensCacheCreation5m int `json:"tokens_cache_creation_5m,omitempty"`

	TokensCacheCreation1h int `json:"tokens_cache_creation_1h,omitempty"`

	TokensInputAudio int `json:"tokens_input_audio,omitempty"`

	TokensOutputAudio int `json:"tokens_output_audio,omitempty"`

	TokensReasoning int `json:"tokens_reasoning,omitempty"`

	ServerToolUse map[string]int `json:"server_tool_use,omitempty"`

	ServiceTier string `json:"service_tier,omitempty"`

	InferenceGeo string `json:"inference_geo,omitempty"`

	Tags []string `json:"tags,omitempty"`

	PolicyRef string `json:"policy_ref,omitempty"`

	Attempts []AttemptRecord `json:"attempts,omitempty"`
}

// AttemptRecord mirrors contracts/events.AttemptRecord — one per-attempt
// row inside the consolidated gateway.request event.
type AttemptRecord struct {
	Target string `json:"target"`

	StartedAt time.Time `json:"started_at"`

	DurationMs int64 `json:"duration_ms,omitempty"`

	StatusCode int `json:"status_code,omitempty"`

	Error string `json:"error,omitempty"`

	Outcome string `json:"outcome"`
}
