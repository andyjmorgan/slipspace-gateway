package translate

import (
	"encoding/json"
	"testing"
)

func decodeErrEnvelope(t *testing.T, status int, body string) anthropicErrorEnvelope {
	t.Helper()
	out, err := translateChatErrorToMessages(status, []byte(body))
	if err != nil {
		t.Fatalf("translateChatErrorToMessages: %v", err)
	}
	var env anthropicErrorEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode error envelope: %v\nout: %s", err, out)
	}
	if env.Type != "error" {
		t.Errorf("envelope type = %q, want error", env.Type)
	}
	return env
}

func TestTranslateError_MessageAndType(t *testing.T) {
	env := decodeErrEnvelope(t, 429, `{"error":{"message":"slow down","type":"rate_limit_exceeded","code":null}}`)
	if env.Error.Message != "slow down" {
		t.Errorf("message = %q, want 'slow down'", env.Error.Message)
	}
	if env.Error.Type != "rate_limit_error" {
		t.Errorf("type = %q, want rate_limit_error (status 429)", env.Error.Type)
	}
}

func TestTranslateError_StatusTypeMapping(t *testing.T) {
	tests := map[int]string{
		400: "invalid_request_error",
		401: "authentication_error",
		403: "permission_error",
		404: "not_found_error",
		413: "request_too_large",
		429: "rate_limit_error",
		500: "api_error",
		529: "overloaded_error",
		418: "api_error", // unmapped -> api_error
	}
	for status, want := range tests {
		if got := anthropicErrorType(status); got != want {
			t.Errorf("anthropicErrorType(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestTranslateError_FallbackMessage(t *testing.T) {
	// No OpenAI error.message -> fall back to HTTP status text, never echo the
	// raw upstream blob.
	env := decodeErrEnvelope(t, 500, `{"something":"else"}`)
	if env.Error.Message != "Internal Server Error" {
		t.Errorf("fallback message = %q, want HTTP status text", env.Error.Message)
	}
	env2 := decodeErrEnvelope(t, 503, `not even json`)
	if env2.Error.Message == "" || env2.Error.Message == "not even json" {
		t.Errorf("message = %q, want status-text fallback, not the raw blob", env2.Error.Message)
	}
}

func TestTranslateError_ViaInterface(t *testing.T) {
	tr, ok := Lookup("messages", "chat")
	if !ok {
		t.Fatal("translator not registered")
	}
	et, ok := tr.(ErrorTranslator)
	if !ok {
		t.Fatal("translator does not implement ErrorTranslator")
	}
	out, err := et.TranslateError(401, []byte(`{"error":{"message":"bad key"}}`))
	if err != nil {
		t.Fatalf("TranslateError: %v", err)
	}
	var env anthropicErrorEnvelope
	if err := json.Unmarshal(out, &env); err != nil || env.Error.Type != "authentication_error" {
		t.Errorf("envelope = %s (err %v), want authentication_error", out, err)
	}
}

func TestErrorChunk_Detection(t *testing.T) {
	if msg, ok := errorChunk([]byte(`{"error":{"message":"boom"}}`)); !ok || msg != "boom" {
		t.Errorf("errorChunk = %q,%v; want boom,true", msg, ok)
	}
	if _, ok := errorChunk([]byte(`{"choices":[]}`)); ok {
		t.Error("errorChunk matched a non-error chunk")
	}
	if _, ok := errorChunk([]byte(`not json`)); ok {
		t.Error("errorChunk matched invalid json")
	}
}

func TestStream_MidStreamErrorBecomesErrorEvent(t *testing.T) {
	in := `data: {"id":"x","model":"m","choices":[{"index":0,"delta":{"content":"partial"}}]}

data: {"error":{"message":"upstream blew up","type":"server_error"}}

`
	ev := runStream(t, in)
	var sawError bool
	var errMsg string
	for _, e := range ev {
		if e.typ == "error" {
			sawError = true
			var d struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(e.data, &d)
			errMsg = d.Error.Message
		}
	}
	if !sawError {
		t.Fatalf("no anthropic error event emitted: %v", types(ev))
	}
	if errMsg != "upstream blew up" {
		t.Errorf("error message = %q, want 'upstream blew up'", errMsg)
	}
}
