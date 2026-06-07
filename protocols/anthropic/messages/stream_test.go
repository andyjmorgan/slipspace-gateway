package messages

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/models"
)

func TestUnmarshalStreamEvent_MessageStart(t *testing.T) {
	in := []byte(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":0}}}`)
	streamRoundTrip(t, in, "message_start", func(ev StreamEvent) {
		ms, ok := ev.(*MessageStartEvent)
		if !ok {
			t.Fatalf("got %T", ev)
		}
		if ms.Message.ID != "msg_1" {
			t.Fatalf("message id = %q", ms.Message.ID)
		}
	})
}

func TestUnmarshalStreamEvent_ContentBlockStart(t *testing.T) {
	in := []byte(`{"content_block":{"text":"","type":"text"},"index":0,"type":"content_block_start"}`)
	streamRoundTrip(t, in, "content_block_start", func(ev StreamEvent) {
		cbs, ok := ev.(*ContentBlockStartEvent)
		if !ok {
			t.Fatalf("got %T", ev)
		}
		if cbs.Index != 0 {
			t.Fatalf("index = %d", cbs.Index)
		}
		if _, ok := cbs.ContentBlock.(*TextBlock); !ok {
			t.Fatalf("content_block = %T", cbs.ContentBlock)
		}
	})
}

func TestUnmarshalStreamEvent_ContentBlockStartUnknownBlock(t *testing.T) {
	in := []byte(`{"content_block":{"payload":"keep","type":"future_block"},"index":2,"type":"content_block_start"}`)
	streamRoundTrip(t, in, "content_block_start", func(ev StreamEvent) {
		cbs := ev.(*ContentBlockStartEvent)
		if u, ok := cbs.ContentBlock.(*UnknownBlock); !ok || u.Type != "future_block" {
			t.Fatalf("content_block = %T (%+v)", cbs.ContentBlock, cbs.ContentBlock)
		}
	})
}

func TestUnmarshalStreamEvent_ContentBlockStartNullBlock(t *testing.T) {
	in := []byte(`{"content_block":null,"index":1,"type":"content_block_start"}`)
	ev, err := UnmarshalStreamEvent(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cbs := ev.(*ContentBlockStartEvent)
	if cbs.ContentBlock != nil {
		t.Fatalf("content_block = %v", cbs.ContentBlock)
	}
}

func TestUnmarshalStreamEvent_ContentBlockStartBadBlock(t *testing.T) {
	in := []byte(`{"content_block":{"text":"no type"},"index":0,"type":"content_block_start"}`)
	if _, err := UnmarshalStreamEvent(in); err == nil {
		t.Fatalf("expected error on missing block type")
	}
}

func TestUnmarshalStreamEvent_ContentBlockDelta_Text(t *testing.T) {
	in := []byte(`{"delta":{"text":"hi","type":"text_delta"},"index":0,"type":"content_block_delta"}`)
	streamRoundTrip(t, in, "content_block_delta", func(ev StreamEvent) {
		cbd, ok := ev.(*ContentBlockDeltaEvent)
		if !ok {
			t.Fatalf("got %T", ev)
		}
		td, ok := cbd.Delta.(*TextDelta)
		if !ok {
			t.Fatalf("delta = %T", cbd.Delta)
		}
		if td.Text != "hi" {
			t.Fatalf("text = %q", td.Text)
		}
	})
}

func TestUnmarshalStreamEvent_ContentBlockDelta_InputJSON(t *testing.T) {
	in := []byte(`{"delta":{"partial_json":"{\"q\":","type":"input_json_delta"},"index":1,"type":"content_block_delta"}`)
	streamRoundTrip(t, in, "content_block_delta", func(ev StreamEvent) {
		cbd := ev.(*ContentBlockDeltaEvent)
		ij, ok := cbd.Delta.(*InputJSONDelta)
		if !ok || ij.PartialJSON != `{"q":` {
			t.Fatalf("delta = %T %+v", cbd.Delta, cbd.Delta)
		}
	})
}

func TestUnmarshalStreamEvent_ContentBlockDelta_Thinking(t *testing.T) {
	in := []byte(`{"delta":{"thinking":"pondering","type":"thinking_delta"},"index":0,"type":"content_block_delta"}`)
	streamRoundTrip(t, in, "content_block_delta", func(ev StreamEvent) {
		cbd := ev.(*ContentBlockDeltaEvent)
		td, ok := cbd.Delta.(*ThinkingDelta)
		if !ok || td.Thinking != "pondering" {
			t.Fatalf("delta = %T %+v", cbd.Delta, cbd.Delta)
		}
	})
}

func TestUnmarshalStreamEvent_ContentBlockDelta_Signature(t *testing.T) {
	in := []byte(`{"delta":{"signature":"sigblob","type":"signature_delta"},"index":0,"type":"content_block_delta"}`)
	streamRoundTrip(t, in, "content_block_delta", func(ev StreamEvent) {
		cbd := ev.(*ContentBlockDeltaEvent)
		sd, ok := cbd.Delta.(*SignatureDelta)
		if !ok || sd.Signature != "sigblob" {
			t.Fatalf("delta = %T %+v", cbd.Delta, cbd.Delta)
		}
	})
}

func TestUnmarshalStreamEvent_ContentBlockDelta_UnknownDelta(t *testing.T) {
	in := []byte(`{"delta":{"future":"keep","type":"future_delta"},"index":0,"type":"content_block_delta"}`)
	streamRoundTrip(t, in, "content_block_delta", func(ev StreamEvent) {
		cbd := ev.(*ContentBlockDeltaEvent)
		u, ok := cbd.Delta.(*UnknownContentBlockDelta)
		if !ok || u.Type != "future_delta" {
			t.Fatalf("delta = %T %+v", cbd.Delta, cbd.Delta)
		}
		if string(u.Extra["future"]) != `"keep"` {
			t.Fatalf("delta extras: %v", u.Extra)
		}
	})
}

func TestUnmarshalStreamEvent_ContentBlockDelta_NullDelta(t *testing.T) {
	in := []byte(`{"delta":null,"index":0,"type":"content_block_delta"}`)
	ev, err := UnmarshalStreamEvent(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cbd := ev.(*ContentBlockDeltaEvent)
	if cbd.Delta != nil {
		t.Fatalf("delta = %v", cbd.Delta)
	}
}

func TestUnmarshalStreamEvent_ContentBlockDelta_BadDelta(t *testing.T) {
	in := []byte(`{"delta":{"text":"missing type"},"index":0,"type":"content_block_delta"}`)
	if _, err := UnmarshalStreamEvent(in); err == nil {
		t.Fatalf("expected error on missing delta type")
	}
}

func TestUnmarshalStreamEvent_ContentBlockStop(t *testing.T) {
	in := []byte(`{"index":0,"type":"content_block_stop"}`)
	streamRoundTrip(t, in, "content_block_stop", func(ev StreamEvent) {
		if _, ok := ev.(*ContentBlockStopEvent); !ok {
			t.Fatalf("got %T", ev)
		}
	})
}

func TestUnmarshalStreamEvent_MessageDelta(t *testing.T) {
	in := []byte(`{"delta":{"stop_reason":"end_turn","stop_sequence":null},"type":"message_delta","usage":{"input_tokens":0,"output_tokens":42}}`)
	streamRoundTrip(t, in, "message_delta", func(ev StreamEvent) {
		md, ok := ev.(*MessageDeltaEvent)
		if !ok {
			t.Fatalf("got %T", ev)
		}
		if md.Delta.StopReason == nil || *md.Delta.StopReason != "end_turn" {
			t.Fatalf("stop_reason = %v", md.Delta.StopReason)
		}
		if md.Usage.OutputTokens != 42 {
			t.Fatalf("output_tokens = %d", md.Usage.OutputTokens)
		}
	})
}

func TestUnmarshalStreamEvent_MessageStop(t *testing.T) {
	in := []byte(`{"type":"message_stop"}`)
	streamRoundTrip(t, in, "message_stop", func(ev StreamEvent) {
		if _, ok := ev.(*MessageStopEvent); !ok {
			t.Fatalf("got %T", ev)
		}
	})
}

func TestUnmarshalStreamEvent_Ping(t *testing.T) {
	in := []byte(`{"type":"ping"}`)
	streamRoundTrip(t, in, "ping", func(ev StreamEvent) {
		if _, ok := ev.(*PingEvent); !ok {
			t.Fatalf("got %T", ev)
		}
	})
}

func TestUnmarshalStreamEvent_Error(t *testing.T) {
	// request_id is the top-level "req_"-prefixed identifier Anthropic
	// surfaces in error bodies alongside the error object; it decodes typed
	// and round-trips.
	in := []byte(`{"error":{"message":"oops","type":"overloaded_error"},"request_id":"req_011Cabc","type":"error"}`)
	streamRoundTrip(t, in, "error", func(ev StreamEvent) {
		ee, ok := ev.(*ErrorEvent)
		if !ok {
			t.Fatalf("got %T", ev)
		}
		if ee.Error.Type != "overloaded_error" || ee.Error.Message != "oops" {
			t.Fatalf("error = %+v", ee.Error)
		}
		if ee.RequestID != "req_011Cabc" {
			t.Fatalf("request_id = %q", ee.RequestID)
		}
		if len(ee.Extra) != 0 {
			t.Fatalf("unmapped fields leaked: %v", ee.Extra)
		}
	})
}

func TestUnmarshalStreamEvent_UnknownEvent(t *testing.T) {
	in := []byte(`{"future":"keep","type":"future_event"}`)
	ev, err := UnmarshalStreamEvent(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	u, ok := ev.(*UnknownStreamEvent)
	if !ok {
		t.Fatalf("got %T", ev)
	}
	if u.Type != "future_event" {
		t.Fatalf("type = %q", u.Type)
	}
	if u.EventType() != "future_event" {
		t.Fatalf("event_type = %q", u.EventType())
	}
	if string(u.Extra["future"]) != `"keep"` {
		t.Fatalf("extras = %v", u.Extra)
	}
	out, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func TestContentBlockStartEvent_InvalidJSON(t *testing.T) {
	var e ContentBlockStartEvent
	if err := json.Unmarshal([]byte(`"not an object"`), &e); err == nil {
		t.Fatalf("expected error on non-object input")
	}
}

func TestContentBlockDeltaEvent_InvalidJSON(t *testing.T) {
	var e ContentBlockDeltaEvent
	if err := json.Unmarshal([]byte(`"not an object"`), &e); err == nil {
		t.Fatalf("expected error on non-object input")
	}
}

func TestUnmarshalStreamEvent_MissingType(t *testing.T) {
	_, err := UnmarshalStreamEvent([]byte(`{"index":0}`))
	if !errors.Is(err, models.ErrMissingDiscriminator) {
		t.Fatalf("want ErrMissingDiscriminator, got %v", err)
	}
}

func TestUnmarshalContentBlockDelta_MissingType(t *testing.T) {
	_, err := UnmarshalContentBlockDelta([]byte(`{"text":"no type"}`))
	if !errors.Is(err, models.ErrMissingDiscriminator) {
		t.Fatalf("want ErrMissingDiscriminator, got %v", err)
	}
}

func TestStream_AllExportedFieldsHaveJSONTag(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(MessageStartEvent{}),
		reflect.TypeOf(ContentBlockStartEvent{}),
		reflect.TypeOf(ContentBlockDeltaEvent{}),
		reflect.TypeOf(ContentBlockStopEvent{}),
		reflect.TypeOf(MessageDeltaEvent{}),
		reflect.TypeOf(MessageStopEvent{}),
		reflect.TypeOf(PingEvent{}),
		reflect.TypeOf(ErrorEvent{}),
		reflect.TypeOf(UnknownStreamEvent{}),
		reflect.TypeOf(StreamError{}),
		reflect.TypeOf(MessageDelta{}),
		reflect.TypeOf(TextDelta{}),
		reflect.TypeOf(InputJSONDelta{}),
		reflect.TypeOf(ThinkingDelta{}),
		reflect.TypeOf(SignatureDelta{}),
		reflect.TypeOf(UnknownContentBlockDelta{}),
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

func TestStream_RegistryCoversAllConcreteTypes(t *testing.T) {
	want := map[string]bool{
		"message_start":       false,
		"content_block_start": false,
		"content_block_delta": false,
		"content_block_stop":  false,
		"message_delta":       false,
		"message_stop":        false,
		"ping":                false,
		"error":               false,
	}
	for k := range streamEventRegistry.Factories {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected event registry key %q", k)
		}
		want[k] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("event registry missing factory for %q", k)
		}
	}
	if streamEventRegistry.Fallback == nil {
		t.Fatalf("event registry must have a fallback")
	}

	wantDelta := map[string]bool{"text_delta": false, "input_json_delta": false, "thinking_delta": false, "signature_delta": false}
	for k := range contentBlockDeltaRegistry.Factories {
		if _, ok := wantDelta[k]; !ok {
			t.Errorf("unexpected delta registry key %q", k)
		}
		wantDelta[k] = true
	}
	for k, seen := range wantDelta {
		if !seen {
			t.Errorf("delta registry missing factory for %q", k)
		}
	}
	if contentBlockDeltaRegistry.Fallback == nil {
		t.Fatalf("delta registry must have a fallback")
	}
}

func TestStreamEvent_EventTypeAccessors(t *testing.T) {
	cases := []struct {
		ev   StreamEvent
		want string
	}{
		{MessageStartEvent{}, "message_start"},
		{ContentBlockStartEvent{}, "content_block_start"},
		{ContentBlockDeltaEvent{}, "content_block_delta"},
		{ContentBlockStopEvent{}, "content_block_stop"},
		{MessageDeltaEvent{}, "message_delta"},
		{MessageStopEvent{}, "message_stop"},
		{PingEvent{}, "ping"},
		{ErrorEvent{}, "error"},
	}
	for _, c := range cases {
		if got := c.ev.EventType(); got != c.want {
			t.Errorf("EventType for %T = %q want %q", c.ev, got, c.want)
		}
	}
}

func TestContentBlockDelta_DeltaTypeAccessors(t *testing.T) {
	cases := []struct {
		d    ContentBlockDelta
		want string
	}{
		{TextDelta{}, "text_delta"},
		{InputJSONDelta{}, "input_json_delta"},
		{ThinkingDelta{}, "thinking_delta"},
		{SignatureDelta{}, "signature_delta"},
		{UnknownContentBlockDelta{Type: "x"}, "x"},
	}
	for _, c := range cases {
		if got := c.d.DeltaType(); got != c.want {
			t.Errorf("DeltaType for %T = %q want %q", c.d, got, c.want)
		}
	}
}

func streamRoundTrip(t *testing.T, in []byte, wantType string, inspect func(StreamEvent)) {
	t.Helper()
	ev, err := UnmarshalStreamEvent(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.EventType() != wantType {
		t.Fatalf("event_type = %q want %q", ev.EventType(), wantType)
	}
	inspect(ev)
	out, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}
