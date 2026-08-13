package genaiattr_test

import (
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/genaiattr"
)

// textOf joins the text-part content of a parts slice, for terse assertions.
func textOf(parts []genaiattr.Part) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Content)
		}
	}
	return b.String()
}

func userText(c genaiattr.Content) string {
	if c.InputMessage == nil {
		return ""
	}
	return textOf(c.InputMessage.Parts)
}

func TestExtractContent_OpenAIChat(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"messages": [
			{"role": "system", "content": "Be terse."},
			{"role": "developer", "content": "Use UK spelling."},
			{"role": "user", "content": "first question"},
			{"role": "assistant", "content": "an answer"},
			{"role": "user", "content": "latest question"}
		],
		"tools": [{"type": "function", "function": {"name": "get_weather", "description": "weather", "parameters": {"type": "object"}}}]
	}`)
	c := genaiattr.ExtractContent("chat_completions", raw)
	// Both system AND developer messages contribute, in order (not first-only).
	if got := textOf(c.SystemInstructions); got != "Be terse.Use UK spelling." {
		t.Errorf("system parts = %q, want both system+developer", got)
	}
	if len(c.SystemInstructions) != 2 {
		t.Errorf("system parts count = %d, want 2", len(c.SystemInstructions))
	}
	// Only the latest user turn is kept (bounded).
	if userText(c) != "latest question" {
		t.Errorf("latest user = %q, want 'latest question'", userText(c))
	}
	if len(c.ToolDefinitions) != 1 || c.ToolDefinitions[0].Name != "get_weather" || c.ToolDefinitions[0].Type != "function" {
		t.Fatalf("tool defs = %+v", c.ToolDefinitions)
	}
	if !strings.Contains(string(c.ToolDefinitions[0].Parameters), "object") {
		t.Errorf("tool params = %s", c.ToolDefinitions[0].Parameters)
	}
}

func TestExtractContent_OpenAIChat_MultiPartUserTurn(t *testing.T) {
	t.Parallel()
	// Array content (vision-style): text parts preserved as separate parts,
	// image block passes through by type with no inline bytes.
	raw := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello "},{"type":"image_url","image_url":{"url":"data:..."}},{"type":"text","text":"world"}]}]}`)
	c := genaiattr.ExtractContent("chat_completions", raw)
	if c.InputMessage == nil || len(c.InputMessage.Parts) != 3 {
		t.Fatalf("input parts = %+v", c.InputMessage)
	}
	if userText(c) != "hello world" {
		t.Errorf("text = %q", userText(c))
	}
	if c.InputMessage.Parts[1].Type != "image_url" || c.InputMessage.Parts[1].Content != "" {
		t.Errorf("image part should pass through by type with no bytes: %+v", c.InputMessage.Parts[1])
	}
	if c.ToolDefinitions != nil {
		t.Errorf("tools should be nil when absent, got %+v", c.ToolDefinitions)
	}
}

func TestExtractContent_Anthropic(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"system":"You are Claude.","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"},{"role":"user","content":"bye"}],"tools":[{"name":"t","description":"d","input_schema":{"type":"object"}}]}`)
	c := genaiattr.ExtractContent("messages", raw)
	if textOf(c.SystemInstructions) != "You are Claude." {
		t.Errorf("system = %q", textOf(c.SystemInstructions))
	}
	if userText(c) != "bye" {
		t.Errorf("latest user = %q, want 'bye'", userText(c))
	}
	// input_schema normalises to parameters under a function-typed tool.
	if len(c.ToolDefinitions) != 1 || c.ToolDefinitions[0].Type != "function" || c.ToolDefinitions[0].Name != "t" {
		t.Fatalf("tool defs = %+v", c.ToolDefinitions)
	}
	if !strings.Contains(string(c.ToolDefinitions[0].Parameters), "object") {
		t.Errorf("anthropic input_schema not mapped to parameters: %s", c.ToolDefinitions[0].Parameters)
	}
}

func TestExtractContent_Anthropic_SystemBlocks(t *testing.T) {
	t.Parallel()
	// Anthropic system may be an array of text blocks; each becomes a part.
	raw := []byte(`{"system":[{"type":"text","text":"part one"},{"type":"text","text":"part two"}],"messages":[{"role":"user","content":"q"}]}`)
	c := genaiattr.ExtractContent("messages", raw)
	if len(c.SystemInstructions) != 2 {
		t.Fatalf("system parts = %d, want 2 (one per block)", len(c.SystemInstructions))
	}
	if c.SystemInstructions[0].Content != "part one" || c.SystemInstructions[1].Content != "part two" {
		t.Errorf("system blocks not preserved separately: %+v", c.SystemInstructions)
	}
}

func TestExtractContent_Anthropic_ToolUseAndResult(t *testing.T) {
	t.Parallel()
	// A user turn carrying a tool_result block maps to tool_call_response.
	raw := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"42 degrees"},{"type":"text","text":"thanks"}]}]}`)
	c := genaiattr.ExtractContent("messages", raw)
	if c.InputMessage == nil || len(c.InputMessage.Parts) != 2 {
		t.Fatalf("parts = %+v", c.InputMessage)
	}
	p := c.InputMessage.Parts[0]
	if p.Type != "tool_call_response" || p.ID != "toolu_1" || p.Result != "42 degrees" {
		t.Errorf("tool_result part = %+v", p)
	}
	if c.InputMessage.Role != "user" {
		t.Errorf("mixed text/tool-result role = %q, want user", c.InputMessage.Role)
	}
}

