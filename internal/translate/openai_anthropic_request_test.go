package translate

import (
	"encoding/json"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/protocols/anthropic/messages"
)

// decodeMessages translates an OpenAI Chat request body and decodes the
// resulting Anthropic Messages request for assertions.
func decodeMessagesReq(t *testing.T, body string) (messages.MessagesRequest, []Drop) {
	t.Helper()
	out, drops, err := translateChatRequestToMessages([]byte(body))
	if err != nil {
		t.Fatalf("translateChatRequestToMessages: %v", err)
	}
	var req messages.MessagesRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("decode translated request: %v\nbody: %s", err, out)
	}
	return req, drops
}

func TestChatToMessages_TextAndScalars(t *testing.T) {
	req, _ := decodeMessagesReq(t, `{
		"model":"gpt-4o",
		"max_tokens":256,
		"temperature":0.5,
		"top_p":0.9,
		"stream":true,
		"service_tier":"flex",
		"reasoning_effort":"high",
		"user":"u-123",
		"stop":["STOP","HALT"],
		"messages":[
			{"role":"system","content":"be terse"},
			{"role":"user","content":"hello"}
		]
	}`)

	if req.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o (verbatim)", req.Model)
	}
	if req.MaxTokens != 256 {
		t.Errorf("MaxTokens = %d, want 256", req.MaxTokens)
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
	if req.ServiceTier != "flex" {
		t.Errorf("ServiceTier = %q, want flex", req.ServiceTier)
	}
	if req.OutputConfig == nil || req.OutputConfig.Effort != "high" {
		t.Errorf("OutputConfig = %+v, want effort=high", req.OutputConfig)
	}
	if req.Metadata == nil || req.Metadata.UserID != "u-123" {
		t.Errorf("Metadata = %+v, want user_id=u-123", req.Metadata)
	}
	if len(req.StopSequences) != 2 || req.StopSequences[0] != "STOP" || req.StopSequences[1] != "HALT" {
		t.Errorf("StopSequences = %v, want [STOP HALT]", req.StopSequences)
	}
	sys, ok := req.SystemAsString()
	if !ok || sys != "be terse" {
		t.Errorf("System = %q (ok=%v), want 'be terse'", sys, ok)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Fatalf("messages = %+v, want one user message", req.Messages)
	}
	blocks, ok := req.Messages[0].ContentAsBlocks()
	if !ok || len(blocks) != 1 {
		t.Fatalf("user content blocks = %+v (ok=%v), want one block", blocks, ok)
	}
	if tb, ok := blocks[0].(*messages.TextBlock); !ok || tb.Text != "hello" {
		t.Errorf("user content[0] = %+v, want text 'hello'", blocks[0])
	}
}

