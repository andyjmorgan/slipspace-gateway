package messages

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMessagesRequest_StringContentRoundTrip(t *testing.T) {
	in := []byte(`{"max_tokens":1024,"messages":[{"content":"hello","role":"user"}],"model":"claude-sonnet-4-5"}`)
	var req MessagesRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Model != "claude-sonnet-4-5" || req.MaxTokens != 1024 || len(req.Messages) != 1 {
		t.Fatalf("typed fields wrong: %+v", req)
	}
	if got, ok := req.Messages[0].ContentAsString(); !ok || got != "hello" {
		t.Fatalf("ContentAsString = (%q, %v)", got, ok)
	}
	if _, ok := req.Messages[0].ContentAsBlocks(); ok {
		t.Fatalf("ContentAsBlocks should be false for string content")
	}
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func TestMessagesRequest_ArrayContentRoundTripWithUnknownBlock(t *testing.T) {
	in := []byte(`{
		"max_tokens": 4096,
		"messages": [{
			"content": [
				{"type":"text","text":"What is this?"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAA"}},
				{"type":"weather_widget","lat":40.7,"lon":-74}
			],
			"role": "user"
		}],
		"metadata": {"future_field":"keep","user_id":"u123"},
		"model": "claude-sonnet-4-5",
		"stream": true,
		"temperature": 0.7,
		"top_k": 40,
		"top_p": 0.9,
		"stop_sequences": ["STOP"],
		"system": "be helpful",
		"thinking": {"budget_tokens":1000,"type":"enabled"},
		"tools": [{
			"name": "search",
			"description": "Search the web",
			"input_schema": {"type":"object","properties":{"q":{"type":"string"}}},
			"cache_control": {"type":"ephemeral"}
		}],
		"tool_choice": {"disable_parallel_tool_use":false,"type":"auto"},
		"service_tier": "auto"
	}`)
	var req MessagesRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if req.Model != "claude-sonnet-4-5" {
		t.Fatalf("model = %q", req.Model)
	}
	if !req.Stream {
		t.Fatalf("stream not set")
	}
	if req.Temperature == nil || *req.Temperature != 0.7 {
		t.Fatalf("temperature = %v", req.Temperature)
	}
	if sys, ok := req.SystemAsString(); !ok || sys != "be helpful" {
		t.Fatalf("system = (%q, %v)", sys, ok)
	}
	blocks, ok := req.Messages[0].ContentAsBlocks()
	if !ok {
		t.Fatalf("expected array content")
	}
	if len(blocks) != 3 {
		t.Fatalf("block count = %d", len(blocks))
	}
	if _, ok := blocks[0].(*TextBlock); !ok {
		t.Fatalf("block 0 = %T", blocks[0])
	}
	if _, ok := blocks[1].(*ImageBlock); !ok {
		t.Fatalf("block 1 = %T", blocks[1])
	}
	if u, ok := blocks[2].(*UnknownBlock); !ok || u.Type != "weather_widget" {
		t.Fatalf("block 2 = %T (%+v)", blocks[2], blocks[2])
	}
	if req.Metadata == nil || req.Metadata.UserID != "u123" {
		t.Fatalf("metadata = %+v", req.Metadata)
	}
	if string(req.Metadata.Extra["future_field"]) != `"keep"` {
		t.Fatalf("metadata extras: %v", req.Metadata.Extra)
	}

	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func TestMessagesRequest_ArraySystemRoundTrip(t *testing.T) {
	in := []byte(`{
		"max_tokens": 256,
		"messages": [{"content":"hi","role":"user"}],
		"model": "claude-sonnet-4-5",
		"system": [
			{"type":"text","text":"be terse","cache_control":{"type":"ephemeral"}}
		]
	}`)
	var req MessagesRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := req.SystemAsString(); ok {
		t.Fatalf("SystemAsString should be false for array system")
	}
	blocks, ok := req.SystemAsBlocks()
	if !ok || len(blocks) != 1 || blocks[0].Text != "be terse" {
		t.Fatalf("SystemAsBlocks = (%+v, %v)", blocks, ok)
	}
	if blocks[0].CacheControl == nil || blocks[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("cache_control = %+v", blocks[0].CacheControl)
	}

	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func TestMessagesRequest_UnknownTopLevelFieldsRoundTrip(t *testing.T) {
	in := []byte(`{"future_top_level":"keep","max_tokens":16,"messages":[{"content":"hi","role":"user"}],"model":"claude-sonnet-4-5"}`)
	var req MessagesRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(req.Extra["future_top_level"]) != `"keep"` {
		t.Fatalf("extras: %v", req.Extra)
	}
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func TestMessagesRequest_SetSystemAndContentHelpers(t *testing.T) {
	var req MessagesRequest
	req.Model = "claude-sonnet-4-5"
	req.MaxTokens = 32
	msg := Message{Role: "user"}
	if err := msg.SetContentString("ping"); err != nil {
		t.Fatalf("SetContentString: %v", err)
	}
	req.Messages = []Message{msg}
	if err := req.SetSystemString("be helpful"); err != nil {
		t.Fatalf("SetSystemString: %v", err)
	}
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := []byte(`{"max_tokens":32,"messages":[{"content":"ping","role":"user"}],"model":"claude-sonnet-4-5","system":"be helpful"}`)
	if !jsonValueEqual(t, out, want) {
		t.Fatalf("got %s", out)
	}

	if err := req.SetSystemBlocks([]SystemBlock{{Type: "text", Text: "x"}}); err != nil {
		t.Fatalf("SetSystemBlocks: %v", err)
	}
	if blocks, ok := req.SystemAsBlocks(); !ok || len(blocks) != 1 || blocks[0].Text != "x" {
		t.Fatalf("SystemAsBlocks after set = (%+v, %v)", blocks, ok)
	}

	if err := req.Messages[0].SetContentBlocks([]ContentBlock{&TextBlock{Type: "text", Text: "y"}}); err != nil {
		t.Fatalf("SetContentBlocks: %v", err)
	}
	if blocks, ok := req.Messages[0].ContentAsBlocks(); !ok || len(blocks) != 1 {
		t.Fatalf("ContentAsBlocks after set = (%+v, %v)", blocks, ok)
	}
}

func TestMessagesRequest_ContentEmptyAccessors(t *testing.T) {
	var m Message
	if _, ok := m.ContentAsString(); ok {
		t.Fatalf("empty ContentAsString should be false")
	}
	if _, ok := m.ContentAsBlocks(); ok {
		t.Fatalf("empty ContentAsBlocks should be false")
	}
}

func TestMessagesRequest_SystemEmptyAccessors(t *testing.T) {
	var r MessagesRequest
	if _, ok := r.SystemAsString(); ok {
		t.Fatalf("empty SystemAsString should be false")
	}
	if _, ok := r.SystemAsBlocks(); ok {
		t.Fatalf("empty SystemAsBlocks should be false")
	}
}

func TestMessagesRequest_SystemAndContentAccessorsMalformed(t *testing.T) {
	r := MessagesRequest{System: json.RawMessage(`<<<not json>>>`)}
	if _, ok := r.SystemAsString(); ok {
		t.Fatalf("SystemAsString should be false for malformed JSON")
	}
	if _, ok := r.SystemAsBlocks(); ok {
		t.Fatalf("SystemAsBlocks should be false for malformed JSON")
	}

	m := Message{Content: json.RawMessage(`<<<not json>>>`)}
	if _, ok := m.ContentAsString(); ok {
		t.Fatalf("ContentAsString should be false for malformed JSON")
	}

	m2 := Message{Content: json.RawMessage(`[{"text":"missing type"}]`)}
	if _, ok := m2.ContentAsBlocks(); ok {
		t.Fatalf("ContentAsBlocks should be false when block dispatch fails")
	}
}

func TestMessagesResponse_RoundTripWithToolUse(t *testing.T) {
	in := []byte(`{
		"id": "msg_01",
		"type": "message",
		"role": "assistant",
		"model": "claude-sonnet-4-5",
		"content": [
			{"type":"text","text":"thinking..."},
			{"type":"server_tool_use","id":"srvtoolu_01","name":"web_search","input":{"query":"weather"}},
			{"type":"web_search_tool_result","tool_use_id":"srvtoolu_01","content":[{"type":"web_search_result","title":"W","url":"https://e.com","encrypted_content":"z"}]},
			{"type":"tool_use","id":"toolu_01","name":"search","input":{"q":"weather"}},
			{"type":"future_block","payload":"keep"}
		],
		"stop_reason": "tool_use",
		"stop_sequence": null,
		"usage": {
			"cache_creation_input_tokens": 8,
			"cache_read_input_tokens": 0,
			"input_tokens": 10,
			"output_tokens": 25,
			"server_tool_use": {"web_search_requests": 1, "web_fetch_requests": 2},
			"service_tier": "standard"
		},
		"container": {"expires_at":"2026-05-15T00:00:00Z","id":"c1"},
		"future_top_level": {"x":1}
	}`)
	var resp MessagesResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID != "msg_01" || resp.Role != "assistant" {
		t.Fatalf("typed fields wrong: %+v", resp)
	}
	if len(resp.Content) != 5 {
		t.Fatalf("content len = %d", len(resp.Content))
	}
	if _, ok := resp.Content[0].(*TextBlock); !ok {
		t.Fatalf("content[0] = %T", resp.Content[0])
	}
	if stu, ok := resp.Content[1].(*ServerToolUseBlock); !ok || stu.Name != "web_search" {
		t.Fatalf("content[1] = %T %+v", resp.Content[1], resp.Content[1])
	}
	if res, ok := resp.Content[2].(*WebSearchToolResultBlock); !ok || res.ToolUseID != "srvtoolu_01" {
		t.Fatalf("content[2] = %T %+v", resp.Content[2], resp.Content[2])
	}
	if tu, ok := resp.Content[3].(*ToolUseBlock); !ok || tu.Name != "search" {
		t.Fatalf("content[3] = %T %+v", resp.Content[3], resp.Content[3])
	}
	if u, ok := resp.Content[4].(*UnknownBlock); !ok || u.Type != "future_block" {
		t.Fatalf("content[4] = %T %+v", resp.Content[4], resp.Content[4])
	}
	if resp.StopReason == nil || *resp.StopReason != "tool_use" {
		t.Fatalf("stop_reason = %v", resp.StopReason)
	}
	if resp.Usage.CacheCreationInputTokens == nil || *resp.Usage.CacheCreationInputTokens != 8 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if resp.Usage.ServerToolUse == nil || resp.Usage.ServerToolUse.WebSearchRequests == nil || *resp.Usage.ServerToolUse.WebSearchRequests != 1 {
		t.Fatalf("server_tool_use = %+v", resp.Usage.ServerToolUse)
	}
	if resp.Usage.ServerToolUse.WebFetchRequests == nil || *resp.Usage.ServerToolUse.WebFetchRequests != 2 {
		t.Fatalf("web_fetch_requests = %v", resp.Usage.ServerToolUse.WebFetchRequests)
	}
	if resp.Container == nil || resp.Container.ID != "c1" {
		t.Fatalf("container = %+v", resp.Container)
	}
	if string(resp.Extra["future_top_level"]) != `{"x":1}` {
		t.Fatalf("top-level extras = %v", resp.Extra)
	}

	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func TestMessagesResponse_NullContent(t *testing.T) {
	in := []byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":null,"usage":{"input_tokens":0,"output_tokens":0}}`)
	var resp MessagesResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Content != nil {
		t.Fatalf("content = %v", resp.Content)
	}
}

func TestMessagesResponse_InvalidContent(t *testing.T) {
	in := []byte(`{"id":"x","model":"m","role":"assistant","type":"message","content":[{"text":"missing type"}],"usage":{"input_tokens":0,"output_tokens":0}}`)
	var resp MessagesResponse
	if err := json.Unmarshal(in, &resp); err == nil {
		t.Fatalf("expected error on content block missing type")
	}
}

func TestMessagesResponse_NonObject(t *testing.T) {
	var resp MessagesResponse
	if err := json.Unmarshal([]byte(`"not an object"`), &resp); err == nil {
		t.Fatalf("expected error on non-object input")
	}
}

func TestTool_AndToolChoice_RoundTrip(t *testing.T) {
	in := []byte(`{"cache_control":{"type":"ephemeral"},"description":"d","input_schema":{"type":"object"},"name":"t"}`)
	var tool Tool
	if err := json.Unmarshal(in, &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("tool drift\n in: %s\nout: %s", in, out)
	}

	tcIn := []byte(`{"disable_parallel_tool_use":true,"name":"search","type":"tool"}`)
	var tc ToolChoice
	if err := json.Unmarshal(tcIn, &tc); err != nil {
		t.Fatalf("tc unmarshal: %v", err)
	}
	if tc.DisableParallelToolUse == nil || !*tc.DisableParallelToolUse {
		t.Fatalf("disable_parallel_tool_use = %v", tc.DisableParallelToolUse)
	}
	tcOut, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("tc marshal: %v", err)
	}
	if !jsonValueEqual(t, tcIn, tcOut) {
		t.Fatalf("tool_choice drift\n in: %s\nout: %s", tcIn, tcOut)
	}
}

// TestTool_DeferLoading_RoundTrip covers the tool-search request surface: a
// deferred custom tool decodes typed (not into Extra) and a tool-search
// server tool round-trips by its versioned type discriminator.
func TestTool_DeferLoading_RoundTrip(t *testing.T) {
	in := []byte(`{"defer_loading":true,"description":"Get the weather at a specific location","input_schema":{"properties":{"location":{"type":"string"}},"required":["location"],"type":"object"},"name":"get_weather"}`)
	var tool Tool
	if err := json.Unmarshal(in, &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tool.DeferLoading == nil || !*tool.DeferLoading {
		t.Fatalf("defer_loading not typed: %v (extra=%v)", tool.DeferLoading, tool.Extra)
	}
	if len(tool.Extra) != 0 {
		t.Fatalf("unmapped fields leaked: %v", tool.Extra)
	}
	out, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("tool drift\n in: %s\nout: %s", in, out)
	}

	searchIn := []byte(`{"name":"tool_search_tool_regex","type":"tool_search_tool_regex_20251119"}`)
	var search Tool
	if err := json.Unmarshal(searchIn, &search); err != nil {
		t.Fatalf("search unmarshal: %v", err)
	}
	if search.Type != "tool_search_tool_regex_20251119" {
		t.Fatalf("search type = %q", search.Type)
	}
	searchOut, err := json.Marshal(search)
	if err != nil {
		t.Fatalf("search marshal: %v", err)
	}
	if !jsonValueEqual(t, searchIn, searchOut) {
		t.Fatalf("search tool drift\n in: %s\nout: %s", searchIn, searchOut)
	}
}

func TestMessagesResponse_UsageDriftFieldsRoundTrip(t *testing.T) {
	in := []byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-4-7",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn","stop_sequence":null,
		"usage":{
			"input_tokens":10,
			"output_tokens":25,
			"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":4},
			"output_tokens_details":{"thinking_tokens":12},
			"inference_geo":"not_available",
			"service_tier":"standard",
			"iterations":[
				{"type":"message","model":"claude-opus-4-7","input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":0}},
				{"type":"compaction","input_tokens":5,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}
			]
		}
	}`)
	var resp MessagesResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Usage.CacheCreation == nil || resp.Usage.CacheCreation.Ephemeral5mInputTokens == nil ||
		*resp.Usage.CacheCreation.Ephemeral5mInputTokens != 4 {
		t.Fatalf("cache_creation = %+v", resp.Usage.CacheCreation)
	}
	if resp.Usage.OutputTokensDetails == nil || resp.Usage.OutputTokensDetails.ThinkingTokens == nil ||
		*resp.Usage.OutputTokensDetails.ThinkingTokens != 12 {
		t.Fatalf("output_tokens_details = %+v", resp.Usage.OutputTokensDetails)
	}
	if resp.Usage.InferenceGeo != "not_available" {
		t.Fatalf("inference_geo = %q", resp.Usage.InferenceGeo)
	}
	if len(resp.Usage.Iterations) != 2 {
		t.Fatalf("iterations len = %d, want 2", len(resp.Usage.Iterations))
	}
	if it := resp.Usage.Iterations[0]; it.Type != "message" || it.Model != "claude-opus-4-7" ||
		it.InputTokens != 10 || it.OutputTokens != 20 ||
		it.CacheCreationInputTokens == nil || *it.CacheCreationInputTokens != 2 ||
		it.CacheCreation == nil || it.CacheCreation.Ephemeral5mInputTokens == nil ||
		*it.CacheCreation.Ephemeral5mInputTokens != 2 {
		t.Fatalf("iterations[0] = %+v", it)
	}
	// Compaction iterations carry no model — Model must stay empty, not leak
	// into Extra.
	if it := resp.Usage.Iterations[1]; it.Type != "compaction" || it.Model != "" || it.InputTokens != 5 {
		t.Fatalf("iterations[1] = %+v", it)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

// TestMessagesResponse_CallerAndContextMgmtRoundTrip mirrors a live
// claude-sonnet-4-6 tool-use response: a tool_use block with caller
// attribution, a null stop_details, and the context-management applied_edits
// envelope. All decode typed and round-trip with nothing left in Extra.
func TestMessagesResponse_CallerAndContextMgmtRoundTrip(t *testing.T) {
	in := []byte(`{` +
		`"content":[{"caller":{"type":"direct"},"id":"toolu_1","input":{"city":"Dublin"},"name":"get_weather","type":"tool_use"}],` +
		`"context_management":{"applied_edits":[]},` +
		`"id":"msg_1",` +
		`"model":"claude-sonnet-4-6",` +
		`"role":"assistant",` +
		`"stop_details":null,` +
		`"stop_reason":"tool_use",` +
		`"stop_sequence":null,` +
		`"type":"message",` +
		`"usage":{"input_tokens":596,"output_tokens":97}` +
		`}`)
	var resp MessagesResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tu, ok := resp.Content[0].(*ToolUseBlock)
	if !ok {
		t.Fatalf("content[0] = %T", resp.Content[0])
	}
	if tu.Caller == nil || tu.Caller.Type != "direct" {
		t.Fatalf("caller = %+v", tu.Caller)
	}
	if resp.ContextManagement == nil || string(resp.ContextManagement.AppliedEdits) != "[]" {
		t.Fatalf("context_management = %+v", resp.ContextManagement)
	}
	if string(resp.StopDetails) != "null" {
		t.Fatalf("stop_details = %s (want null)", resp.StopDetails)
	}
	if len(resp.Extra) != 0 || len(tu.Extra) != 0 {
		t.Fatalf("unmapped fields leaked: resp=%v block=%v", resp.Extra, tu.Extra)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func TestMessagesRequest_OutputConfigRoundTrip(t *testing.T) {
	// Both effort and the GA structured-output "format" block are modelled;
	// neither should leak into DynamicProperties.Extra.
	in := []byte(`{"max_tokens":1024,"messages":[{"content":"hi","role":"user"}],"model":"claude-opus-4-7","output_config":{"effort":"xhigh","format":{"schema":{"type":"object"},"type":"json_schema"}}}`)
	var req MessagesRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.OutputConfig == nil || req.OutputConfig.Effort != "xhigh" {
		t.Fatalf("output_config = %+v", req.OutputConfig)
	}
	if req.OutputConfig.Format == nil || req.OutputConfig.Format.Type != "json_schema" {
		t.Fatalf("output_config.format = %+v", req.OutputConfig.Format)
	}
	if string(req.OutputConfig.Format.Schema) != `{"type":"object"}` {
		t.Fatalf("format.schema = %s", req.OutputConfig.Format.Schema)
	}
	if _, ok := req.OutputConfig.Extra["format"]; ok {
		t.Fatalf("format leaked into Extra instead of typed Format: %v", req.OutputConfig.Extra)
	}
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

// TestTool_ServerToolFieldsRoundTrip covers the Anthropic server-tool fields
// type (a versioned pre-built-tool discriminator) and max_uses (the
// per-request invocation cap) — both decode typed with nothing left in Extra.
func TestTool_ServerToolFieldsRoundTrip(t *testing.T) {
	in := []byte(`{"max_uses":5,"name":"web_search","type":"web_search_20250305"}`)
	var tool Tool
	if err := json.Unmarshal(in, &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tool.Type != "web_search_20250305" || tool.Name != "web_search" {
		t.Fatalf("tool = %+v", tool)
	}
	if tool.MaxUses == nil || *tool.MaxUses != 5 {
		t.Fatalf("max_uses = %v", tool.MaxUses)
	}
	if len(tool.Extra) != 0 {
		t.Fatalf("unmapped fields leaked: %v", tool.Extra)
	}
	out, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("tool drift\n in: %s\nout: %s", in, out)
	}
}

func TestMessages_AllExportedFieldsHaveJSONTag(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(MessagesRequest{}),
		reflect.TypeOf(Message{}),
		reflect.TypeOf(Tool{}),
		reflect.TypeOf(ToolChoice{}),
		reflect.TypeOf(Metadata{}),
		reflect.TypeOf(ThinkingConfig{}),
		reflect.TypeOf(SystemBlock{}),
		reflect.TypeOf(CacheControl{}),
		reflect.TypeOf(OutputConfig{}),
		reflect.TypeOf(OutputFormat{}),
		reflect.TypeOf(MessagesResponse{}),
		reflect.TypeOf(Usage{}),
		reflect.TypeOf(ServerToolUseUsage{}),
		reflect.TypeOf(IterationUsage{}),
		reflect.TypeOf(CacheCreation{}),
		reflect.TypeOf(OutputTokensDetails{}),
		reflect.TypeOf(Container{}),
		reflect.TypeOf(ContextManagement{}),
		reflect.TypeOf(ErrorResponse{}),
		reflect.TypeOf(ContextManagementConfig{}),
		reflect.TypeOf(ClearToolUsesEdit{}),
		reflect.TypeOf(ClearThinkingEdit{}),
		reflect.TypeOf(CompactEdit{}),
		reflect.TypeOf(UnknownContextEdit{}),
	}
	for _, rt := range types {
		t.Run(rt.Name(), func(t *testing.T) {
			for i := 0; i < rt.NumField(); i++ {
				sf := rt.Field(i)
				if sf.Anonymous || !sf.IsExported() {
					continue
				}
				if _, ok := sf.Tag.Lookup("json"); !ok {
					t.Errorf("%s.%s missing json tag", rt.Name(), sf.Name)
				}
			}
		})
	}
}
