package chat

import (
	"encoding/json"
	"testing"
)

// TestChat_ErrorResponseRoundTrips pins the OpenAI error envelope
// {"error":{"message","type","param","code"}} — the shape OpenAI and its
// compat servers (gpt-oss, vLLM, LocalAI, Ollama) return on 4xx/5xx and as
// mid-stream SSE error frames. Each case must round-trip byte-equivalent
// modulo key order, including a numeric code (compat servers) and unknown
// fields at both envelope and error-object level.
func TestChat_ErrorResponseRoundTrips(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{
			name: "openai canonical nulls",
			json: `{"error":{"code":null,"message":"model not found","param":null,"type":"invalid_request_error"}}`,
		},
		{
			name: "string code with param",
			json: `{"error":{"code":"model_not_found","message":"The model does not exist","param":"model","type":"invalid_request_error"}}`,
		},
		{
			name: "numeric code from compat server",
			json: `{"error":{"code":404,"message":"model not loaded","param":null,"type":"api_error"}}`,
		},
		{
			name: "unknown fields both levels",
			json: `{"error":{"code":null,"detail":"extra","message":"boom","param":null,"type":"api_error"},"request_id":"req_1"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var er ErrorResponse
			if err := json.Unmarshal([]byte(tc.json), &er); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			out, err := json.Marshal(er)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !jsonValueEqual(t, []byte(tc.json), out) {
				t.Fatalf("drift\n in: %s\nout: %s", tc.json, out)
			}
		})
	}
}

func TestChat_ErrorResponse_TypedFields(t *testing.T) {
	in := []byte(`{"error":{"code":"rate_limited","message":"slow down","param":"messages","type":"rate_limit_error"}}`)
	var er ErrorResponse
	if err := json.Unmarshal(in, &er); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if er.Error.Message != "slow down" || er.Error.Type != "rate_limit_error" {
		t.Fatalf("message/type: %+v", er.Error)
	}
	if er.Error.Param == nil || *er.Error.Param != "messages" {
		t.Fatalf("param: %v", er.Error.Param)
	}
	if string(er.Error.Code) != `"rate_limited"` {
		t.Fatalf("code raw: %s", er.Error.Code)
	}
}

// TestChat_ErrorResponse_UnknownFields verifies unknown keys land in
// DynamicProperties.Extra on both the envelope and the nested error object.
func TestChat_ErrorResponse_UnknownFields(t *testing.T) {
	in := []byte(`{"error":{"code":null,"message":"m","param":null,"provider_hint":"gpu","type":"api_error"},"trace_id":"t_1"}`)
	var er ErrorResponse
	if err := json.Unmarshal(in, &er); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(er.Extra["trace_id"]) != `"t_1"` {
		t.Fatalf("envelope extra: %v", er.Extra)
	}
	if string(er.Error.Extra["provider_hint"]) != `"gpu"` {
		t.Fatalf("error object extra: %v", er.Error.Extra)
	}
	out, err := json.Marshal(er)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}
}