func TestExtractContent_Anthropic_PureToolResultsUseToolRole(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"messages":[{"role":"user","content":[
		{"type":"tool_result","tool_use_id":"toolu_1","content":"sunny"},
		{"type":"tool_result","tool_use_id":"toolu_2","content":"rainy"}
	]}]}`)
	c := genaiattr.ExtractContent("messages", raw)
	if c.InputMessage == nil || c.InputMessage.Role != "tool" {
		t.Fatalf("input message = %+v, want role tool", c.InputMessage)
	}
	if len(c.InputMessage.Parts) != 2 {
		t.Fatalf("parts = %+v, want two tool responses", c.InputMessage.Parts)
	}
	for i, p := range c.InputMessage.Parts {
		if p.Type != "tool_call_response" {
			t.Errorf("part %d = %+v, want tool_call_response", i, p)
		}
	}
}

func TestExtractContent_Anthropic_Thinking(t *testing.T) {
	t.Parallel()
	// thinking + text + redacted_thinking in a turn map to ordered parts:
	// reasoning (with content), text, reasoning (redacted, no content).
	raw := []byte(`{"messages":[{"role":"user","content":[{"type":"thinking","thinking":"reason","signature":"s"},{"type":"text","text":"answer"},{"type":"redacted_thinking","data":"blob"}]}]}`)
	c := genaiattr.ExtractContent("messages", raw)
	if c.InputMessage == nil || len(c.InputMessage.Parts) != 3 {
		t.Fatalf("parts = %+v", c.InputMessage)
	}
	p := c.InputMessage.Parts
	if p[0].Type != "reasoning" || p[0].Content != "reason" {
		t.Errorf("part0 = %+v, want reasoning/reason", p[0])
	}
	if p[1].Type != "text" || p[1].Content != "answer" {
		t.Errorf("part1 = %+v, want text/answer", p[1])
	}
	if p[2].Type != "reasoning" || p[2].Content != "" {
		t.Errorf("part2 (redacted) = %+v, want reasoning/empty", p[2])
	}
}

func TestExtractContent_Gemini(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"systemInstruction":{"parts":[{"text":"sys"}]},"contents":[{"role":"user","parts":[{"text":"q1"}]},{"role":"model","parts":[{"text":"a1"}]},{"role":"user","parts":[{"text":"q2"}]}],"tools":[{"functionDeclarations":[{"name":"f1","parameters":{"type":"object"}},{"name":"f2"}]}]}`)
	c := genaiattr.ExtractContent("generate_content", raw)
	if textOf(c.SystemInstructions) != "sys" {
		t.Errorf("system = %q", textOf(c.SystemInstructions))
	}
	if userText(c) != "q2" {
		t.Errorf("latest user = %q, want q2", userText(c))
	}
	// functionDeclarations flatten — two declarations become two tools.
	if len(c.ToolDefinitions) != 2 || c.ToolDefinitions[0].Name != "f1" || c.ToolDefinitions[1].Name != "f2" {
		t.Fatalf("tool defs = %+v", c.ToolDefinitions)
	}
	if c.ToolDefinitions[0].Type != "function" {
		t.Errorf("gemini tool type = %q, want function", c.ToolDefinitions[0].Type)
	}
}

