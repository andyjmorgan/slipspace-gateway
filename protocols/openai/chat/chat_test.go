package chat

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestChatCompletionRequest_NonStreamingWithToolCalls(t *testing.T) {
	in := []byte(`{` +
		`"model":"gpt-4o-mini",` +
		`"messages":[` +
		`{"role":"system","content":"You are helpful."},` +
		`{"role":"user","content":"What is the weather?"}` +
		`],` +
		`"temperature":0.2,` +
		`"max_tokens":256,` +
		`"tools":[{"type":"function","function":{"name":"get_weather","description":"Lookup weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}],` +
		`"tool_choice":"auto"` +
		`}`)
	var req ChatCompletionRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Model != "gpt-4o-mini" {
		t.Fatalf("model = %q", req.Model)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %d", len(req.Messages))
	}
	sys, ok := req.Messages[0].(*SystemMessage)
	if !ok {
		t.Fatalf("messages[0] = %T", req.Messages[0])
	}
	if sys.Content != "You are helpful." {
		t.Fatalf("system content = %q", sys.Content)
	}
	if req.Temperature == nil || *req.Temperature != 0.2 {
		t.Fatalf("temperature = %v", req.Temperature)
	}
	roundTripJSON(t, in, &req)
}

func TestChatCompletionRequest_VisionMultimodal(t *testing.T) {
	in := []byte(`{` +
		`"messages":[{"content":[{"text":"What is in this image?","type":"text"},{"image_url":{"detail":"high","url":"https://example.com/cat.png"},"type":"image_url"}],"role":"user"}],` +
		`"model":"gpt-4o"` +
		`}`)
	var req ChatCompletionRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	user, ok := req.Messages[0].(*UserMessage)
	if !ok {
		t.Fatalf("messages[0] = %T", req.Messages[0])
	}
	parts, err := user.Content.AsParts()
	if err != nil {
		t.Fatalf("parts: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d", len(parts))
	}
	if parts[0].PartType() != "text" {
		t.Fatalf("part 0 type = %q", parts[0].PartType())
	}
	img, ok := parts[1].(*ImageURLContentPart)
	if !ok {
		t.Fatalf("part 1 = %T", parts[1])
	}
	if img.ImageURL.URL != "https://example.com/cat.png" {
		t.Fatalf("image url = %q", img.ImageURL.URL)
	}
	roundTripJSON(t, in, &req)
}

func TestChatCompletionRequest_StringContent(t *testing.T) {
	in := []byte(`{"messages":[{"content":"hello","role":"user"}],"model":"gpt-4o-mini"}`)
	var req ChatCompletionRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	user, ok := req.Messages[0].(*UserMessage)
	if !ok {
		t.Fatalf("messages[0] = %T", req.Messages[0])
	}
	s, err := user.Content.AsString()
	if err != nil {
		t.Fatalf("as string: %v", err)
	}
	if s != "hello" {
		t.Fatalf("content = %q", s)
	}
	roundTripJSON(t, in, &req)
}

func TestChatCompletionRequest_UnknownTopLevelField(t *testing.T) {
	in := []byte(`{"messages":[{"content":"hi","role":"user"}],"model":"gpt-4o-mini","new_provider_field":{"x":1}}`)
	var req ChatCompletionRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(req.Extra["new_provider_field"]) != `{"x":1}` {
		t.Fatalf("extra: %v", req.Extra)
	}
	roundTripJSON(t, in, &req)
}

func TestChatCompletionRequest_UnknownRole(t *testing.T) {
	in := []byte(`{"messages":[{"content":"hi","extra":"keep","role":"future_role"}],"model":"gpt-4o-mini"}`)
	var req ChatCompletionRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	u, ok := req.Messages[0].(*UnknownRequestMessage)
	if !ok {
		t.Fatalf("messages[0] = %T", req.Messages[0])
	}
	if u.Role() != "future_role" {
		t.Fatalf("role = %q", u.Role())
	}
	if string(u.Extra["content"]) != `"hi"` {
		t.Fatalf("content not preserved: %v", u.Extra)
	}
	if string(u.Extra["extra"]) != `"keep"` {
		t.Fatalf("extra not preserved: %v", u.Extra)
	}
	roundTripJSON(t, in, &req)
}