func TestChatToMessages_MaxTokensPrecedence(t *testing.T) {
	// max_completion_tokens wins over max_tokens.
	req, _ := decodeMessagesReq(t, `{"model":"m","max_tokens":10,"max_completion_tokens":99,"messages":[{"role":"user","content":"hi"}]}`)
	if req.MaxTokens != 99 {
		t.Errorf("MaxTokens = %d, want 99 (max_completion_tokens preferred)", req.MaxTokens)
	}

	// Neither present -> synthesised default (Anthropic requires it).
	req, _ = decodeMessagesReq(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if req.MaxTokens != defaultAnthropicMaxTokens {
		t.Errorf("MaxTokens = %d, want default %d", req.MaxTokens, defaultAnthropicMaxTokens)
	}
}

func TestChatToMessages_StopAsString(t *testing.T) {
	req, _ := decodeMessagesReq(t, `{"model":"m","max_tokens":8,"stop":"END","messages":[{"role":"user","content":"hi"}]}`)
	if len(req.StopSequences) != 1 || req.StopSequences[0] != "END" {
		t.Errorf("StopSequences = %v, want [END]", req.StopSequences)
	}
}

func TestChatToMessages_DeveloperFoldsToSystem(t *testing.T) {
	req, _ := decodeMessagesReq(t, `{
		"model":"m","max_tokens":8,
		"messages":[
			{"role":"system","content":"first"},
			{"role":"developer","content":"second"},
			{"role":"user","content":"hi"}
		]
	}`)
	sys, ok := req.SystemAsString()
	if !ok || sys != "first\n\nsecond" {
		t.Errorf("System = %q, want 'first\\n\\nsecond'", sys)
	}
}

func TestChatToMessages_DroppedScalars(t *testing.T) {
	_, drops := decodeMessagesReq(t, `{
		"model":"m","max_tokens":8,
		"n":2,
		"stream_options":{"include_usage":true},
		"presence_penalty":0.1,
		"frequency_penalty":0.2,
		"logit_bias":{"50256":-100},
		"logprobs":true,
		"top_logprobs":3,
		"response_format":{"type":"json_object"},
		"seed":42,
		"modalities":["text","audio"],
		"audio":{"voice":"alloy","format":"wav"},
		"metadata":{"k":"v"},
		"store":true,
		"messages":[{"role":"user","content":"hi"}]
	}`)

	for _, f := range []string{
		"n", "stream_options", "presence_penalty", "frequency_penalty",
		"logit_bias", "logprobs", "top_logprobs", "response_format", "seed",
		"modalities", "audio", "metadata", "store",
	} {
		if !hasDrop(drops, f) {
			t.Errorf("missing expected drop %q; drops=%+v", f, drops)
		}
	}
}

func TestChatToMessages_Tools(t *testing.T) {
	req, drops := decodeMessagesReq(t, `{
		"model":"m","max_tokens":8,
		"tools":[
			{"type":"function","function":{"name":"get_weather","description":"look up","parameters":{"type":"object"},"strict":true}},
			{"type":"retrieval"}
		],
		"messages":[{"role":"user","content":"hi"}]
	}`)
	if len(req.Tools) != 1 {
		t.Fatalf("tools = %d, want 1 (non-function dropped)", len(req.Tools))
	}
	if req.Tools[0].Name != "get_weather" || req.Tools[0].Description != "look up" {
		t.Errorf("tool[0] = %+v, want get_weather", req.Tools[0])
	}
	if len(req.Tools[0].InputSchema) == 0 {
		t.Error("tool[0].InputSchema empty, want parameters carried")
	}
	if !hasDrop(drops, "tools[0].function.strict") {
		t.Errorf("missing strict drop; drops=%+v", drops)
	}
	if !hasDrop(drops, "tools[1]") {
		t.Errorf("missing non-function tool drop; drops=%+v", drops)
	}
}

func TestChatToMessages_ToolChoiceVariants(t *testing.T) {
	cases := []struct {
		name     string
		choice   string
		wantType string
		wantName string
		wantDrop bool
	}{
		{"auto", `"auto"`, "auto", "", false},
		{"none", `"none"`, "none", "", false},
		{"required", `"required"`, "any", "", false},
		{"named", `{"type":"function","function":{"name":"f"}}`, "tool", "f", false},
		{"unknown_string", `"weird"`, "", "", true},
		{"bad_object", `{"type":"other"}`, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, drops := decodeMessagesReq(t, `{"model":"m","max_tokens":8,"tool_choice":`+tc.choice+`,"messages":[{"role":"user","content":"hi"}]}`)
			if tc.wantType == "" {
				if req.ToolChoice != nil {
					t.Errorf("ToolChoice = %+v, want nil", req.ToolChoice)
				}
			} else {
				if req.ToolChoice == nil || req.ToolChoice.Type != tc.wantType || req.ToolChoice.Name != tc.wantName {
					t.Errorf("ToolChoice = %+v, want type=%q name=%q", req.ToolChoice, tc.wantType, tc.wantName)
				}
			}
			if tc.wantDrop && !hasDrop(drops, "tool_choice") {
				t.Errorf("missing tool_choice drop; drops=%+v", drops)
			}
		})
	}
}

