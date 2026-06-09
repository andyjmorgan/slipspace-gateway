package messages

import (
	"encoding/json"
	"testing"
)

func TestContextManagementConfig_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{
			name: "clear_tool_uses full",
			in: `{"edits":[{"clear_at_least":{"type":"input_tokens","value":100},` +
				`"clear_tool_inputs":true,"exclude_tools":["web_search"],` +
				`"keep":{"type":"tool_uses","value":3},` +
				`"trigger":{"type":"input_tokens","value":30000},` +
				`"type":"clear_tool_uses_20250919"}]}`,
		},
		{
			name: "clear_thinking keep string",
			in:   `{"edits":[{"keep":"all","type":"clear_thinking_20251015"}]}`,
		},
		{
			name: "clear_thinking keep object",
			in:   `{"edits":[{"keep":{"type":"thinking_turns","value":2},"type":"clear_thinking_20251015"}]}`,
		},
		{
			name: "compact full",
			in: `{"edits":[{"instructions":"summarize tersely","pause_after_compaction":true,` +
				`"trigger":{"type":"input_tokens","value":150000},"type":"compact_20260112"}]}`,
		},
		{
			name: "multiple edits",
			in: `{"edits":[{"type":"clear_tool_uses_20250919"},` +
				`{"type":"clear_thinking_20251015"},{"type":"compact_20260112"}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg ContextManagementConfig
			if err := json.Unmarshal([]byte(tc.in), &cfg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(cfg.Extra) != 0 {
				t.Fatalf("unmapped top-level fields leaked: %v", cfg.Extra)
			}
			out, err := json.Marshal(cfg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !jsonValueEqual(t, []byte(tc.in), out) {
				t.Fatalf("round-trip drift\n in: %s\nout: %s", tc.in, out)
			}
		})
	}
}

func TestContextManagementConfig_TypedEditAccess(t *testing.T) {
	in := `{"edits":[{"exclude_tools":["a"],"type":"clear_tool_uses_20250919"},` +
		`{"keep":"all","type":"clear_thinking_20251015"},` +
		`{"instructions":"go","pause_after_compaction":false,"type":"compact_20260112"}]}`
	var cfg ContextManagementConfig
	if err := json.Unmarshal([]byte(in), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Edits) != 3 {
		t.Fatalf("edits len = %d", len(cfg.Edits))
	}

	clear, ok := cfg.Edits[0].(*ClearToolUsesEdit)
	if !ok {
		t.Fatalf("edits[0] = %T", cfg.Edits[0])
	}
	if clear.EditType() != "clear_tool_uses_20250919" || len(clear.ExcludeTools) != 1 {
		t.Fatalf("clear = %+v", clear)
	}

	think, ok := cfg.Edits[1].(*ClearThinkingEdit)
	if !ok {
		t.Fatalf("edits[1] = %T", cfg.Edits[1])
	}
	if think.EditType() != "clear_thinking_20251015" || string(think.Keep) != `"all"` {
		t.Fatalf("think = %+v", think)
	}

	compact, ok := cfg.Edits[2].(*CompactEdit)
	if !ok {
		t.Fatalf("edits[2] = %T", cfg.Edits[2])
	}
	if compact.EditType() != "compact_20260112" || compact.Instructions != "go" {
		t.Fatalf("compact = %+v", compact)
	}
	if compact.PauseAfterCompaction == nil || *compact.PauseAfterCompaction {
		t.Fatalf("pause_after_compaction = %v", compact.PauseAfterCompaction)
	}
}

func TestContextManagementConfig_UnknownDiscriminatorRoundTrips(t *testing.T) {
	in := `{"edits":[{"future_field":99,"type":"summarize_20990101"}]}`
	var cfg ContextManagementConfig
	if err := json.Unmarshal([]byte(in), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Edits) != 1 {
		t.Fatalf("edits len = %d", len(cfg.Edits))
	}
	u, ok := cfg.Edits[0].(*UnknownContextEdit)
	if !ok {
		t.Fatalf("edits[0] = %T", cfg.Edits[0])
	}
	if u.Type != "summarize_20990101" || u.EditType() != "summarize_20990101" {
		t.Fatalf("unknown edit = %+v", u)
	}
	if string(u.Extra["future_field"]) != "99" {
		t.Fatalf("extra not preserved: %v", u.Extra)
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, []byte(in), out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func TestContextManagementConfig_UnknownTopLevelFieldRoundTrips(t *testing.T) {
	in := `{"edits":[{"type":"compact_20260112"}],"future_knob":{"x":1}}`
	var cfg ContextManagementConfig
	if err := json.Unmarshal([]byte(in), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(cfg.Extra["future_knob"]) != `{"x":1}` {
		t.Fatalf("extra not preserved: %v", cfg.Extra)
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, []byte(in), out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func TestContextManagementConfig_EmptyAndNullEdits(t *testing.T) {
	for _, in := range []string{`{}`, `{"edits":null}`} {
		var cfg ContextManagementConfig
		if err := json.Unmarshal([]byte(in), &cfg); err != nil {
			t.Fatalf("unmarshal %q: %v", in, err)
		}
		if cfg.Edits != nil {
			t.Fatalf("edits = %v (want nil) for %q", cfg.Edits, in)
		}
	}
}

// TestContextManagementConfig_EmptyEditsCollapsesToAbsent documents the
// intentional omitempty collapse: an explicit empty "edits":[] decodes to a
// non-nil empty slice and re-marshals to an omitted key. This is lossless
// because the SDK field is total=False (empty list ≡ absent ≡ "no edits"); the
// asymmetry vs the response-side applied_edits (which keeps []) is by design.
// If the Edits omitempty is ever removed, this test fails as a signal.
func TestContextManagementConfig_EmptyEditsCollapsesToAbsent(t *testing.T) {
	var cfg ContextManagementConfig
	if err := json.Unmarshal([]byte(`{"edits":[]}`), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != `{}` {
		t.Fatalf("empty edits did not collapse to {}: %s", out)
	}
}

func TestContextManagementConfig_InvalidEdits(t *testing.T) {
	// An edits array element missing the "type" discriminator is rejected by
	// the registry rather than silently dropped.
	in := `{"edits":[{"keep":"all"}]}`
	var cfg ContextManagementConfig
	if err := json.Unmarshal([]byte(in), &cfg); err == nil {
		t.Fatalf("expected error for edit missing discriminator")
	}
}

func TestContextManagementConfig_NonObject(t *testing.T) {
	var cfg ContextManagementConfig
	if err := json.Unmarshal([]byte(`["not an object"]`), &cfg); err == nil {
		t.Fatalf("expected error decoding non-object into ContextManagementConfig")
	}
}

func TestUnmarshalContextEdit_Single(t *testing.T) {
	v, err := UnmarshalContextEdit([]byte(`{"type":"compact_20260112"}`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := v.(*CompactEdit); !ok {
		t.Fatalf("got %T", v)
	}
}

func TestContextEditRegistry_CoversAllConcreteTypes(t *testing.T) {
	want := map[string]bool{
		"clear_tool_uses_20250919": false,
		"clear_thinking_20251015":  false,
		"compact_20260112":         false,
	}
	for k := range contextEditRegistry.Factories {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected registry key %q", k)
		}
		want[k] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("registry missing factory for %q", k)
		}
	}
	if contextEditRegistry.Fallback == nil {
		t.Fatalf("registry must have a fallback so unknown discriminators round-trip")
	}
}

func TestMessagesRequest_ContextManagementRoundTrip(t *testing.T) {
	in := []byte(`{"context_management":{"edits":[{"keep":{"type":"tool_uses","value":3},` +
		`"type":"clear_tool_uses_20250919"}]},` +
		`"max_tokens":1024,"messages":[{"content":"hi","role":"user"}],"model":"claude-sonnet-4-6"}`)
	var req MessagesRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.ContextManagement == nil || len(req.ContextManagement.Edits) != 1 {
		t.Fatalf("context_management = %+v", req.ContextManagement)
	}
	if _, ok := req.ContextManagement.Edits[0].(*ClearToolUsesEdit); !ok {
		t.Fatalf("edit = %T", req.ContextManagement.Edits[0])
	}
	if len(req.Extra) != 0 {
		t.Fatalf("unmapped fields leaked: %v", req.Extra)
	}
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func TestErrorResponse_RoundTrip(t *testing.T) {
	in := []byte(`{"error":{"message":"Your credit balance is too low","type":"invalid_request_error"},` +
		`"request_id":"req_011CSHoEHfqM","type":"error"}`)
	var er ErrorResponse
	if err := json.Unmarshal(in, &er); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if er.Type != "error" {
		t.Fatalf("type = %q", er.Type)
	}
	if er.Error.Type != "invalid_request_error" || er.Error.Message != "Your credit balance is too low" {
		t.Fatalf("error = %+v", er.Error)
	}
	if er.RequestID != "req_011CSHoEHfqM" {
		t.Fatalf("request_id = %q", er.RequestID)
	}
	if len(er.Extra) != 0 || len(er.Error.Extra) != 0 {
		t.Fatalf("unmapped fields leaked: %v %v", er.Extra, er.Error.Extra)
	}
	out, err := json.Marshal(er)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func TestErrorResponse_UnknownFieldsRoundTrip(t *testing.T) {
	in := []byte(`{"error":{"detail":"x","message":"boom","type":"overloaded_error"},` +
		`"future_top":1,"type":"error"}`)
	var er ErrorResponse
	if err := json.Unmarshal(in, &er); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(er.Extra["future_top"]) != "1" {
		t.Fatalf("top extra not preserved: %v", er.Extra)
	}
	if string(er.Error.Extra["detail"]) != `"x"` {
		t.Fatalf("nested extra not preserved: %v", er.Error.Extra)
	}
	out, err := json.Marshal(er)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}
