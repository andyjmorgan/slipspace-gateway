package translate

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// openAIErrorEnvelope is the OpenAI error body shape:
// {"error":{"message":...,"type":...,"code":null,"param":null}}. Code and Param
// are emitted as explicit null so the body matches OpenAI's wire shape rather
// than omitting the fields.
type openAIErrorEnvelope struct {
	Error openAIErrorBody `json:"error"`
}

// openAIErrorBody is the inner OpenAI error object.
type openAIErrorBody struct {
	// Message is the human-readable error message.
	Message string `json:"message"`

	// Type is OpenAI's machine-readable error type, derived from the HTTP
	// status (the reliable cross-provider signal).
	Type string `json:"type"`

	// Code is the OpenAI error code; always null here (Anthropic carries no
	// equivalent), emitted explicitly to match OpenAI's body shape.
	Code *string `json:"code"`

	// Param is the offending request parameter; always null here.
	Param *string `json:"param"`
}

// translateMessagesErrorToChat maps an upstream Anthropic error response (HTTP
// status >= 400) into the OpenAI error envelope. The HTTP status code is
// preserved by the forwarder; only the body shape changes, so an OpenAI client
// (and SDK) sees a well-formed OpenAI error — the SDK selects its exception
// class from the status, so the body need only parse.
func translateMessagesErrorToChat(status int, body []byte) ([]byte, error) {
	out := openAIErrorEnvelope{
		Error: openAIErrorBody{
			Message: anthropicErrorMessage(body, status),
			Type:    openAIErrorType(status),
		},
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("translate messages->chat: encode error: %w", err)
	}
	return b, nil
}

// anthropicErrorMessage pulls the human-readable message out of an Anthropic
// error body ({"type":"error","error":{"message":...}}), falling back to the
// HTTP status text when absent so the client always gets something actionable
// (and we never echo an unparsed upstream blob).
func anthropicErrorMessage(body []byte, status int) string {
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

// openAIErrorType maps an HTTP status to an OpenAI-style error type. Status is
// the reliable cross-provider signal; Anthropic's error.type vocabulary differs,
// so we derive from the code OpenAI clients (and the SDK) key on.
func openAIErrorType(status int) string {
	switch {
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return "authentication_error"
	case status >= 400 && status < 500:
		return "invalid_request_error"
	default:
		return "api_error"
	}
}