func TestChatToMessages_ParallelToolCalls(t *testing.T) {
	// parallel_tool_calls=false with no tool_choice synthesises an auto choice.
	req, _ := decodeMessagesReq(t, `{"model":"m","max_tokens":8,"parallel_tool_calls":false,"messages":[{"role":"user","content":"hi"}]}`)
	if req.ToolChoice == nil || req.ToolChoice.Type != "auto" {
		t.Fatalf("ToolChoice = %+v, want auto", req.ToolChoice)
	}
	if req.ToolChoice.DisableParallelToolUse == nil || !*req.ToolChoice.DisableParallelToolUse {
		t.Errorf("DisableParallelToolUse = %v, want true", req.ToolChoice.DisableParallelToolUse)
	}

	// parallel_tool_calls=true folds onto an existing tool_choice.
	req, _ = decodeMessagesReq(t, `{"model":"m","max_tokens":8,"tool_choice":"required","parallel_tool_calls":true,"messages":[{"role":"user","content":"hi"}]}`)
	if req.ToolChoice == nil || req.ToolChoice.Type != "any" {
		t.Fatalf("ToolChoice = %+v, want any", req.ToolChoice)
	}
	if req.ToolChoice.DisableParallelToolUse == nil || *req.ToolChoice.DisableParallelToolUse {
		t.Errorf("DisableParallelToolUse = %v, want false", req.ToolChoice.DisableParallelToolUse)
	}
}

func TestChatToMessages_UserContentParts(t *testing.T) {
	req, drops := decodeMessagesReq(t, `{
		"model":"m","max_tokens":8,
		"messages":[{"role":"user","content":[
			{"type":"text","text":"see this"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,QUJD"}},
			{"type":"image_url","image_url":{"url":"https://example.com/x.png"}},
			{"type":"input_audio","input_audio":{"data":"AA","format":"wav"}}
		]}]
	}`)
	if len(req.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(req.Messages))
	}
	blocks, ok := req.Messages[0].ContentAsBlocks()
	if !ok {
		t.Fatalf("user content is not blocks: %s", req.Messages[0].Content)
	}
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want 3 (audio dropped)", len(blocks))
	}
	if tb, ok := blocks[0].(*messages.TextBlock); !ok || tb.Text != "see this" {
		t.Errorf("blocks[0] = %+v, want text 'see this'", blocks[0])
	}
	img, ok := blocks[1].(*messages.ImageBlock)
	if !ok || img.Source.Type != "base64" || img.Source.MediaType != "image/png" || img.Source.Data != "QUJD" {
		t.Errorf("blocks[1] = %+v, want base64 image/png QUJD", blocks[1])
	}
	urlImg, ok := blocks[2].(*messages.ImageBlock)
	if !ok || urlImg.Source.Type != "url" || urlImg.Source.URL != "https://example.com/x.png" {
		t.Errorf("blocks[2] = %+v, want url image", blocks[2])
	}
	if !hasDrop(drops, "messages[0].content[3].input_audio") {
		t.Errorf("missing input_audio drop; drops=%+v", drops)
	}
}

func TestChatToMessages_AssistantWithToolCalls(t *testing.T) {
	req, _ := decodeMessagesReq(t, `{
		"model":"m","max_tokens":8,
		"messages":[
			{"role":"user","content":"weather?"},
			{"role":"assistant","content":"let me check","tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}},
				{"id":"call_2","type":"function","function":{"name":"noargs","arguments":""}}
			]}
		]
	}`)
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(req.Messages))
	}
	asst := req.Messages[1]
	if asst.Role != "assistant" {
		t.Fatalf("messages[1] role = %q, want assistant", asst.Role)
	}
	blocks, ok := asst.ContentAsBlocks()
	if !ok {
		t.Fatalf("assistant content not blocks: %s", asst.Content)
	}
	if len(blocks) != 3 {
		t.Fatalf("assistant blocks = %d, want 3 (text + 2 tool_use)", len(blocks))
	}
	if tb, ok := blocks[0].(*messages.TextBlock); !ok || tb.Text != "let me check" {
		t.Errorf("blocks[0] = %+v, want text", blocks[0])
	}
	tu, ok := blocks[1].(*messages.ToolUseBlock)
	if !ok || tu.ID != "call_1" || tu.Name != "get_weather" {
		t.Fatalf("blocks[1] = %+v, want tool_use call_1", blocks[1])
	}
	var in map[string]string
	if err := json.Unmarshal(tu.Input, &in); err != nil || in["city"] != "SF" {
		t.Errorf("tool_use input = %s (err %v), want {city:SF}", tu.Input, err)
	}
	tu2 := blocks[2].(*messages.ToolUseBlock)
	if string(tu2.Input) != "{}" {
		t.Errorf("empty-args tool_use input = %s, want {}", tu2.Input)
	}
}

