package events_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/contracts/events"
)

func TestRequest_MarshalRoundTrip(t *testing.T) {
	t.Parallel()

	in := events.Request{
		CorrelationID: "11111111-1111-1111-1111-111111111111",
		Provider:      "openai",
		Protocol:      "chat_completions",
		Model:         "gpt-4o-mini",
		StatusCode:    200,
		DurationMs:    742,
		Streaming:     true,
		UpstreamError: "",
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out events.Request
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch:\n  in  = %#v\n  out = %#v", in, out)
	}
}

func TestRequest_OmitemptyFields(t *testing.T) {
	t.Parallel()

	ev := events.Request{StatusCode: 200, DurationMs: 50}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got := string(data)
	for _, banned := range []string{"correlation_id", "provider", "protocol", "model", "streaming", "upstream_error"} {
		if strings.Contains(got, banned) {
			t.Errorf("expected %q to be omitted on zero value; got: %s", banned, got)
		}
	}

	for _, required := range []string{`"status_code":200`, `"duration_ms":50`} {
		if !strings.Contains(got, required) {
			t.Errorf("expected %s; got: %s", required, got)
		}
	}
}

func TestRequest_PopulatedFieldsSerialise(t *testing.T) {
	t.Parallel()

	ev := events.Request{
		CorrelationID: "abc",
		Provider:      "anthropic",
		Protocol:      "messages",
		Model:         "claude-haiku-4-5",
		StatusCode:    502,
		DurationMs:    1234,
		Streaming:     true,
		UpstreamError: "dial tcp: connection refused",
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)
	want := []string{
		`"correlation_id":"abc"`,
		`"provider":"anthropic"`,
		`"protocol":"messages"`,
		`"model":"claude-haiku-4-5"`,
		`"status_code":502`,
		`"duration_ms":1234`,
		`"streaming":true`,
		`"upstream_error":"dial tcp: connection refused"`,
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %s in: %s", w, got)
		}
	}
}
