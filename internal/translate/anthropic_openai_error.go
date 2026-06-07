package translate

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/andyjmorgan/sluice-gateway/protocols/anthropic/messages"
)

// anthropicErrorEnvelope is the Anthropic non-streaming error body shape:
// {"type":"error","error":{"type":...,"message":...}}. Reuses StreamError for
// the inner object (same {type,message} shape Anthropic uses on both the
// error response and the SSE error event).
type anthropicErrorEnvelope struct {
	Type  string               `json:"type"`
	Error messages.StreamError `json:"error"`
}

// translateChatErrorToMessages maps an upstream OpenAI-style error response
// (HTTP status >= 400) into the Anthropic error envelope. The HTTP status code
// is preserved by the forwarder; only the body shape changes, so a Claude
// client sees a well-formed Anthropic error instead of a degenerate empty
// success.
func translateChatErrorToMessages(status int, body []byte) ([]byte, error) {
	out := anthropicErrorEnvelope{
		Type: "error",
		Error: messages.StreamError{
			Type:    anthropicErrorType(status),
			Message: openAIErrorMessage(body, status),
		},
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("translate chat->messages: encode error: %w", err)
	}
	return b, nil
}

// openAIErrorMessage pulls the human-readable message out of an OpenAI-style
// error body ({"error":{"message":...}}), falling back to the HTTP status text
// when absent so the client always gets something actionable (and we never echo
// an unparsed upstream blob).
func openAIErrorMessage(body []byte, status int) string {
	var probe struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &probe); err == nil && probe.Error.Message != "" {
		return probe.Error.Message
	}
	if txt := http.StatusText(status); txt != "" {
		return txt
	}
	return "upstream error"
}

// errorChunk reports whether an SSE data payload carries a top-level OpenAI
// "error" object, returning its message. Used by the stream translator to turn
// a mid-stream error into an Anthropic error event.
func errorChunk(frame []byte) (string, bool) {
	var probe struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(frame, &probe); err != nil || probe.Error == nil {
		return "", false
	}
	return probe.Error.Message, true
}

// anthropicErrorType maps an HTTP status to Anthropic's machine-readable error
// type. Status is the reliable cross-provider signal; OpenAI's error.type
// vocabulary differs, so we derive from the code Anthropic clients key on.
func anthropicErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case 529:
		return "overloaded_error"
	default:
		return "api_error"
	}
}