func TestChatToMessages_ToolResultsFoldIntoUserTurn(t *testing.T) {
	// assistant(tool_calls) -> tool, tool -> one Anthropic user turn of
	// tool_result blocks; the inverse of the forward split.
	req, _ := decodeMessagesReq(t, `{
		"model":"m","max_tokens":8,
		"messages":[
			{"role":"user","content":"q"},
			{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"c1","content":"42"},
			{"role":"tool","tool_call_id":"c2","content":[{"type":"text","text":"more"}]}
		]
	}`)
	if len(req.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (user, assistant, tool-result user)", len(req.Messages))
	}
	last := req.Messages[2]
	if last.Role != "user" {
		t.Fatalf("messages[2] role = %q, want user", last.Role)
	}
	blocks, ok := last.ContentAsBlocks()
	if !ok || len(blocks) != 2 {
		t.Fatalf("tool-result turn blocks = %+v, want 2 tool_result", blocks)
	}
	tr0, ok := blocks[0].(*messages.ToolResultBlock)
	if !ok || tr0.ToolUseID != "c1" {
		t.Fatalf("blocks[0] = %+v, want tool_result c1", blocks[0])
	}
	var s string
	if err := json.Unmarshal(tr0.Content, &s); err != nil || s != "42" {
		t.Errorf("tool_result[0] content = %s (err %v), want \"42\"", tr0.Content, err)
	}
	tr1 := blocks[1].(*messages.ToolResultBlock)
	if tr1.ToolUseID != "c2" {
		t.Errorf("tool_result[1] tool_use_id = %q, want c2", tr1.ToolUseID)
	}
	inner, err := messages.UnmarshalContentBlocks(tr1.Content)
	if err != nil || len(inner) != 1 {
		t.Fatalf("tool_result[1] inner = %+v (err %v), want 1 block", inner, err)
	}
	if tb, ok := inner[0].(*messages.TextBlock); !ok || tb.Text != "more" {
		t.Errorf("tool_result[1] inner[0] = %+v, want text 'more'", inner[0])
	}
}

func TestChatToMessages_EmptyToolContent(t *testing.T) {
	req, _ := decodeMessagesReq(t, `{
		"model":"m","max_tokens":8,
		"messages":[{"role":"tool","tool_call_id":"c1"}]
	}`)
	blocks, _ := req.Messages[0].ContentAsBlocks()
	tr := blocks[0].(*messages.ToolResultBlock)
	if len(tr.Content) != 0 {
		t.Errorf("empty tool content should yield no content, got %s", tr.Content)
	}
}

func TestChatToMessages_AssistantRefusalAndAudio(t *testing.T) {
	req, drops := decodeMessagesReq(t, `{
		"model":"m","max_tokens":8,
		"messages":[{"role":"assistant","refusal":"cannot help","audio":{"id":"a1"}}]
	}`)
	blocks, _ := req.Messages[0].ContentAsBlocks()
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1 (refusal->text, audio dropped)", len(blocks))
	}
	if tb, ok := blocks[0].(*messages.TextBlock); !ok || tb.Text != "cannot help" {
		t.Errorf("blocks[0] = %+v, want text 'cannot help'", blocks[0])
	}
	if !hasDrop(drops, "messages[0].refusal") || !hasDrop(drops, "messages[0].audio") {
		t.Errorf("missing refusal/audio drops; drops=%+v", drops)
	}
}