func TestExtractContent_Gemini_FunctionCallTurn(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"contents":[{"role":"user","parts":[{"functionCall":{"name":"get_weather","args":{"loc":"Paris"}}}]}]}`)
	c := genaiattr.ExtractContent("generate_content", raw)
	if c.InputMessage == nil || len(c.InputMessage.Parts) != 1 {
		t.Fatalf("parts = %+v", c.InputMessage)
	}
	p := c.InputMessage.Parts[0]
	if p.Type != "tool_call" || p.Name != "get_weather" || !strings.Contains(string(p.Arguments), "Paris") {
		t.Errorf("functionCall part = %+v", p)
	}
}

func TestExtractContent_Gemini_BuiltinTools(t *testing.T) {
	t.Parallel()
	// Built-in tools carry no functionDeclaration — they are key-discriminated
	// markers — but a request that enabled them should still surface a named
	// tool def, mirroring the Anthropic server-tool listing.
	raw := []byte(`{"contents":[{"role":"user","parts":[{"text":"latest F1 champion?"}]}],` +
		`"tools":[{"googleSearch":{}},{"codeExecution":{}},{"urlContext":{}},{"googleSearchRetrieval":{}},` +
		`{"functionDeclarations":[{"name":"get_weather","parameters":{"type":"object"}}]}]}`)
	c := genaiattr.ExtractContent("generate_content", raw)
	got := map[string]bool{}
	for _, d := range c.ToolDefinitions {
		got[d.Name] = true
	}
	for _, want := range []string{"google_search", "code_execution", "url_context", "google_search_retrieval", "get_weather"} {
		if !got[want] {
			t.Errorf("tool defs %+v missing %q", c.ToolDefinitions, want)
		}
	}
}

func TestExtractContent_OpenAIResponses(t *testing.T) {
	t.Parallel()
	// Responses API: instructions = system, input = prompt (string form).
	// Tools are FLAT — {type,name,description,parameters} — not nested under a
	// "function" object the way Chat Completions encodes them.
	c := genaiattr.ExtractContent("responses", []byte(`{"instructions":"be brief","input":"hello there","tools":[{"type":"function","name":"f","description":"d","parameters":{"type":"object"}}]}`))
	if textOf(c.SystemInstructions) != "be brief" {
		t.Errorf("system = %q", textOf(c.SystemInstructions))
	}
	if userText(c) != "hello there" {
		t.Errorf("latest user (string input) = %q", userText(c))
	}
	if len(c.ToolDefinitions) != 1 || c.ToolDefinitions[0].Name != "f" || c.ToolDefinitions[0].Description != "d" {
		t.Fatalf("tools = %+v", c.ToolDefinitions)
	}
	if !strings.Contains(string(c.ToolDefinitions[0].Parameters), "object") {
		t.Errorf("flat responses parameters not captured: %s", c.ToolDefinitions[0].Parameters)
	}
}

func TestExtractContent_OpenAIResponses_MixedToolCatalogue(t *testing.T) {
	t.Parallel()
	// A Codex-shaped catalogue: a flat function tool, a custom tool, and
	// built-in tools that carry only a type. Each must keep its name (where it
	// has one) and its type — the pre-fix parser dropped every name because it
	// looked for a nested "function" object.
	raw := []byte(`{"input":"x","tools":[` +
		`{"type":"function","name":"shell","description":"run","parameters":{"type":"object"}},` +
		`{"type":"custom","name":"apply_patch","description":"patch"},` +
		`{"type":"tool_search"},{"type":"web_search"},{"type":"image_generation"},` +
		`{"type":"file_search"},{"type":"code_interpreter"},{"type":"mcp"},` +
		`{"type":"future_thing"}]}`)
	c := genaiattr.ExtractContent("responses", raw)
	if len(c.ToolDefinitions) != 9 {
		t.Fatalf("tool count = %d, want 9: %+v", len(c.ToolDefinitions), c.ToolDefinitions)
	}
	if td := c.ToolDefinitions[0]; td.Type != "function" || td.Name != "shell" || !strings.Contains(string(td.Parameters), "object") {
		t.Errorf("function tool = %+v", td)
	}
	if td := c.ToolDefinitions[1]; td.Type != "custom" || td.Name != "apply_patch" || td.Description != "patch" {
		t.Errorf("custom tool = %+v", td)
	}
	// Built-in and unknown/future types pass through carrying only their type —
	// no name, description, or parameters — and are never dropped.
	for i, wantType := range []string{"tool_search", "web_search", "image_generation", "file_search", "code_interpreter", "mcp", "future_thing"} {
		if td := c.ToolDefinitions[2+i]; td.Type != wantType || td.Name != "" || td.Description != "" || td.Parameters != nil {
			t.Errorf("type-only tool[%d] = %+v, want type %q with no name/desc/params", i, td, wantType)
		}
	}
}

func TestExtractContent_OpenAIResponses_SystemInInput(t *testing.T) {
	t.Parallel()
	// System/developer items embedded in input[] also feed system_instructions
	// (alongside top-level instructions), and the latest user item wins.
	raw := []byte(`{"instructions":"top","input":[{"role":"developer","content":[{"type":"input_text","text":"dev rule"}]},{"role":"user","content":[{"type":"input_text","text":"q1"}]},{"role":"user","content":[{"type":"input_text","text":"q2"}]}]}`)
	c := genaiattr.ExtractContent("responses", raw)
	if got := textOf(c.SystemInstructions); got != "topdev rule" {
		t.Errorf("system = %q, want instructions + input developer item", got)
	}
	if userText(c) != "q2" {
		t.Errorf("latest user (array input) = %q, want q2", userText(c))
	}
}

func TestExtractContent_PartEdgeCases(t *testing.T) {
	t.Parallel()

	// An untyped block carrying text is treated as a text part.
	if c := genaiattr.ExtractContent("chat_completions", []byte(`{"messages":[{"role":"user","content":[{"text":"bare"}]}]}`)); userText(c) != "bare" {
		t.Errorf("untyped text part = %q, want 'bare'", userText(c))
	}

	// Content that is neither a string nor an array yields no parts.
	c := genaiattr.ExtractContent("chat_completions", []byte(`{"messages":[{"role":"user","content":5}]}`))
	if c.InputMessage == nil || len(c.InputMessage.Parts) != 0 {
		t.Errorf("non-string/array content should yield no parts: %+v", c.InputMessage)
	}

	// An OpenAI tool without an explicit type defaults to "function".
	if c := genaiattr.ExtractContent("chat_completions", []byte(`{"tools":[{"function":{"name":"f"}}],"messages":[]}`)); len(c.ToolDefinitions) != 1 || c.ToolDefinitions[0].Type != "function" {
		t.Errorf("default tool type = %+v", c.ToolDefinitions)
	}

	// A Gemini functionResponse part maps to tool_call_response.
	c4 := genaiattr.ExtractContent("generate_content", []byte(`{"contents":[{"role":"user","parts":[{"functionResponse":{"name":"f","response":{"ok":true}}}]}]}`))
	if c4.InputMessage == nil || len(c4.InputMessage.Parts) != 1 || c4.InputMessage.Parts[0].Type != "tool_call_response" {
		t.Fatalf("functionResponse part = %+v", c4.InputMessage)
	}
	if c4.InputMessage.Parts[0].Name != "f" {
		t.Errorf("functionResponse name = %q", c4.InputMessage.Parts[0].Name)
	}
	if c4.InputMessage.Role != "tool" {
		t.Errorf("pure functionResponse role = %q, want tool", c4.InputMessage.Role)
	}

	// Text mixed with a function response remains a user turn.
	c4Mixed := genaiattr.ExtractContent("generate_content", []byte(`{"contents":[{"role":"user","parts":[{"functionResponse":{"name":"f","response":{"ok":true}}},{"text":"continue"}]}]}`))
	if c4Mixed.InputMessage == nil || c4Mixed.InputMessage.Role != "user" {
		t.Errorf("mixed Gemini input = %+v, want role user", c4Mixed.InputMessage)
	}

	// An Anthropic tool_result with array content flattens text + content
	// fields to the result string.
	c5 := genaiattr.ExtractContent("messages", []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"r1"},{"content":"r2"}]}]}]}`))
	if c5.InputMessage == nil || c5.InputMessage.Parts[0].Result != "r1r2" {
		t.Errorf("tool_result array content = %+v", c5.InputMessage)
	}
}

