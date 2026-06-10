package admin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestSessionSpan_NullableFieldsSerializeAsNull pins the DTO's null semantics:
// the schema's nullable scalars must serialize as explicit `null` (not be
// omitted) so the renderer can distinguish "unknown" without key probing, and
// the required part envelopes must be arrays, never null.
func TestSessionSpan_NullableFieldsSerializeAsNull(t *testing.T) {
	span := SessionSpan{
		CID:         "cid-1",
		At:          time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC),
		SessionID:   "s1",
		OutputParts: []SessionSpanOutputPart{},
		InputParts:  []SessionSpanInputPart{},
	}
	b, err := json.Marshal(span)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, key := range []string{
		`"latency_ms":null`, `"ttfc_ms":null`, `"status":null`, `"model":null`,
		`"finish_reason":null`, `"parent_conversation_id":null`,
		`"input_text":null`, `"input_text_chars":null`,
		`"output_text":null`, `"output_text_chars":null`,
		`"usage":{"input":null,"output":null,"cache_read":null,"cache_creation":null,"server_tool_use":null}`,
		`"output_parts":[]`, `"input_parts":[]`,
	} {
		if !strings.Contains(s, key) {
			t.Errorf("serialized span missing %s:\n%s", key, s)
		}
	}
}

// TestSessionSpanParts_OmitInapplicableFields pins the part-envelope wire
// shape: only the fields relevant to a part's type appear, matching the DTO
// schema where chars/id/name/args are all optional per-type.
func TestSessionSpanParts_OmitInapplicableFields(t *testing.T) {
	n := 3
	out, err := json.Marshal(SessionSpanOutputPart{Type: "tool_call", ID: "t1", Name: "Bash"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"type":"tool_call","id":"t1","name":"Bash"}`; string(out) != want {
		t.Errorf("tool_call = %s, want %s", out, want)
	}
	in, err := json.Marshal(SessionSpanInputPart{Type: "text", Chars: &n, Text: "abc"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"type":"text","chars":3,"text":"abc"}`; string(in) != want {
		t.Errorf("text part = %s, want %s", in, want)
	}
}
