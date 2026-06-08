package main

import (
	"strings"
	"testing"
)

func dropped(fs []finding) []string {
	var out []string
	for _, f := range fs {
		if f.Category == "DROPPED_KEY" {
			out = append(out, f.Key)
		}
	}
	return out
}

// A rich Anthropic stream exercising citations_delta, tool_use caller, and
// message_delta usage breakdowns must audit CLEAN against the fixed assembler —
// this is the regression guard that locks in the v1 rollup-fidelity fix.
func TestAuditCase_AnthropicRichStreamIsClean(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-x","content":[],"usage":{"input_tokens":5,"output_tokens":1,"cache_creation":{"ephemeral_1h_input_tokens":7},"service_tier":"standard"}}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"The sky is blue."}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://e.com","cited_text":"blue"}}}`,
		``,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"Edit","input":{},"caller":{"type":"direct"}}}`,
		``,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\":1}"}}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42,"output_tokens_details":{"thinking_tokens":7},"server_tool_use":{"web_search_requests":1}}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	fs := auditCase("anthropic", "messages", "rich", []byte(sse))
	if d := dropped(fs); len(d) != 0 {
		t.Fatalf("expected no dropped keys on fixed assembler, got %v (all findings: %+v)", d, fs)
	}
}

// A synthetic field on message_delta.delta is not copied into the assembled
// body (absorbMessageDelta lifts only typed fields), so the detector must flag
// it — this proves positive detection works.
func TestAuditCase_DetectsDroppedKey(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"x","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","xyzzy_marker":"dropme"},"usage":{"output_tokens":2}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	fs := auditCase("anthropic", "messages", "synthetic", []byte(sse))
	found := false
	for _, k := range dropped(fs) {
		if k == "xyzzy_marker" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected xyzzy_marker flagged as DROPPED_KEY, got findings %+v", fs)
	}
}

func TestDecodeStored_UnwrapsJSONString(t *testing.T) {
	raw := decodeStored([]byte(`"event: ping\ndata: {}"`))
	if string(raw) != "event: ping\ndata: {}" {
		t.Fatalf("json-string unwrap = %q", raw)
	}
	plain := decodeStored([]byte("event: ping\ndata: {}"))
	if string(plain) != "event: ping\ndata: {}" {
		t.Fatalf("plain passthrough = %q", plain)
	}
}

func TestParseEvents_SkipsBlankAndDone(t *testing.T) {
	evs := parseEvents([]byte("data: {\"type\":\"a\",\"x\":1}\n\ndata: [DONE]\n\ndata: {\"type\":\"b\"}\n\n"))
	if len(evs) != 2 {
		t.Fatalf("events = %d want 2", len(evs))
	}
}

func TestCollectKeys_Recurses(t *testing.T) {
	keys := map[string]bool{}
	collectKeys(map[string]any{"a": map[string]any{"b": 1}, "c": []any{map[string]any{"d": 2}}}, keys)
	for _, k := range []string{"a", "b", "c", "d"} {
		if !keys[k] {
			t.Errorf("missing key %q", k)
		}
	}
}