func TestChatCompletionResponse_NonStreaming(t *testing.T) {
	in := []byte(`{` +
		`"choices":[{"finish_reason":"stop","index":0,"message":{"content":"Hello there!","role":"assistant"}}],` +
		`"created":1700000000,` +
		`"id":"chatcmpl-abc",` +
		`"model":"gpt-4o-mini",` +
		`"object":"chat.completion",` +
		`"system_fingerprint":"fp_123",` +
		`"usage":{"completion_tokens":3,"prompt_tokens":10,"total_tokens":13}` +
		`}`)
	var resp ChatCompletionResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID != "chatcmpl-abc" {
		t.Fatalf("id = %q", resp.ID)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d", len(resp.Choices))
	}
	if resp.Usage.TotalTokens != 13 {
		t.Fatalf("usage total = %d", resp.Usage.TotalTokens)
	}
	roundTripJSON(t, in, &resp)
}

func TestChatCompletionResponse_WithToolCall(t *testing.T) {
	in := []byte(`{` +
		`"choices":[{"finish_reason":"tool_calls","index":0,"message":{"content":null,"role":"assistant","tool_calls":[{"function":{"arguments":"{\"city\":\"SF\"}","name":"get_weather"},"id":"call_1","type":"function"}]}}],` +
		`"created":1700000000,` +
		`"id":"chatcmpl-tc",` +
		`"model":"gpt-4o-mini",` +
		`"object":"chat.completion"` +
		`}`)
	var resp ChatCompletionResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(resp.Choices[0].Message.ToolCalls))
	}
	tc := resp.Choices[0].Message.ToolCalls[0]
	if tc.Function.Name != "get_weather" {
		t.Fatalf("tool name = %q", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"city":"SF"}` {
		t.Fatalf("tool args = %q", tc.Function.Arguments)
	}
	roundTripJSON(t, in, &resp)
}

func TestChatCompletionResponse_AssistantRefusal(t *testing.T) {
	in := []byte(`{` +
		`"choices":[{"finish_reason":"stop","index":0,"message":{"refusal":"I can't help with that.","role":"assistant"}}],` +
		`"created":1700000000,` +
		`"id":"chatcmpl-refusal",` +
		`"model":"gpt-4o-mini",` +
		`"object":"chat.completion"` +
		`}`)
	var resp ChatCompletionResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Choices[0].Message.Refusal == nil || *resp.Choices[0].Message.Refusal != "I can't help with that." {
		t.Fatalf("refusal = %v", resp.Choices[0].Message.Refusal)
	}
	roundTripJSON(t, in, &resp)
}

func TestChatCompletionChunk_StreamingDelta(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"content":"Hello","role":"assistant"},"finish_reason":null,"index":0}],"created":1700000000,"id":"chatcmpl-s1","model":"gpt-4o-mini","object":"chat.completion.chunk"}`,
		`{"choices":[{"delta":{"content":" there"},"finish_reason":null,"index":0}],"created":1700000000,"id":"chatcmpl-s1","model":"gpt-4o-mini","object":"chat.completion.chunk"}`,
		`{"choices":[{"delta":{},"finish_reason":"stop","index":0}],"created":1700000000,"id":"chatcmpl-s1","model":"gpt-4o-mini","object":"chat.completion.chunk"}`,
	}
	for i, c := range chunks {
		var chunk ChatCompletionChunk
		if err := json.Unmarshal([]byte(c), &chunk); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
		out, err := json.Marshal(chunk)
		if err != nil {
			t.Fatalf("marshal %d: %v", i, err)
		}
		if !jsonValueEqual(t, []byte(c), out) {
			t.Fatalf("chunk %d drift\n in: %s\nout: %s", i, c, out)
		}
	}
}

