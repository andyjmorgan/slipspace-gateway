// Package pipeline defines the typed-message channel pipeline that carries a
// request and its response through the gateway's middleware chain.
package pipeline

import "net/http"

// Message is implemented by every concrete pipeline message type.
type Message interface {
	isMessage()
}

// ResponseInitial signals the start of an upstream response, carrying the
// status code, headers, and whether the body is streaming.
type ResponseInitial struct {
	StatusCode int

	Headers http.Header

	Streaming bool
}

func (ResponseInitial) isMessage() {}

// StreamChunk carries a single SSE event payload from an upstream streaming
// response.
type StreamChunk struct {
	Data []byte

	Event string

	ID string

	IsRaw bool
}

func (StreamChunk) isMessage() {}

// JSONResponse carries the full body of a non-streaming JSON response.
type JSONResponse struct {
	Body []byte
}

func (JSONResponse) isMessage() {}

// Error carries a terminal error encountered while processing a request.
type Error struct {
	StatusCode int

	Err error
}

func (Error) isMessage() {}

// Complete signals the end of the pipeline for a given request.
type Complete struct{}

func (Complete) isMessage() {}
