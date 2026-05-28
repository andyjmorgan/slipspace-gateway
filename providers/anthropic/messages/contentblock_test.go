package messages

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/models"
)

func TestUnmarshalContentBlock_Text(t *testing.T) {
	in := []byte(`{"type":"text","text":"hello"}`)
	v, err := UnmarshalContentBlock(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tb, ok := v.(*TextBlock)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if tb.Text != "hello" {
		t.Fatalf("text = %q", tb.Text)
	}
	if tb.BlockType() != "text" {
		t.Fatalf("block type = %q", tb.BlockType())
	}
	roundTrip(t, in, v)
}

func TestUnmarshalContentBlock_Image(t *testing.T) {
	in := []byte(`{"source":{"data":"AAA","media_type":"image/png","type":"base64"},"type":"image"}`)
	v, err := UnmarshalContentBlock(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ib, ok := v.(*ImageBlock)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if ib.Source.MediaType != "image/png" || ib.Source.Data != "AAA" {
		t.Fatalf("image source = %+v", ib.Source)
	}
	if ib.BlockType() != "image" {
		t.Fatalf("block type = %q", ib.BlockType())
	}
	roundTrip(t, in, v)
}

func TestUnmarshalContentBlock_ToolUse(t *testing.T) {
	in := []byte(`{"id":"tool_1","input":{"q":"weather"},"name":"search","type":"tool_use"}`)
	v, err := UnmarshalContentBlock(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tu, ok := v.(*ToolUseBlock)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if tu.ID != "tool_1" || tu.Name != "search" {
		t.Fatalf("tool_use = %+v", tu)
	}
	if tu.BlockType() != "tool_use" {
		t.Fatalf("block type = %q", tu.BlockType())
	}
	roundTrip(t, in, v)
}

func TestUnmarshalContentBlock_ToolResult(t *testing.T) {
	in := []byte(`{"content":"42","is_error":false,"tool_use_id":"tool_1","type":"tool_result"}`)
	v, err := UnmarshalContentBlock(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tr, ok := v.(*ToolResultBlock)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if tr.ToolUseID != "tool_1" {
		t.Fatalf("tool_use_id = %q", tr.ToolUseID)
	}
	if tr.IsError == nil || *tr.IsError {
		t.Fatalf("is_error = %v", tr.IsError)
	}
	if tr.BlockType() != "tool_result" {
		t.Fatalf("block type = %q", tr.BlockType())
	}
	roundTrip(t, in, v)
}

func TestUnmarshalContentBlock_Thinking(t *testing.T) {
	in := []byte(`{"signature":"EtgFCmYI","thinking":"Let me work through this.","type":"thinking"}`)
	v, err := UnmarshalContentBlock(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tb, ok := v.(*ThinkingBlock)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if tb.Thinking != "Let me work through this." || tb.Signature != "EtgFCmYI" {
		t.Fatalf("thinking = %+v", tb)
	}
	if tb.BlockType() != "thinking" {
		t.Fatalf("block type = %q", tb.BlockType())
	}
	roundTrip(t, in, v)
}

func TestUnmarshalContentBlock_ThinkingExtraFieldsRoundTrip(t *testing.T) {
	// A field Anthropic adds to a thinking block must survive via Extra.
	in := []byte(`{"signature":"sig","estimated_tokens":50,"thinking":"hmm","type":"thinking"}`)
	v, err := UnmarshalContentBlock(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tb, ok := v.(*ThinkingBlock)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if _, ok := tb.Extra["estimated_tokens"]; !ok {
		t.Fatalf("estimated_tokens not preserved in Extra: %+v", tb.Extra)
	}
	roundTrip(t, in, v)
}

func TestUnmarshalContentBlock_RedactedThinking(t *testing.T) {
	in := []byte(`{"data":"EvwBCkgYAiADKkBx...","type":"redacted_thinking"}`)
	v, err := UnmarshalContentBlock(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rb, ok := v.(*RedactedThinkingBlock)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if rb.Data != "EvwBCkgYAiADKkBx..." {
		t.Fatalf("data = %q", rb.Data)
	}
	if rb.BlockType() != "redacted_thinking" {
		t.Fatalf("block type = %q", rb.BlockType())
	}
	roundTrip(t, in, v)
}

func TestUnmarshalContentBlock_UnknownDiscriminatorRoundTrips(t *testing.T) {
	in := []byte(`{"lat":40.7,"lon":-74,"type":"weather_widget"}`)
	v, err := UnmarshalContentBlock(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	u, ok := v.(*UnknownBlock)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if u.Type != "weather_widget" {
		t.Fatalf("type = %q", u.Type)
	}
	if u.BlockType() != "weather_widget" {
		t.Fatalf("block type = %q", u.BlockType())
	}
	if string(u.Extra["lat"]) != `40.7` || string(u.Extra["lon"]) != `-74` {
		t.Fatalf("extras not preserved: %v", u.Extra)
	}
	roundTrip(t, in, v)
}

func TestUnmarshalContentBlock_TextWithExtraFieldsRoundTrip(t *testing.T) {
	in := []byte(`{"cache_control":{"type":"ephemeral"},"text":"hi","type":"text"}`)
	v, err := UnmarshalContentBlock(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tb, ok := v.(*TextBlock)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if string(tb.Extra["cache_control"]) != `{"type":"ephemeral"}` {
		t.Fatalf("extras: %v", tb.Extra)
	}
	roundTrip(t, in, v)
}

func TestUnmarshalContentBlock_ImageSourceExtraFields(t *testing.T) {
	in := []byte(`{"source":{"data":"AAA","future_field":"keep","media_type":"image/png","type":"base64"},"type":"image"}`)
	v, err := UnmarshalContentBlock(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ib, ok := v.(*ImageBlock)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if string(ib.Source.Extra["future_field"]) != `"keep"` {
		t.Fatalf("nested extras: %v", ib.Source.Extra)
	}
	roundTrip(t, in, v)
}

func TestUnmarshalContentBlock_MissingType(t *testing.T) {
	_, err := UnmarshalContentBlock([]byte(`{"text":"no type"}`))
	if !errors.Is(err, models.ErrMissingDiscriminator) {
		t.Fatalf("want ErrMissingDiscriminator, got %v", err)
	}
}

func TestUnmarshalContentBlocks_MixedSlice(t *testing.T) {
	in := []byte(`[` +
		`{"type":"text","text":"hi"},` +
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAA"}},` +
		`{"type":"tool_use","id":"t1","name":"search","input":{"q":"x"}},` +
		`{"type":"tool_result","tool_use_id":"t1","content":"ok"},` +
		`{"type":"weather_widget","lat":40.7}` +
		`]`)
	blocks, err := UnmarshalContentBlocks(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(blocks) != 5 {
		t.Fatalf("len = %d", len(blocks))
	}
	wantTypes := []string{"text", "image", "tool_use", "tool_result", "weather_widget"}
	for i, b := range blocks {
		if b.BlockType() != wantTypes[i] {
			t.Fatalf("elem %d type = %q want %q", i, b.BlockType(), wantTypes[i])
		}
	}
	if _, ok := blocks[4].(*UnknownBlock); !ok {
		t.Fatalf("elem 4 = %T", blocks[4])
	}
}

func TestUnmarshalContentBlocks_NonArray(t *testing.T) {
	_, err := UnmarshalContentBlocks([]byte(`{"not":"array"}`))
	if err == nil {
		t.Fatalf("expected error on non-array input")
	}
}

func TestContentBlock_AllExportedFieldsHaveJSONTag(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(TextBlock{}),
		reflect.TypeOf(ImageBlock{}),
		reflect.TypeOf(ImageSource{}),
		reflect.TypeOf(ToolUseBlock{}),
		reflect.TypeOf(ToolResultBlock{}),
		reflect.TypeOf(ThinkingBlock{}),
		reflect.TypeOf(RedactedThinkingBlock{}),
		reflect.TypeOf(UnknownBlock{}),
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

func TestContentBlock_RegistryCoversAllConcreteTypes(t *testing.T) {
	want := map[string]bool{"text": false, "image": false, "tool_use": false, "tool_result": false, "thinking": false, "redacted_thinking": false}
	for k := range blockRegistry.Factories {
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
	if blockRegistry.Fallback == nil {
		t.Fatalf("registry must have a fallback so unknown discriminators round-trip")
	}
}

func roundTrip(t *testing.T, in []byte, v ContentBlock) {
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