func TestChatCompletionChunk_ToolCallDelta(t *testing.T) {
	in := []byte(`{` +
		`"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"","name":"get_weather"},"id":"call_1","index":0,"type":"function"}]},"finish_reason":null,"index":0}],` +
		`"created":1700000000,` +
		`"id":"chatcmpl-s2",` +
		`"model":"gpt-4o-mini",` +
		`"object":"chat.completion.chunk"` +
		`}`)
	var chunk ChatCompletionChunk
	if err := json.Unmarshal(in, &chunk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(chunk.Choices[0].Delta.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(chunk.Choices[0].Delta.ToolCalls))
	}
	roundTripJSON(t, in, &chunk)
}

func TestRequestMessage_AllRoles(t *testing.T) {
	cases := []struct {
		name string

		json string

		role string

		want any
	}{
		{name: "system", json: `{"role":"system","content":"You are helpful."}`, role: "system", want: &SystemMessage{}},
		{name: "developer", json: `{"role":"developer","content":"Internal instructions."}`, role: "developer", want: &DeveloperMessage{}},
		{name: "user_string", json: `{"role":"user","content":"hi"}`, role: "user", want: &UserMessage{}},
		{name: "assistant", json: `{"role":"assistant","content":"hi"}`, role: "assistant", want: &AssistantMessage{}},
		{name: "tool", json: `{"role":"tool","content":"42","tool_call_id":"call_1"}`, role: "tool", want: &ToolMessage{}},
		{name: "function", json: `{"role":"function","content":"42","name":"do_math"}`, role: "function", want: &FunctionMessage{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := UnmarshalRequestMessage([]byte(tc.json))
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if reflect.TypeOf(m) != reflect.TypeOf(tc.want) {
				t.Fatalf("got %T want %T", m, tc.want)
			}
			if m.Role() != tc.role {
				t.Fatalf("role = %q want %q", m.Role(), tc.role)
			}
			out, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !jsonValueEqual(t, []byte(tc.json), out) {
				t.Fatalf("drift\n in: %s\nout: %s", tc.json, out)
			}
		})
	}
}

func TestUnmarshalRequestMessage_MissingRole(t *testing.T) {
	_, err := UnmarshalRequestMessage([]byte(`{"content":"hi"}`))
	if err == nil {
		t.Fatalf("expected error for missing role")
	}
}

func TestUnmarshalRequestMessages_NonArray(t *testing.T) {
	_, err := UnmarshalRequestMessages([]byte(`{"not":"array"}`))
	if err == nil {
		t.Fatalf("expected error on non-array input")
	}
}

func TestContentPart_AllVariants(t *testing.T) {
	cases := []struct {
		name string

		json string

		typ string

		want any
	}{
		{name: "text", json: `{"text":"hi","type":"text"}`, typ: "text", want: &TextContentPart{}},
		{name: "image_url", json: `{"image_url":{"detail":"high","url":"https://example.com/a.png"},"type":"image_url"}`, typ: "image_url", want: &ImageURLContentPart{}},
		{name: "input_audio", json: `{"input_audio":{"data":"AAA","format":"wav"},"type":"input_audio"}`, typ: "input_audio", want: &InputAudioContentPart{}},
		{name: "audio", json: `{"audio":{"id":"a_1","transcript":"hello"},"type":"audio"}`, typ: "audio", want: &OutputAudioContentPart{}},
		{name: "refusal", json: `{"refusal":"no","type":"refusal"}`, typ: "refusal", want: &RefusalContentPart{}},
		{name: "file", json: `{"file":{"file_id":"file_1"},"type":"file"}`, typ: "file", want: &FileContentPart{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := UnmarshalContentPart([]byte(tc.json))
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if reflect.TypeOf(p) != reflect.TypeOf(tc.want) {
				t.Fatalf("got %T want %T", p, tc.want)
			}
			if p.PartType() != tc.typ {
				t.Fatalf("part type = %q want %q", p.PartType(), tc.typ)
			}
			out, err := json.Marshal(p)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !jsonValueEqual(t, []byte(tc.json), out) {
				t.Fatalf("drift\n in: %s\nout: %s", tc.json, out)
			}
		})
	}
}

