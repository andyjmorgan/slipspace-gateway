package chat

import (
	"encoding/json"

	"github.com/andyjmorgan/slipspace-gateway/models"
)

// ErrorResponse is the top-level error body returned by POST
// /v1/chat/completions on an HTTP 4xx/5xx response — shape
// {"error":{"message","type","param","code"}}. A successful call never
// carries this; it returns a ChatCompletionResponse instead. The same
// envelope also appears inline as an SSE frame (data: {"error":{...}}) when
// an error occurs mid-stream. OpenAI-compatible servers (gpt-oss, vLLM,
// LocalAI, Ollama) reproduce this exact shape. Unknown fields round-trip via
// the embedded DynamicProperties.
//
// Mirrors protocols/anthropic/messages.ErrorResponse (the sibling protocol's
// error envelope); the nested object is ErrorObject.
// Ref: https://platform.openai.com/docs/guides/error-codes
type ErrorResponse struct {
	// Error carries the machine-readable error classification and
	// human-readable message.
	Error ErrorObject `json:"error"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *ErrorResponse) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ErrorResponse) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ErrorObject is the nested error object inside an ErrorResponse (and inside
// a mid-stream SSE error frame). Param and Code carry no omitempty because
// OpenAI emits them as explicit null when unset — the marshalled body matches
// OpenAI's wire shape rather than omitting the fields (same convention as the
// gateway's cross-provider error translator). Unknown fields round-trip via
// the embedded DynamicProperties.
// Ref: https://platform.openai.com/docs/guides/error-codes
type ErrorObject struct {
	// Message is the human-readable error message.
	Message string `json:"message"`

	// Type is OpenAI's machine-readable error type (e.g.,
	// "invalid_request_error", "rate_limit_error", "api_error").
	Type string `json:"type"`

	// Param names the offending request parameter; null when the error is
	// not attributable to one parameter.
	Param *string `json:"param"`

	// Code is the machine-readable error code. Kept raw because OpenAI ships
	// a string or null, while some OpenAI-compatible servers (e.g. proxied
	// upstreams echoing HTTP statuses) ship a JSON number — the exact wire
	// shape is preserved either way.
	Code json.RawMessage `json:"code"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into o, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (o *ErrorObject) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, o) }

// MarshalJSON encodes o and merges DynamicProperties.Extra back into the
// resulting object.
func (o ErrorObject) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(o) }
