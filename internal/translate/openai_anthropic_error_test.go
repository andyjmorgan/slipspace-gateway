package translate

import (
	"encoding/json"
	"net/http"
	"testing"
)

// decodeOpenAIError translates an Anthropic error body and decodes the OpenAI
// error envelope for assertions.
func decodeOpenAIError(t *testing.T, status int, body string) openAIErrorEnvelope {
	t.Helper()
	out, err := translateMessagesErrorToChat(status, []byte(body))
	if err != nil {
		t.Fatalf("translateMessagesErrorToChat: %v", err)
	}
	var env openAIErrorEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode error envelope: %v\nbody: %s", err, out)
	}
	return env
}

func TestMessagesErrorToChat_TypeByStatus(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusTooManyRequests, "rate_limit_error"},
		{http.StatusUnauthorized, "authentication_error"},
		{http.StatusForbidden, "authentication_error"},
		{http.StatusBadRequest, "invalid_request_error"},
		{http.StatusNotFound, "invalid_request_error"},
		{http.StatusInternalServerError, "api_error"},
		{529, "api_error"},
	}
	for _, tc := range cases {
		env := decodeOpenAIError(t, tc.status, `{"type":"error","error":{"type":"overloaded_error","message":"boom"}}`)
		if env.Error.Type != tc.want {
			t.Errorf("status %d: type = %q, want %q", tc.status, env.Error.Type, tc.want)
		}
		if env.Error.Message != "boom" {
			t.Errorf("status %d: message = %q, want boom", tc.status, env.Error.Message)
		}
	}
}

func TestMessagesErrorToChat_NullCodeParam(t *testing.T) {
	out, err := translateMessagesErrorToChat(http.StatusTooManyRequests, []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow"}}`))
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	// code and param must serialize as explicit null to match OpenAI's shape.
	var probe map[string]json.RawMessage
	var inner map[string]json.RawMessage
	_ = json.Unmarshal(out, &probe)
	_ = json.Unmarshal(probe["error"], &inner)
	if string(inner["code"]) != "null" || string(inner["param"]) != "null" {
		t.Errorf("code/param = %s/%s, want null/null", inner["code"], inner["param"])
	}
}

func TestMessagesErrorToChat_MessageFallback(t *testing.T) {
	// Unparseable body -> HTTP status text.
	env := decodeOpenAIError(t, http.StatusBadRequest, `not json at all`)
	if env.Error.Message != http.StatusText(http.StatusBadRequest) {
		t.Errorf("message = %q, want %q", env.Error.Message, http.StatusText(http.StatusBadRequest))
	}

	// Unknown status with no body and no status text -> generic fallback.
	if got := anthropicErrorMessage([]byte(``), 799); got != "upstream error" {
		t.Errorf("fallback message = %q, want 'upstream error'", got)
	}
}

func TestMessagesErrorToChat_EmptyMessageFallsBack(t *testing.T) {
	// Well-formed envelope but empty message -> status text fallback.
	env := decodeOpenAIError(t, http.StatusForbidden, `{"type":"error","error":{"type":"permission_error","message":""}}`)
	if env.Error.Message != http.StatusText(http.StatusForbidden) {
		t.Errorf("message = %q, want %q", env.Error.Message, http.StatusText(http.StatusForbidden))
	}
}