func TestChatToMessages_AssistantContentParts(t *testing.T) {
	req, drops := decodeMessagesReq(t, `{
		"model":"m","max_tokens":8,
		"messages":[{"role":"assistant","content":[
			{"type":"text","text":"hi"},
			{"type":"refusal","refusal":"no"},
			{"type":"image_url","image_url":{"url":"https://x"}}
		]}]
	}`)
	blocks, _ := req.Messages[0].ContentAsBlocks()
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2 (text + refusal->text, image dropped)", len(blocks))
	}
	if !hasDrop(drops, "messages[0].content[1].refusal") {
		t.Errorf("missing refusal-part drop; drops=%+v", drops)
	}
	if !hasDrop(drops, "messages[0].content[2].image_url") {
		t.Errorf("missing assistant image_url drop; drops=%+v", drops)
	}
}

func TestChatToMessages_FunctionAndUnknownRoles(t *testing.T) {
	_, drops := decodeMessagesReq(t, `{
		"model":"m","max_tokens":8,
		"messages":[
			{"role":"function","name":"legacy","content":"x"},
			{"role":"martian","content":"x"}
		]
	}`)
	if !hasDrop(drops, "messages[0].function") {
		t.Errorf("missing function-role drop; drops=%+v", drops)
	}
	if !hasDrop(drops, "messages[1].martian") {
		t.Errorf("missing unknown-role drop; drops=%+v", drops)
	}
}

func TestChatToMessages_BadRequestBody(t *testing.T) {
	if _, _, err := translateChatRequestToMessages([]byte(`{not json`)); err == nil {
		t.Error("expected decode error for malformed body")
	}
}

func TestChatToMessages_BadStop(t *testing.T) {
	// A stop value that is neither a string nor a string array is a hard error.
	if _, _, err := translateChatRequestToMessages([]byte(`{"model":"m","max_tokens":8,"stop":123,"messages":[{"role":"user","content":"hi"}]}`)); err == nil {
		t.Error("expected error for non-string/array stop")
	}
}

func TestChatToMessages_MalformedContentArrays(t *testing.T) {
	// A content array that is not content-part objects is a hard error, in each
	// of the three roles that decode content parts.
	cases := map[string]string{
		"user":      `{"model":"m","max_tokens":8,"messages":[{"role":"user","content":[1,2,3]}]}`,
		"assistant": `{"model":"m","max_tokens":8,"messages":[{"role":"assistant","content":[1,2,3]}]}`,
		"tool":      `{"model":"m","max_tokens":8,"messages":[{"role":"tool","tool_call_id":"c","content":[1,2,3]}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := translateChatRequestToMessages([]byte(body)); err == nil {
				t.Errorf("%s: expected error for malformed content parts array", name)
			}
		})
	}
}

func TestChatToMessages_InvalidToolArguments(t *testing.T) {
	// Tool-call arguments that are not valid JSON cannot become a tool_use input
	// object, so encoding the assistant turn is a hard error.
	body := `{"model":"m","max_tokens":8,"messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{bad json"}}]}]}`
	if _, _, err := translateChatRequestToMessages([]byte(body)); err == nil {
		t.Error("expected error for invalid tool-call arguments")
	}
}

func TestChatToMessages_ToolResultUnsupportedPart(t *testing.T) {
	// A tool result whose nested content carries an unmappable part records a
	// drop labelled under the tool_result path.
	req, drops := decodeMessagesReq(t, `{
		"model":"m","max_tokens":8,
		"messages":[{"role":"tool","tool_call_id":"c1","content":[
			{"type":"text","text":"ok"},
			{"type":"input_audio","input_audio":{"data":"x","format":"wav"}}
		]}]
	}`)
	blocks, _ := req.Messages[0].ContentAsBlocks()
	tr := blocks[0].(*messages.ToolResultBlock)
	inner, _ := messages.UnmarshalContentBlocks(tr.Content)
	if len(inner) != 1 {
		t.Fatalf("tool_result inner blocks = %d, want 1 (audio dropped)", len(inner))
	}
	if !hasDrop(drops, "messages[0].tool_result.content[1].input_audio") {
		t.Errorf("missing tool_result audio drop; drops=%+v", drops)
	}
}
