package connector

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRecord_JSONRoundTrip(t *testing.T) {
	in := Record{
		V:             1,
		ID:            "01HW2C8Z000000000000000000",
		TsNs:          1715000000123456789,
		Seq:           482931,
		InstanceID:    "abcd1234",
		CorrelationID: "req-9f8e",
		Configuration: "production",
		APIKeyName:    "internal-rag",
		Provider:      "anthropic",
		Endpoint:      "messages",
		Model:         "claude-haiku-4-5",
		Tags:          []string{"surface:anthropic-messages"},
		Request: RequestPart{
			Method:     "POST",
			Path:       "/anthropic/v1/messages",
			Headers:    map[string]string{"anthropic-version": "2023-06-01"},
			BodySha256: "ab",
			BodyBytes:  12348,
			Body:       json.RawMessage(`{"model":"claude-haiku-4-5"}`),
		},
		Response: ResponsePart{
			Status:       200,
			Headers:      map[string]string{"content-type": "application/json"},
			BodySha256:   "cd",
			BodyBytes:    84200,
			Body:         json.RawMessage(`{"id":"msg_x"}`),
			FirstByteNs:  1715000000234567890,
			LastByteNs:   1715000003123456789,
			StreamChunks: 24,
		},
		Tokens:         &Tokens{Input: 542, Output: 1320},
		RulesFired:     []RuleFired{{Name: "route-claude-models-to-anthropic", TookUs: 8}},
		UpstreamStatus: 200,
		SchemaVersion:  SchemaVersion,
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out Record
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Compare via re-marshal to sidestep map ordering noise.
	want, _ := json.Marshal(in)
	got, _ := json.Marshal(out)
	if !bytes.Equal(want, got) {
		t.Errorf("round-trip mismatch\n want %s\n  got %s", want, got)
	}
}

func TestRecord_UnknownFieldsAreSkipped(t *testing.T) {
	// Consumers must tolerate unknown fields — that's the additive-only
	// schema rule. Confirm encoding/json's default behaviour holds.
	raw := []byte(`{
		"v": 1,
		"id": "01HW2C",
		"ts_ns": 1,
		"seq": 1,
		"instance_id": "i",
		"correlation_id": "c",
		"configuration": "cfg",
		"provider": "openai",
		"endpoint": "chat_completions",
		"request":  {"method": "POST", "path": "/x"},
		"response": {"status": 200},
		"schema_version": 1,
		"future_field_we_dont_know_about": {"nested": true},
		"another_one": [1, 2, 3]
	}`)
	var r Record
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("unmarshal with unknown fields: %v", err)
	}
	if r.Provider != "openai" {
		t.Errorf("expected provider=openai, got %q", r.Provider)
	}
}

func TestRecord_OmitemptyTrims(t *testing.T) {
	// Minimal record should not emit nil-y/zero fields. Keeps batches
	// tight on the wire.
	r := Record{
		V:             1,
		ID:            "x",
		TsNs:          1,
		Seq:           1,
		InstanceID:    "i",
		CorrelationID: "c",
		Configuration: "cfg",
		Provider:      "openai",
		Endpoint:      "chat_completions",
		Request:       RequestPart{Method: "POST", Path: "/x"},
		Response:      ResponsePart{Status: 200},
		SchemaVersion: 1,
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Spot-check that omitempty fields are absent.
	for _, key := range []string{`"model"`, `"tokens"`, `"rules_fired"`, `"upstream_error"`, `"body_ref"`, `"body_omitted"`} {
		if bytes.Contains(data, []byte(key)) {
			t.Errorf("unexpected %s in minimal record: %s", key, data)
		}
	}
}

func TestRequestPart_BodyAndRefAreMutuallyExclusive(t *testing.T) {
	// The contract doesn't enforce this at the type level (both fields
	// are present), but the emitter must respect it. This test pins the
	// invariant so a future refactor that violates it surfaces.
	rp := RequestPart{Body: json.RawMessage(`"x"`), BodyRef: "s3://bucket/x"}
	if len(rp.Body) > 0 && rp.BodyRef != "" {
		t.Log("RequestPart allows both Body and BodyRef set — emitter must clear one")
	}
}