// toolResponseOf returns the first tool_call_response part of the input turn,
// or a zero Part when none is present.
func toolResponseOf(c genaiattr.Content) genaiattr.Part {
	if c.InputMessage == nil {
		return genaiattr.Part{}
	}
	for _, p := range c.InputMessage.Parts {
		if p.Type == "tool_call_response" {
			return p
		}
	}
	return genaiattr.Part{}
}

// TestExtractContent_ToolResponseCapture asserts a tool result is recorded as
// a tool_call_response part on every protocol, using the REAL captured wire
// shapes of a tool-continuation request (the turn where the client feeds the
// tool's output back to the model). Pre-fix the OpenAI chat tool turn and the
// Responses function_call_output were silently dropped; Gemini under role
// "function" was dropped; Anthropic already worked (regression guard).
func TestExtractContent_ToolResponseCapture(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		endpoint   string
		raw        string
		wantID     string
		wantResult string // substring expected in Result
	}{
		{
			// Real OpenAI chat capture: trailing role:"tool" message.
			name:     "openai_chat",
			endpoint: "chat_completions",
			raw: `{
				"messages": [
					{"role": "user", "content": "What's the weather in Paris? Use the tool."},
					{"role": "assistant", "annotations": [], "tool_calls": [{"id": "call_noziK0eYcDqsVyWYFAan0uXI", "function": {"arguments": "{\"location\":\"Paris\"}", "name": "get_weather"}, "type": "function"}]},
					{"role": "tool", "tool_call_id": "call_noziK0eYcDqsVyWYFAan0uXI", "content": "18C and sunny"}
				],
				"model": "gpt-4o-mini",
				"tools": [{"type": "function", "function": {"name": "get_weather", "description": "Get current weather for a city.", "parameters": {"type": "object"}}}]
			}`,
			wantID:     "call_noziK0eYcDqsVyWYFAan0uXI",
			wantResult: "18C and sunny",
		},
		{
			// Real OpenAI Responses capture: type:"function_call_output" item
			// (no role), preceded by user + function_call items.
			name:     "openai_responses",
			endpoint: "responses",
			raw: `{
				"input": [
					{"role": "user", "content": "What's the weather in Paris? Use the tool."},
					{"arguments": "{\"location\":\"Paris\"}", "call_id": "call_E94IoACfD2nvIxqrFRJxwQEQ", "name": "get_weather", "type": "function_call", "id": "fc_0e91", "status": "completed"},
					{"type": "function_call_output", "call_id": "call_E94IoACfD2nvIxqrFRJxwQEQ", "output": "18C and sunny"}
				],
				"model": "gpt-4o-mini"
			}`,
			wantID:     "call_E94IoACfD2nvIxqrFRJxwQEQ",
			wantResult: "18C and sunny",
		},
		{
			// OpenAI custom-tool continuation uses a distinct output-item type
			// but the same call_id/output join contract.
			name:     "openai_responses_custom_tool",
			endpoint: "responses",
			raw: `{
				"input": [
					{"type":"custom_tool_call","id":"ctc_1","call_id":"call_custom_1","name":"exec","input":"print(1)"},
					{"type":"custom_tool_call_output","call_id":"call_custom_1","output":"1"}
				]
			}`,
			wantID:     "call_custom_1",
			wantResult: "1",
		},
		{
			// Real Gemini capture: functionResponse part under role "user".
			name:     "gemini_user_role",
			endpoint: "generate_content",
			raw: `{
				"contents": [
					{"parts": [{"text": "What's the weather in Paris? Use the tool."}], "role": "user"},
					{"parts": [{"functionCall": {"args": {"location": "Paris"}, "name": "get_weather"}}], "role": "model"},
					{"parts": [{"functionResponse": {"name": "get_weather", "response": {"result": "18C and sunny"}}}], "role": "user"}
				]
			}`,
			wantID:     "",
			wantResult: "18C and sunny",
		},
		{
			// Gemini under the explicit "function" role (some clients use it).
			name:     "gemini_function_role",
			endpoint: "generate_content",
			raw: `{
				"contents": [
					{"parts": [{"functionResponse": {"name": "get_weather", "response": {"result": "18C and sunny"}}}], "role": "function"}
				]
			}`,
			wantID:     "",
			wantResult: "18C and sunny",
		},
		{
			// Anthropic tool_result block in a user turn (regression guard).
			name:     "anthropic",
			endpoint: "messages",
			raw: `{
				"messages": [
					{"role": "user", "content": "What's the weather in Paris? Use the tool."},
					{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_01", "name": "get_weather", "input": {"location": "Paris"}}]},
					{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_01", "content": "18C and sunny"}]}
				]
			}`,
			wantID:     "toolu_01",
			wantResult: "18C and sunny",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := genaiattr.ExtractContent(tc.endpoint, []byte(tc.raw))
			p := toolResponseOf(c)
			if p.Type != "tool_call_response" {
				t.Fatalf("no tool_call_response part captured; input turn = %+v", c.InputMessage)
			}
			if p.ID != tc.wantID {
				t.Errorf("tool_call_response ID = %q, want %q", p.ID, tc.wantID)
			}
			if !strings.Contains(p.Result, tc.wantResult) {
				t.Errorf("tool_call_response Result = %q, want substring %q", p.Result, tc.wantResult)
			}
		})
	}
}

