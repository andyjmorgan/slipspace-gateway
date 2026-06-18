package translate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/protocols/openai/chat"
)

// decodeChat translates an Anthropic Messages request body and decodes the
// resulting OpenAI Chat request for assertions.
func decodeChat(t *testing.T, body string) (chat.ChatCompletionRequest, []Drop) {
	t.Helper()
	out, drops, err := translateMessagesRequestToChat([]byte(body))
	if err != nil {
		t.Fatalf("translateMessagesRequestToChat: %v", err)
	}
	var req chat.ChatCompletionRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("decode translated request: %v\nbody: %s", err, out)
	}
	return req, drops
}

func hasDrop(drops []Drop, field string) bool {
	for _, d := range drops {
		if d.Field == field {
			return true
		}
	}
	return false
}

func TestTranslateRequest_TextAndScalars(t *testing.T) {
	req, drops := decodeChat(t, `{
		"model":"claude-sonnet-4",
		"max_tokens":256,
		"temperature":0.5,
		"top_p":0.9,
		"stream":true,
		"system":"be terse",
		"messages":[{"role":"user","content":"hello"}]
	}`)

	if req.Model != "claude-sonnet-4" {
		t.Errorf("Model = %q, want claude-sonnet-4 (verbatim)", req.Model)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 256 {
		t.Errorf("MaxTokens = %v, want 256", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0.5 {
		t.Errorf("Temperature = %v, want 0.5", req.Temperature)
	}
	if req.TopP == nil || *req.TopP != 0.9 {
		t.Errorf("TopP = %v, want 0.9", req.TopP)
	}
	if !req.Stream {
		t.Error("Stream = false, want true")
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (system + user)", len(req.Messages))
	}
	if req.Messages[0].Role() != "system" {
		t.Errorf("messages[0] role = %q, want system", req.Messages[0].Role())
	}
	sys, ok := req.Messages[0].(*chat.SystemMessage)
	if !ok || sys.Content != "be terse" {
		t.Errorf("system content = %+v, want 'be terse'", req.Messages[0])
	}
	user, ok := req.Messages[1].(*chat.UserMessage)
	if !ok {
		t.Fatalf("messages[1] = %T, want *chat.UserMessage", req.Messages[1])
	}
	if s, _ := user.Content.AsString(); s != "hello" {
		t.Errorf("user content = %q, want hello", s)
	}
	if len(drops) != 0 {
		t.Errorf("unexpected drops: %+v", drops)
	}
}

func TestTranslateRequest_SystemBlocksDropCacheControl(t *testing.T) {
	req, drops := decodeChat(t, `{
		"model":"m","max_tokens":1,
		"system":[
			{"type":"text","text":"line one","cache_control":{"type":"ephemeral"}},
			{"type":"text","text":"line two"}
		],
		"messages":[{"role":"user","content":"hi"}]
	}`)
	sys, ok := req.Messages[0].(*chat.SystemMessage)
	if !ok {
		t.Fatalf("messages[0] = %T, want SystemMessage", req.Messages[0])
	}
	if sys.Content != "line one\n\nline two" {
		t.Errorf("joined system = %q, want 'line one\\n\\nline two'", sys.Content)
	}
	if !hasDrop(drops, "system[0].cache_control") {
		t.Errorf("expected system[0].cache_control drop, got %+v", drops)
	}
}

func TestTranslateRequest_StopSequences(t *testing.T) {
	req, _ := decodeChat(t, `{"model":"m","max_tokens":1,"stop_sequences":["END","STOP"],"messages":[{"role":"user","content":"x"}]}`)
	var stop []string
	if err := json.Unmarshal(req.Stop, &stop); err != nil {
		t.Fatalf("decode stop: %v", err)
	}
	if len(stop) != 2 || stop[0] != "END" || stop[1] != "STOP" {
		t.Errorf("stop = %v, want [END STOP]", stop)
	}
}

func TestTranslateRequest_ToolsAndToolChoice(t *testing.T) {
	tests := []struct {
		name          string
		toolChoice    string
		wantRawChoice string
		wantParallel  *bool
	}{
		{"auto", `{"type":"auto"}`, `"auto"`, nil},
		{"any", `{"type":"any"}`, `"required"`, nil},
		{"none", `{"type":"none"}`, `"none"`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"model":"m","max_tokens":1,
				"tools":[{"name":"get_weather","description":"d","input_schema":{"type":"object","properties":{"q":{"type":"string"}}},"cache_control":{"type":"ephemeral"}}],
				"tool_choice":` + tt.toolChoice + `,
				"messages":[{"role":"user","content":"x"}]}`
			req, drops := decodeChat(t, body)
			if len(req.Tools) != 1 || req.Tools[0].Type != "function" || req.Tools[0].Function == nil {
				t.Fatalf("tools = %+v, want one function tool", req.Tools)
			}
			if req.Tools[0].Function.Name != "get_weather" {
				t.Errorf("tool name = %q, want get_weather", req.Tools[0].Function.Name)
			}
			if len(req.Tools[0].Function.Parameters) == 0 {
				t.Error("tool parameters not carried from input_schema")
			}
			if string(req.ToolChoice) != tt.wantRawChoice {
				t.Errorf("tool_choice = %s, want %s", req.ToolChoice, tt.wantRawChoice)
			}
			if !hasDrop(drops, "tools[0].cache_control") {
				t.Errorf("expected tools[0].cache_control drop, got %+v", drops)
			}
		})
	}
}

func TestTranslateRequest_ToolChoiceSpecificTool(t *testing.T) {
	req, _ := decodeChat(t, `{"model":"m","max_tokens":1,
		"tool_choice":{"type":"tool","name":"calc","disable_parallel_tool_use":true},
		"messages":[{"role":"user","content":"x"}]}`)
	var tc struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(req.ToolChoice, &tc); err != nil {
		t.Fatalf("decode tool_choice: %v (%s)", err, req.ToolChoice)
	}
	if tc.Type != "function" || tc.Function.Name != "calc" {
		t.Errorf("tool_choice = %+v, want function/calc", tc)
	}
	if req.ParallelToolCalls == nil || *req.ParallelToolCalls != false {
		t.Errorf("ParallelToolCalls = %v, want false (disable_parallel_tool_use=true)", req.ParallelToolCalls)
	}
}

func TestTranslateRequest_AssistantToolUseAndUserToolResult(t *testing.T) {
	// A realistic tool round-trip: assistant emits a tool_use, the user turn
	// carries the tool_result. OpenAI needs: assistant(tool_calls) then a
	// separate tool message correlated by id.
	req, _ := decodeChat(t, `{
		"model":"m","max_tokens":1,
		"messages":[
			{"role":"user","content":"weather?"},
			{"role":"assistant","content":[
				{"type":"text","text":"checking"},
				{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"72F"}
			]}
		]
	}`)

	// system absent → messages: user, assistant(+toolcall), tool
	if len(req.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(req.Messages))
	}
	asst, ok := req.Messages[1].(*chat.AssistantMessage)
	if !ok {
		t.Fatalf("messages[1] = %T, want AssistantMessage", req.Messages[1])
	}
	if s, _ := asst.Content.AsString(); s != "checking" {
		t.Errorf("assistant content = %q, want checking", s)
	}
	if len(asst.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %d, want 1", len(asst.ToolCalls))
	}
	tcCall := asst.ToolCalls[0]
	if tcCall.ID != "toolu_1" || tcCall.Type != "function" || tcCall.Function.Name != "get_weather" {
		t.Errorf("tool_call = %+v, want toolu_1/function/get_weather", tcCall)
	}
	if !strings.Contains(tcCall.Function.Arguments, `"city"`) {
		t.Errorf("tool_call arguments = %q, want JSON string containing city", tcCall.Function.Arguments)
	}
	tm, ok := req.Messages[2].(*chat.ToolMessage)
	if !ok {
		t.Fatalf("messages[2] = %T, want ToolMessage", req.Messages[2])
	}
	if tm.ToolCallID != "toolu_1" {
		t.Errorf("tool message tool_call_id = %q, want toolu_1", tm.ToolCallID)
	}
	if s, _ := tm.Content.AsString(); s != "72F" {
		t.Errorf("tool message content = %q, want 72F", s)
	}
}

func TestTranslateRequest_Images(t *testing.T) {
	req, _ := decodeChat(t, `{
		"model":"m","max_tokens":1,
		"messages":[{"role":"user","content":[
			{"type":"text","text":"look"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}},
			{"type":"image","source":{"type":"url","url":"https://x/y.png"}}
		]}]
	}`)
	user, ok := req.Messages[0].(*chat.UserMessage)
	if !ok {
		t.Fatalf("messages[0] = %T, want UserMessage", req.Messages[0])
	}
	parts, err := user.Content.AsParts()
	if err != nil {
		t.Fatalf("content parts: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("parts = %d, want 3", len(parts))
	}
	img1, ok := parts[1].(*chat.ImageURLContentPart)
	if !ok || img1.ImageURL.URL != "data:image/png;base64,AAAA" {
		t.Errorf("parts[1] = %+v, want base64 data URL", parts[1])
	}
	img2, ok := parts[2].(*chat.ImageURLContentPart)
	if !ok || img2.ImageURL.URL != "https://x/y.png" {
		t.Errorf("parts[2] = %+v, want passthrough URL", parts[2])
	}
}

func TestTranslateRequest_Drops(t *testing.T) {
	_, drops := decodeChat(t, `{
		"model":"m","max_tokens":1,
		"top_k":40,
		"thinking":{"type":"enabled","budget_tokens":1024},
		"metadata":{"user_id":"u1","trace":"abc"},
		"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"hmm","signature":"sig"}]}]
	}`)
	for _, want := range []string{"top_k", "thinking", "metadata", "messages[0].content[0].thinking"} {
		if !hasDrop(drops, want) {
			t.Errorf("expected drop %q, got %+v", want, drops)
		}
	}
}

func TestTranslateRequest_MetadataUserIDMaps(t *testing.T) {
	req, _ := decodeChat(t, `{"model":"m","max_tokens":1,"metadata":{"user_id":"u1"},"messages":[{"role":"user","content":"x"}]}`)
	if req.User != "u1" {
		t.Errorf("User = %q, want u1", req.User)
	}
}

func TestTranslateRequest_ReasoningEffort(t *testing.T) {
	req, _ := decodeChat(t, `{"model":"m","max_tokens":1,"output_config":{"effort":"high"},"messages":[{"role":"user","content":"x"}]}`)
	if req.ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort = %q, want high", req.ReasoningEffort)
	}
}

func TestTranslateRequest_VendorUnknownNotCarried(t *testing.T) {
	// Anthropic-specific unknown top-level fields are intentionally NOT carried
	// onto the OpenAI request — they are meaningless to OpenAI. Documented
	// behaviour, asserted so it stays intentional.
	out, _, err := translateMessagesRequestToChat([]byte(`{"model":"m","max_tokens":1,"anthropic_only_knob":true,"messages":[{"role":"user","content":"x"}]}`))
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if strings.Contains(string(out), "anthropic_only_knob") {
		t.Errorf("vendor-specific unknown leaked into OpenAI request: %s", out)
	}
}

func TestTranslateRequest_InvalidBody(t *testing.T) {
	if _, _, err := translateMessagesRequestToChat([]byte(`{not json`)); err == nil {
		t.Error("expected error on invalid JSON body")
	}
}

func TestTranslateRequest_ToolResultBlockContent(t *testing.T) {
	req, drops := decodeChat(t, `{
		"model":"m","max_tokens":1,
		"messages":[{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"t1","content":[
				{"type":"text","text":"part one"},
				{"type":"image","source":{"type":"url","url":"https://x/y.png"}},
				{"type":"document","title":"unmodelled"}
			]}
		]}]
	}`)
	tm, ok := req.Messages[0].(*chat.ToolMessage)
	if !ok {
		t.Fatalf("messages[0] = %T, want ToolMessage", req.Messages[0])
	}
	parts, err := tm.Content.AsParts()
	if err != nil {
		t.Fatalf("tool message parts: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2 (text + image; document dropped)", len(parts))
	}
	if _, ok := parts[0].(*chat.TextContentPart); !ok {
		t.Errorf("parts[0] = %T, want TextContentPart", parts[0])
	}
	if _, ok := parts[1].(*chat.ImageURLContentPart); !ok {
		t.Errorf("parts[1] = %T, want ImageURLContentPart", parts[1])
	}
	if !hasDrop(drops, "messages[0].content[0].tool_result[2].document") {
		t.Errorf("expected tool_result document drop, got %+v", drops)
	}
}

func TestTranslateRequest_ToolResultEmptyContent(t *testing.T) {
	req, _ := decodeChat(t, `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1"}]}]}`)
	tm, ok := req.Messages[0].(*chat.ToolMessage)
	if !ok {
		t.Fatalf("messages[0] = %T, want ToolMessage", req.Messages[0])
	}
	if s, err := tm.Content.AsString(); err != nil || s != "" {
		t.Errorf("tool content = %q (err %v), want empty string", s, err)
	}
}

func TestTranslateRequest_UnknownBlocksDropped(t *testing.T) {
	_, drops := decodeChat(t, `{
		"model":"m","max_tokens":1,
		"messages":[
			{"role":"assistant","content":[{"type":"server_tool_use","id":"s1","name":"web"}]},
			{"role":"user","content":[{"type":"mystery","foo":"bar"}]}
		]
	}`)
	if !hasDrop(drops, "messages[0].content[0].server_tool_use") {
		t.Errorf("expected assistant unknown-block drop, got %+v", drops)
	}
	if !hasDrop(drops, "messages[1].content[0].mystery") {
		t.Errorf("expected user unknown-block drop, got %+v", drops)
	}
}

func TestTranslateRequest_ToolChoiceUnknownOmitted(t *testing.T) {
	req, _ := decodeChat(t, `{"model":"m","max_tokens":1,"tool_choice":{"type":"weird_future_policy"},"messages":[{"role":"user","content":"x"}]}`)
	if len(req.ToolChoice) != 0 {
		t.Errorf("tool_choice = %s, want omitted for unknown policy", req.ToolChoice)
	}
}

func TestTranslateRequest_ContentWrongType(t *testing.T) {
	// content that is neither a string nor a block array is a hard error, not a
	// silent drop.
	if _, _, err := translateMessagesRequestToChat([]byte(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":123}]}`)); err == nil {
		t.Error("expected error for numeric message content")
	}
}

func TestTranslateRequest_AssistantStringContent(t *testing.T) {
	req, _ := decodeChat(t, `{"model":"m","max_tokens":1,"messages":[{"role":"assistant","content":"prior reply"}]}`)
	asst, ok := req.Messages[0].(*chat.AssistantMessage)
	if !ok {
		t.Fatalf("messages[0] = %T, want AssistantMessage", req.Messages[0])
	}
	if s, _ := asst.Content.AsString(); s != "prior reply" {
		t.Errorf("assistant content = %q, want 'prior reply'", s)
	}
}

func TestTranslateRequest_AssistantOnlyToolUseNoText(t *testing.T) {
	// An assistant turn that is purely a tool_use has no text content — the
	// AssistantMessage must carry tool_calls and a null/absent content, not an
	// empty string.
	req, _ := decodeChat(t, `{"model":"m","max_tokens":1,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"f","input":{}}]}]}`)
	asst, ok := req.Messages[0].(*chat.AssistantMessage)
	if !ok {
		t.Fatalf("messages[0] = %T, want AssistantMessage", req.Messages[0])
	}
	if !asst.Content.IsNull() {
		t.Errorf("assistant content = %s, want null (no text blocks)", asst.Content.Raw())
	}
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].Function.Arguments != "{}" {
		t.Errorf("tool_calls = %+v, want one call with empty-object args", asst.ToolCalls)
	}
}

func TestTranslateRequest_ToolResultMalformedContent(t *testing.T) {
	// tool_result content that is neither a string nor a decodable block array
	// is a hard error.
	if _, _, err := translateMessagesRequestToChat([]byte(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":123}]}]}`)); err == nil {
		t.Error("expected error for malformed tool_result content")
	}
}
