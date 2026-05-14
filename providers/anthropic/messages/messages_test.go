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
			"server_tool_use": {"web_search_requests": 1},
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
	if len(resp.Content) != 3 {
		t.Fatalf("content len = %d", len(resp.Content))
	}
	if _, ok := resp.Content[0].(*TextBlock); !ok {
		t.Fatalf("content[0] = %T", resp.Content[0])
	}
	if tu, ok := resp.Content[1].(*ToolUseBlock); !ok || tu.Name != "search" {
		t.Fatalf("content[1] = %T %+v", resp.Content[1], resp.Content[1])
	}
	if u, ok := resp.Content[2].(*UnknownBlock); !ok || u.Type != "future_block" {
		t.Fatalf("content[2] = %T %+v", resp.Content[2], resp.Content[2])
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
		reflect.TypeOf(MessagesResponse{}),
		reflect.TypeOf(Usage{}),
		reflect.TypeOf(ServerToolUseUsage{}),
		reflect.TypeOf(Container{}),
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