// TestExtractContent_OpenAIChat_MultipleToolResults asserts that a parallel
// tool-call continuation — a contiguous trailing run of several role:"tool"
// messages — captures every tool result as its own tool_call_response part in
// the single input turn.
func TestExtractContent_OpenAIChat_MultipleToolResults(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"messages":[
		{"role":"user","content":"weather in two cities"},
		{"role":"assistant","tool_calls":[{"id":"call_a","type":"function","function":{"name":"w","arguments":"{}"}},{"id":"call_b","type":"function","function":{"name":"w","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_a","content":"sunny"},
		{"role":"tool","tool_call_id":"call_b","content":"rainy"}
	]}`)
	c := genaiattr.ExtractContent("chat_completions", raw)
	if c.InputMessage == nil || len(c.InputMessage.Parts) != 2 {
		t.Fatalf("want 2 tool_call_response parts, got %+v", c.InputMessage)
	}
	for i, want := range []struct{ id, result string }{{"call_a", "sunny"}, {"call_b", "rainy"}} {
		p := c.InputMessage.Parts[i]
		if p.Type != "tool_call_response" || p.ID != want.id || p.Result != want.result {
			t.Errorf("part %d = %+v, want id=%q result=%q", i, p, want.id, want.result)
		}
	}
}

// TestExtractContent_OpenAIChat_UserAfterToolWins asserts the accumulator
// resolves to the last input turn: a user message following a tool run resets
// the group and becomes the input turn.
func TestExtractContent_OpenAIChat_UserAfterToolWins(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"messages":[
		{"role":"tool","tool_call_id":"call_a","content":"sunny"},
		{"role":"user","content":"and tomorrow?"}
	]}`)
	c := genaiattr.ExtractContent("chat_completions", raw)
	if userText(c) != "and tomorrow?" {
		t.Errorf("user turn after tool run should win, got %+v", c.InputMessage)
	}
	if p := toolResponseOf(c); p.Type == "tool_call_response" {
		t.Errorf("tool turn should have been superseded by the trailing user turn, got %+v", p)
	}
}

// FuzzExtractContent asserts ExtractContent never panics across the four
// protocols for arbitrary bodies, seeded with the real tool-continuation wire
// shapes whose value spaces this change touched (trailing tool-message runs,
// function_call_output items, function-role functionResponse turns).
func FuzzExtractContent(f *testing.F) {
	f.Add("chat_completions", `{"messages":[{"role":"tool","tool_call_id":"c","content":"r"}]}`)
	f.Add("responses", `{"input":[{"type":"function_call_output","call_id":"c","output":"r"}]}`)
	f.Add("generate_content", `{"contents":[{"role":"function","parts":[{"functionResponse":{"name":"f","response":{"result":"r"}}}]}]}`)
	f.Add("messages", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"r"}]}]}`)
	f.Add("chat_completions", `{"messages":[{"role":"tool"},{"role":"tool"},{"role":"user","content":"x"}]}`)
	f.Add("responses", `{"input":[{"type":"function_call_output"},{"role":"user","content":"x"}]}`)
	f.Add("chat_completions", `not json`)
	f.Fuzz(func(t *testing.T, endpoint, raw string) {
		// Property: parsing arbitrary input never panics; the result is a
		// well-formed Content (an input turn, when present, is non-nil).
		c := genaiattr.ExtractContent(endpoint, []byte(raw))
		_ = c.SystemInstructions
		_ = c.ToolDefinitions
		if c.InputMessage != nil {
			_ = c.InputMessage.Parts
		}
	})
}

func TestExtractContent_EdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		endpoint string
		raw      string
	}{
		{"empty", "chat_completions", ""},
		{"unrecognised endpoint", "embeddings", `{"input":"x"}`},
		{"models endpoint", "models", `{}`},
		{"malformed", "messages", `{nope`},
		{"gemini empty", "generate_content", `{}`},
		{"null tools", "chat_completions", `{"tools":null,"messages":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := genaiattr.ExtractContent(tc.endpoint, []byte(tc.raw))
			if len(c.SystemInstructions) != 0 || c.InputMessage != nil || c.ToolDefinitions != nil {
				t.Errorf("expected zero Content, got %+v", c)
			}
		})
	}
}
