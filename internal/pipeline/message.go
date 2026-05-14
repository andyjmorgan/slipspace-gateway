// Package pipeline defines the typed-message channel pipeline that carries a
// request and its response through the gateway's middleware chain.
package pipeline

import "net/http"

// Message is the sum type carried by the pipeline's channel-based
// middleware. Implementations are sealed to this package via the unexported
// isMessage method, so the set of concrete types is closed: type-switching on
// Message in a middleware is exhaustive by construction.
type Message interface {
	isMessage()
}

// ResponseInitial is the first message emitted by the forwarder once the
// upstream provider returns response headers. Downstream middleware uses it
// to decide whether to expect a single JSONResponse or a stream of
// StreamChunk values.
type ResponseInitial struct {
	// StatusCode is the upstream HTTP status code, propagated verbatim to
	// the client.
	StatusCode int

	// Headers is the upstream response header set. The forwarder will
	// have already applied the configured header-pass policy before
	// emitting; downstream middleware mutates at its own risk.
	Headers http.Header

	// Streaming is true when the response body is an SSE / chunked stream.
	// Subsequent messages will be StreamChunk; when false, exactly one
	// JSONResponse follows.
	Streaming bool
}

func (ResponseInitial) isMessage() {}

// StreamChunk is emitted by the forwarder for each SSE event read off a
// streaming upstream response. It always follows a ResponseInitial whose
// Streaming flag is true, and the sequence terminates with Complete.
type StreamChunk struct {
	// Data is the SSE event payload (everything after `data:`). For
	// provider-native SSE the value is the JSON object as bytes.
	Data []byte

	// Event is the SSE event-type label (after `event:`) or empty when
	// the upstream omits it. Anthropic / OpenAI use this; Gemini does
	// not.
	Event string

	// ID is the SSE event id (after `id:`) when supplied by the upstream.
	// Empty when omitted.
	ID string

	// IsRaw signals that Data carries an opaque non-JSON payload (e.g.
	// `[DONE]` sentinels from OpenAI). Downstream middleware skips typed
	// decoding when this is true.
	IsRaw bool
}

func (StreamChunk) isMessage() {}

// JSONResponse is emitted by the forwarder for a non-streaming response,
// following a ResponseInitial whose Streaming flag is false. Exactly one
// JSONResponse appears per request, then Complete.
type JSONResponse struct {
	// Body is the verbatim response body bytes.
	Body []byte
}

func (JSONResponse) isMessage() {}

// Error is emitted by any middleware that cannot recover from a failure.
// It terminates the pipeline — middlewares that see it should forward it
// downstream unchanged so the writer can translate it to an HTTP error.
type Error struct {
	// StatusCode is the HTTP status the writer should send. Middlewares
	// pick this based on the failure class (400 for client input, 502 for
	// upstream, 500 for internal).
	StatusCode int

	// Err is the underlying error. Logged with full context; only its
	// status code reaches the client unless the writer is configured to
	// expose the message.
	Err error
}

func (Error) isMessage() {}

// Complete is emitted as the final message of a successful response and
// signals every middleware to release per-request resources. It carries no
// payload — its presence is the signal.
type Complete struct{}

func (Complete) isMessage() {}
