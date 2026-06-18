package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/genaiattr"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

func TestCapText_NoCapPassesThrough(t *testing.T) {
	in := strings.Repeat("a", 200_000)
	if got := capText(in, 0); got != in {
		t.Errorf("capText(_, 0) truncated unexpectedly: len in=%d, len out=%d", len(in), len(got))
	}
	if got := capText(in, -1); got != in {
		t.Errorf("capText(_, -1) truncated unexpectedly: len in=%d, len out=%d", len(in), len(got))
	}
}

func TestCapText_BoundedTruncatesAtByteBoundary(t *testing.T) {
	in := strings.Repeat("x", 100)
	got := capText(in, 10)
	if !strings.HasSuffix(got, "…[truncated]") {
		t.Errorf("capText: missing truncation marker: %q", got)
	}
	if got[:10] != strings.Repeat("x", 10) {
		t.Errorf("capText: prefix = %q, want 10 x's", got[:10])
	}
}

func TestCapText_UnderCapPassesThrough(t *testing.T) {
	in := "short"
	if got := capText(in, 32); got != in {
		t.Errorf("capText: %q != %q", got, in)
	}
}

func TestSanitiseParts_ToolArgsDroppedOverCap(t *testing.T) {
	args := json.RawMessage(`{"k":"` + strings.Repeat("v", 200) + `"}`)
	parts := []genaiattr.Part{{Type: observability.PartTypeToolCall, ID: "t1", Name: "f", Arguments: args}}
	out := sanitiseParts(parts, 4096, 8) // toolArgsCap=8 — definitely too small
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if len(out[0].arguments) != 0 {
		t.Errorf("arguments survived oversize drop: %s", out[0].arguments)
	}
	if out[0].id != "t1" || out[0].name != "f" {
		t.Errorf("id/name lost on oversize drop: id=%q name=%q", out[0].id, out[0].name)
	}
}

func TestSanitiseParts_ToolArgsKeptWhenUnderCap(t *testing.T) {
	args := json.RawMessage(`{"k":"v"}`)
	parts := []genaiattr.Part{{Type: observability.PartTypeToolCall, ID: "t1", Name: "f", Arguments: args}}
	out := sanitiseParts(parts, 4096, 4096)
	if len(out) != 1 || len(out[0].arguments) == 0 {
		t.Fatalf("expected arguments kept, got %+v", out)
	}
}

func TestSanitiseParts_ToolArgsKeptWhenCapIsZero(t *testing.T) {
	args := json.RawMessage(`{"k":"` + strings.Repeat("v", 100_000) + `"}`)
	parts := []genaiattr.Part{{Type: observability.PartTypeToolCall, ID: "t1", Name: "f", Arguments: args}}
	out := sanitiseParts(parts, 0, 0)
	if len(out) != 1 || len(out[0].arguments) == 0 {
		t.Fatalf("toolArgsCap=0 should be unbounded, args dropped anyway: %+v", out)
	}
}

func TestSanitiseParts_TextCappedAtTextCap(t *testing.T) {
	parts := []genaiattr.Part{{Type: "text", Content: strings.Repeat("x", 100)}}
	out := sanitiseParts(parts, 16, 16)
	if !strings.HasSuffix(out[0].content, "…[truncated]") {
		t.Errorf("text not truncated: %q", out[0].content)
	}
}

func TestSanitiseParts_ToolResponseResultCappedAtTextCap(t *testing.T) {
	parts := []genaiattr.Part{{Type: observability.PartTypeToolCallResponse, ID: "t1", Result: strings.Repeat("r", 100)}}
	out := sanitiseParts(parts, 16, 16)
	if !strings.HasSuffix(out[0].result, "…[truncated]") {
		t.Errorf("tool_call_response.result not truncated: %q", out[0].result)
	}
}

func TestSanitiseParts_TextUnboundedWhenCapZero(t *testing.T) {
	body := strings.Repeat("x", 100_000)
	parts := []genaiattr.Part{{Type: "text", Content: body}}
	out := sanitiseParts(parts, 0, 0)
	if out[0].content != body {
		t.Errorf("text truncated under cap=0: len in=%d, len out=%d", len(body), len(out[0].content))
	}
}