func TestContentPart_UnknownDiscriminatorRoundTrips(t *testing.T) {
	in := []byte(`{"future_field":42,"type":"video_url","video_url":"https://example.com/v.mp4"}`)
	p, err := UnmarshalContentPart(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	u, ok := p.(*UnknownContentPart)
	if !ok {
		t.Fatalf("got %T", p)
	}
	if u.PartType() != "video_url" {
		t.Fatalf("type = %q", u.PartType())
	}
	if string(u.Extra["video_url"]) != `"https://example.com/v.mp4"` {
		t.Fatalf("extras: %v", u.Extra)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}
}

func TestContentPart_TextWithUnknownField(t *testing.T) {
	in := []byte(`{"annotations":[{"start":0,"end":2}],"text":"hi","type":"text"}`)
	p, err := UnmarshalContentPart(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tp, ok := p.(*TextContentPart)
	if !ok {
		t.Fatalf("got %T", p)
	}
	if string(tp.Extra["annotations"]) != `[{"start":0,"end":2}]` {
		t.Fatalf("extras: %v", tp.Extra)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}
}

func TestContentPart_MissingType(t *testing.T) {
	_, err := UnmarshalContentPart([]byte(`{"text":"no type"}`))
	if err == nil {
		t.Fatalf("expected error for missing type")
	}
}

func TestContentParts_NonArray(t *testing.T) {
	_, err := UnmarshalContentParts([]byte(`{"not":"array"}`))
	if err == nil {
		t.Fatalf("expected error on non-array input")
	}
}

func TestMessageContent_NewStringContent(t *testing.T) {
	c := NewStringContent("hello \"world\"")
	if !c.IsString() {
		t.Fatalf("not string")
	}
	s, err := c.AsString()
	if err != nil {
		t.Fatalf("as string: %v", err)
	}
	if s != `hello "world"` {
		t.Fatalf("got %q", s)
	}
	if _, err := c.AsParts(); err == nil {
		t.Fatalf("expected error converting string to parts")
	}
}

func TestMessageContent_NewPartsContent(t *testing.T) {
	parts := []ContentPart{
		&TextContentPart{Type: "text", Text: "hi"},
		&ImageURLContentPart{Type: "image_url", ImageURL: ImageURL{URL: "https://example.com/a.png"}},
	}
	c, err := NewPartsContent(parts)
	if err != nil {
		t.Fatalf("new parts: %v", err)
	}
	if !c.IsArray() {
		t.Fatalf("not array")
	}
	got, err := c.AsParts()
	if err != nil {
		t.Fatalf("as parts: %v", err)
	}
	if len(got) != 2 || got[0].PartType() != "text" {
		t.Fatalf("got %d parts: %v", len(got), got)
	}
	if _, err := c.AsString(); err == nil {
		t.Fatalf("expected error converting array to string")
	}
}

func TestMessageContent_NullAndRaw(t *testing.T) {
	var c MessageContent
	if !c.IsNull() {
		t.Fatalf("expected null")
	}
	out, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != "null" {
		t.Fatalf("got %q", out)
	}

	in := []byte(`"hello"`)
	if err := json.Unmarshal(in, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !c.IsString() {
		t.Fatalf("expected string after unmarshal")
	}
	if string(c.Raw()) != `"hello"` {
		t.Fatalf("raw = %s", c.Raw())
	}
}

func TestChat_AllExportedFieldsHaveJSONTag(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(ChatCompletionRequest{}),
		reflect.TypeOf(ChatCompletionResponse{}),
		reflect.TypeOf(ChatCompletionChunk{}),
		reflect.TypeOf(Choice{}),
		reflect.TypeOf(ChunkChoice{}),
		reflect.TypeOf(Usage{}),
		reflect.TypeOf(PromptTokensDetails{}),
		reflect.TypeOf(CompletionTokensDetails{}),
		reflect.TypeOf(StreamOptions{}),
		reflect.TypeOf(Tool{}),
		reflect.TypeOf(ToolFunction{}),
		reflect.TypeOf(ResponseFormat{}),
		reflect.TypeOf(ResponseFormatJSONSchema{}),
		reflect.TypeOf(AudioOptions{}),
		reflect.TypeOf(AudioMessage{}),
		reflect.TypeOf(ResponseMessage{}),
		reflect.TypeOf(DeltaMessage{}),
		reflect.TypeOf(ToolCall{}),
		reflect.TypeOf(ToolCallDelta{}),
		reflect.TypeOf(ToolCallFunction{}),
		reflect.TypeOf(ToolCallFunctionDelta{}),
		reflect.TypeOf(SystemMessage{}),
		reflect.TypeOf(DeveloperMessage{}),
		reflect.TypeOf(UserMessage{}),
		reflect.TypeOf(AssistantMessage{}),
		reflect.TypeOf(ToolMessage{}),
		reflect.TypeOf(FunctionMessage{}),
		reflect.TypeOf(UnknownRequestMessage{}),
		reflect.TypeOf(TextContentPart{}),
		reflect.TypeOf(ImageURL{}),
		reflect.TypeOf(ImageURLContentPart{}),
		reflect.TypeOf(InputAudio{}),
		reflect.TypeOf(InputAudioContentPart{}),
		reflect.TypeOf(OutputAudioContentPart{}),
		reflect.TypeOf(RefusalContentPart{}),
		reflect.TypeOf(File{}),
		reflect.TypeOf(FileContentPart{}),
		reflect.TypeOf(UnknownContentPart{}),
		reflect.TypeOf(URLCitation{}),
		reflect.TypeOf(UrlCitationAnnotation{}),
		reflect.TypeOf(UnknownAnnotation{}),
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

func TestChat_RegistriesHaveFallbacks(t *testing.T) {
	if requestMessageRegistry.Fallback == nil {
		t.Fatalf("requestMessageRegistry must have a fallback")
	}
	if contentPartRegistry.Fallback == nil {
		t.Fatalf("contentPartRegistry must have a fallback")
	}
	if annotationRegistry.Fallback == nil {
		t.Fatalf("annotationRegistry must have a fallback")
	}
	wantRoles := map[string]bool{
		"system":    false,
		"developer": false,
		"user":      false,
		"assistant": false,
		"tool":      false,
		"function":  false,
	}
	for k := range requestMessageRegistry.Factories {
		if _, ok := wantRoles[k]; !ok {
			t.Errorf("unexpected role %q", k)
		}
		wantRoles[k] = true
	}
	for k, seen := range wantRoles {
		if !seen {
			t.Errorf("missing role %q", k)
		}
	}
	wantParts := map[string]bool{
		"text":        false,
		"image_url":   false,
		"input_audio": false,
		"audio":       false,
		"refusal":     false,
		"file":        false,
	}
	for k := range contentPartRegistry.Factories {
		if _, ok := wantParts[k]; !ok {
			t.Errorf("unexpected part %q", k)
		}
		wantParts[k] = true
	}
	for k, seen := range wantParts {
		if !seen {
			t.Errorf("missing part %q", k)
		}
	}
}

func roundTripJSON(t *testing.T, in []byte, v any) {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func jsonValueEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("b: %v", err)
	}
	return reflect.DeepEqual(av, bv)
}
