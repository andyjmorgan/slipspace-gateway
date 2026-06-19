package responses

import (
	"encoding/json"
	"testing"
)

// The golden tool shapes below are taken from a real Codex (ChatGPT-backend
// Responses) turn captured in slipspace telemetry on 2026-06-08, correlation
// 888a1a80-30cd-4042-9d42-8a2220f2c2f0 (17 tools across all five variants).
// Values are trimmed for readability; the field shapes are verbatim.

func TestUnmarshalTool_Function(t *testing.T) {
	in := []byte(`{` +
		`"description":"Runs a command in a PTY.",` +
		`"name":"exec_command",` +
		`"parameters":{"properties":{"cmd":{"type":"string"}},"required":["cmd"],"type":"object"},` +
		`"strict":false,` +
		`"type":"function"` +
		`}`)
	v, err := UnmarshalTool(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ft, ok := v.(*FunctionTool)
	if !ok {
		t.Fatalf("got %T, want *FunctionTool", v)
	}
	if ft.ToolType() != "function" || ft.Name != "exec_command" {
		t.Fatalf("type=%q name=%q", ft.ToolType(), ft.Name)
	}
	if ft.Strict == nil || *ft.Strict {
		t.Fatalf("strict = %v", ft.Strict)
	}
	roundTripJSON(t, in, v)
}

func TestUnmarshalTool_Custom(t *testing.T) {
	in := []byte(`{` +
		`"description":"Use the apply_patch tool to edit files.",` +
		`"format":{"definition":"start: begin_patch","syntax":"lark","type":"grammar"},` +
		`"name":"apply_patch",` +
		`"type":"custom"` +
		`}`)
	v, err := UnmarshalTool(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ct, ok := v.(*CustomTool)
	if !ok {
		t.Fatalf("got %T, want *CustomTool", v)
	}
	if ct.ToolType() != "custom" || ct.Name != "apply_patch" {
		t.Fatalf("type=%q name=%q", ct.ToolType(), ct.Name)
	}
	if ct.Format.Type != "grammar" || ct.Format.Syntax != "lark" || ct.Format.Definition == "" {
		t.Fatalf("format = %+v", ct.Format)
	}
	roundTripJSON(t, in, v)
}

func TestUnmarshalTool_ToolSearch(t *testing.T) {
	in := []byte(`{` +
		`"description":"Searches over deferred tool metadata.",` +
		`"execution":"client",` +
		`"parameters":{"properties":{"query":{"type":"string"}},"required":["query"],"type":"object"},` +
		`"type":"tool_search"` +
		`}`)
	v, err := UnmarshalTool(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ts, ok := v.(*ToolSearchTool)
	if !ok {
		t.Fatalf("got %T, want *ToolSearchTool", v)
	}
	if ts.ToolType() != "tool_search" || ts.Execution != "client" {
		t.Fatalf("type=%q execution=%q", ts.ToolType(), ts.Execution)
	}
	roundTripJSON(t, in, v)
}

func TestUnmarshalTool_WebSearch(t *testing.T) {
	in := []byte(`{"external_web_access":false,"search_content_types":["text","image"],"type":"web_search"}`)
	v, err := UnmarshalTool(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ws, ok := v.(*WebSearchTool)
	if !ok {
		t.Fatalf("got %T, want *WebSearchTool", v)
	}
	if ws.ToolType() != "web_search" {
		t.Fatalf("type = %q", ws.ToolType())
	}
	if ws.ExternalWebAccess == nil || *ws.ExternalWebAccess {
		t.Fatalf("external_web_access = %v", ws.ExternalWebAccess)
	}
	if len(ws.SearchContentTypes) != 2 || ws.SearchContentTypes[0] != "text" {
		t.Fatalf("search_content_types = %v", ws.SearchContentTypes)
	}
	roundTripJSON(t, in, v)
}

func TestUnmarshalTool_ImageGeneration(t *testing.T) {
	in := []byte(`{"output_format":"png","type":"image_generation"}`)
	v, err := UnmarshalTool(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ig, ok := v.(*ImageGenerationTool)
	if !ok {
		t.Fatalf("got %T, want *ImageGenerationTool", v)
	}
	if ig.ToolType() != "image_generation" || ig.OutputFormat != "png" {
		t.Fatalf("type=%q output_format=%q", ig.ToolType(), ig.OutputFormat)
	}
	roundTripJSON(t, in, v)
}

func TestUnmarshalTool_UnknownDiscriminatorRoundTrips(t *testing.T) {
	in := []byte(`{"future_field":42,"knob":"x","type":"future_tool"}`)
	v, err := UnmarshalTool(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	u, ok := v.(*UnknownTool)
	if !ok {
		t.Fatalf("got %T, want *UnknownTool", v)
	}
	if u.Type != "future_tool" || u.ToolType() != "future_tool" {
		t.Fatalf("type=%q toolType=%q", u.Type, u.ToolType())
	}
	if string(u.Extra["future_field"]) != `42` || string(u.Extra["knob"]) != `"x"` {
		t.Fatalf("extras not preserved: %v", u.Extra)
	}
	roundTripJSON(t, in, v)
}

func TestUnmarshalTools_MixedSlice(t *testing.T) {
	in := []byte(`[` +
		`{"name":"exec_command","type":"function"},` +
		`{"format":{"syntax":"lark","type":"grammar"},"name":"apply_patch","type":"custom"},` +
		`{"execution":"client","type":"tool_search"},` +
		`{"external_web_access":true,"type":"web_search"},` +
		`{"output_format":"png","type":"image_generation"},` +
		`{"type":"future_tool"}` +
		`]`)
	tools, err := UnmarshalTools(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"function", "custom", "tool_search", "web_search", "image_generation", "future_tool"}
	if len(tools) != len(want) {
		t.Fatalf("len = %d, want %d", len(tools), len(want))
	}
	for i, td := range tools {
		if td.ToolType() != want[i] {
			t.Fatalf("elem %d type = %q want %q", i, td.ToolType(), want[i])
		}
	}
	if _, ok := tools[len(tools)-1].(*UnknownTool); !ok {
		t.Fatalf("last elem = %T, want *UnknownTool", tools[len(tools)-1])
	}
}

func TestToolList_RoundTripsOnRequest(t *testing.T) {
	in := []byte(`{` +
		`"input":"hi",` +
		`"model":"gpt-5-codex",` +
		`"tools":[` +
		`{"name":"exec_command","type":"function"},` +
		`{"output_format":"png","type":"image_generation"},` +
		`{"type":"future_tool"}` +
		`]}`)
	var req ResponsesRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.Tools) != 3 {
		t.Fatalf("tools len = %d", len(req.Tools))
	}
	if req.Tools[0].ToolType() != "function" || req.Tools[2].ToolType() != "future_tool" {
		t.Fatalf("tool types = %q, %q", req.Tools[0].ToolType(), req.Tools[2].ToolType())
	}
	roundTripJSON(t, in, &req)
}

func TestToolList_NullAndEmpty(t *testing.T) {
	var tl ToolList
	if err := tl.UnmarshalJSON([]byte("null")); err != nil {
		t.Fatalf("null: %v", err)
	}
	if tl != nil {
		t.Fatalf("null should leave nil, got %v", tl)
	}
	if err := tl.UnmarshalJSON([]byte(`[]`)); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if tl == nil || len(tl) != 0 {
		t.Fatalf("empty array should be non-nil zero-len, got %v", tl)
	}
}

func TestTools_RegistryHasFallback(t *testing.T) {
	if toolRegistry.Fallback == nil {
		t.Fatalf("toolRegistry must have a fallback")
	}
	want := map[string]bool{
		"function":         false,
		"custom":           false,
		"tool_search":      false,
		"web_search":       false,
		"image_generation": false,
	}
	for k := range toolRegistry.Factories {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected tool factory %q", k)
		}
		want[k] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("missing tool factory %q", k)
		}
	}
}