func TestToolDefParamsFit_NoCapAlwaysFits(t *testing.T) {
	defs := []genaiattr.ToolDefinition{{Parameters: json.RawMessage(strings.Repeat("p", 100_000))}}
	if !toolDefParamsFit(defs, 0) {
		t.Error("toolDefParamsFit cap=0 must always be true (unbounded)")
	}
}

func TestToolDefParamsFit_DropWholesaleOverCap(t *testing.T) {
	defs := []genaiattr.ToolDefinition{
		{Parameters: json.RawMessage(strings.Repeat("p", 100))},
		{Parameters: json.RawMessage(strings.Repeat("p", 100))},
	}
	if toolDefParamsFit(defs, 150) {
		t.Error("toolDefParamsFit: 200 bytes total under cap=150 reported fit")
	}
	if !toolDefParamsFit(defs, 1000) {
		t.Error("toolDefParamsFit: 200 bytes total under cap=1000 reported not-fit")
	}
}

func TestLogToolDefsValue_DropsParamsOverCap(t *testing.T) {
	defs := []genaiattr.ToolDefinition{
		{Type: "function", Name: "f", Description: "d", Parameters: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)},
	}
	v, ok := logToolDefsValue(defs, 1024, 8) // toolDefsCap too small
	if !ok {
		t.Fatalf("logToolDefsValue: ok=false")
	}
	flat := flattenLogValue(v)
	if strings.Contains(flat, "object") || strings.Contains(flat, "properties") {
		t.Errorf("parameters survived oversize drop: %q", flat)
	}
	if !strings.Contains(flat, "f") {
		t.Errorf("tool name dropped alongside params: %q", flat)
	}
}

func TestLogToolDefsValue_KeepsParamsUnderCap(t *testing.T) {
	defs := []genaiattr.ToolDefinition{
		{Type: "function", Name: "f", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
	v, ok := logToolDefsValue(defs, 1024, 1024)
	if !ok {
		t.Fatalf("logToolDefsValue: ok=false")
	}
	flat := flattenLogValue(v)
	if !strings.Contains(flat, "object") {
		t.Errorf("parameters dropped under cap: %q", flat)
	}
}

func TestLogToolDefsValue_ParamsKeptWhenCapZero(t *testing.T) {
	big := json.RawMessage(`{"x":"` + strings.Repeat("y", 200_000) + `"}`)
	defs := []genaiattr.ToolDefinition{{Type: "function", Name: "f", Parameters: big}}
	v, ok := logToolDefsValue(defs, 0, 0)
	if !ok {
		t.Fatalf("logToolDefsValue: ok=false")
	}
	flat := flattenLogValue(v)
	if !strings.Contains(flat, "yyyy") {
		t.Errorf("toolDefsCap=0 should keep params; flat=%d chars", len(flat))
	}
}

func TestLogToolDefsValue_DescriptionCappedAtDescCap(t *testing.T) {
	defs := []genaiattr.ToolDefinition{{Type: "function", Name: "f", Description: strings.Repeat("d", 200)}}
	v, ok := logToolDefsValue(defs, 16, 0)
	if !ok {
		t.Fatalf("logToolDefsValue: ok=false")
	}
	flat := flattenLogValue(v)
	if !strings.Contains(flat, "…[truncated]") {
		t.Errorf("description not truncated: %q", flat)
	}
}

func TestJSONToolDefsString_DropsParamsOverCap(t *testing.T) {
	defs := []genaiattr.ToolDefinition{
		{Type: "function", Name: "f", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
	s := jsonToolDefsString(defs, 1024, 4)
	if strings.Contains(s, "object") {
		t.Errorf("span json kept parameters over cap: %s", s)
	}
}

func TestJSONToolDefsString_KeepsParamsWhenCapZero(t *testing.T) {
	defs := []genaiattr.ToolDefinition{
		{Type: "function", Name: "f", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
	s := jsonToolDefsString(defs, 0, 0)
	if !strings.Contains(s, "object") {
		t.Errorf("span json dropped parameters under cap=0: %s", s)
	}
}

func TestJSONToolDefsString_DescriptionCapped(t *testing.T) {
	defs := []genaiattr.ToolDefinition{{Type: "function", Name: "f", Description: strings.Repeat("d", 200)}}
	s := jsonToolDefsString(defs, 16, 0)
	if !strings.Contains(s, "truncated") {
		t.Errorf("span description not truncated: %s", s)
	}
}
