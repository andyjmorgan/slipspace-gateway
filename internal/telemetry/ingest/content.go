package ingest

import (
	"encoding/json"
	"strconv"
	"strings"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// Content span-attribute keys the gateway's reporter writes. Each attribute's
// value is itself a JSON string: input/output messages are [{role, parts:[...]}],
// system_instructions is a bare parts array, tool definitions are
// [{type, name, description, parameters}]. The gateway emits these on the span
// (not as span events), so the attribute path is the primary capture; the event
// scan stays as a fallback for any producer that emits gen_ai.* span events.
const (
	attrInputMessages      = "gen_ai.input.messages"
	attrOutputMessages     = "gen_ai.output.messages"
	attrToolDefinitions    = "gen_ai.tool.definitions"
	attrSystemInstructions = "gen_ai.system_instructions"
)

// captureContent extracts bounded gen_ai content from a span. The primary path
// reads the gateway's gen_ai.* content attributes (JSON-string values),
// producing {input_messages, output_messages, tool_definitions,
// system_instructions}; it falls back to the legacy gen_ai.* span-event scan.
// Returns nil when the span carries no gen_ai content, and a small
// {"truncated":...} marker when the content exceeds maxBytes. A maxBytes of 0
// or less disables the cap so the full content is kept.
func captureContent(span *tracepb.Span, maxBytes int) []byte {
	if b := captureContentAttrs(span, maxBytes); b != nil {
		return b
	}
	return captureContentEvents(span, maxBytes)
}

// captureContentAttrs builds the structured content object from the span's
// content attributes. Each attribute value is a JSON string; it is re-embedded
// as json.RawMessage so the wire payload is a real nested object rather than a
// doubly-escaped string. Non-JSON values are skipped. Returns nil when no
// content attribute is present.
func captureContentAttrs(span *tracepb.Span, maxBytes int) []byte {
	attrs := make(map[string]string, len(span.GetAttributes()))
	for _, kv := range span.GetAttributes() {
		switch kv.GetKey() {
		case attrInputMessages, attrOutputMessages, attrToolDefinitions, attrSystemInstructions:
			if s := anyValueString(kv.GetValue()); s != "" {
				attrs[kv.GetKey()] = s
			}
		}
	}
	if len(attrs) == 0 {
		return nil
	}
	out := struct {
		InputMessages      json.RawMessage `json:"input_messages,omitempty"`
		OutputMessages     json.RawMessage `json:"output_messages,omitempty"`
		ToolDefinitions    json.RawMessage `json:"tool_definitions,omitempty"`
		SystemInstructions json.RawMessage `json:"system_instructions,omitempty"`
	}{
		InputMessages:      rawJSON(attrs[attrInputMessages]),
		OutputMessages:     rawJSON(attrs[attrOutputMessages]),
		ToolDefinitions:    rawJSON(attrs[attrToolDefinitions]),
		SystemInstructions: rawJSON(attrs[attrSystemInstructions]),
	}
	if out.InputMessages == nil && out.OutputMessages == nil && out.ToolDefinitions == nil && out.SystemInstructions == nil {
		return nil
	}
	return marshalBounded(out, maxBytes)
}

// rawJSON returns the attribute string as a json.RawMessage when it is valid
// JSON, or nil otherwise so a malformed value omits rather than corrupting the
// envelope.
func rawJSON(s string) json.RawMessage {
	if s == "" || !json.Valid([]byte(s)) {
		return nil
	}
	return json.RawMessage(s)
}

// captureContentEvents is the legacy fallback: scan the span's gen_ai.* events
// into a compact JSON array. Retained for producers that emit content as span
// events rather than attributes.
func captureContentEvents(span *tracepb.Span, maxBytes int) []byte {
	type contentEvent struct {
		Name       string            `json:"name"`
		Attributes map[string]string `json:"attributes,omitempty"`
	}
	var events []contentEvent
	for _, e := range span.GetEvents() {
		if !strings.HasPrefix(e.GetName(), "gen_ai.") {
			continue
		}
		attrs := make(map[string]string, len(e.GetAttributes()))
		for _, kv := range e.GetAttributes() {
			if s := anyValueString(kv.GetValue()); s != "" {
				attrs[kv.GetKey()] = s
			}
		}
		events = append(events, contentEvent{Name: e.GetName(), Attributes: attrs})
	}
	if len(events) == 0 {
		return nil
	}
	return marshalBounded(events, maxBytes)
}

// marshalBounded marshals v, returning a {"truncated":...} marker in place of
// the payload when it exceeds maxBytes, and nil on a marshal error. A maxBytes
// of 0 or less disables the cap, so the full payload is always returned.
func marshalBounded(v any, maxBytes int) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	if maxBytes > 0 && len(b) > maxBytes {
		marker, _ := json.Marshal(map[string]any{"truncated": true, "original_bytes": len(b)})
		return marker
	}
	return b
}

func anyValueString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10)
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(x.DoubleValue, 'g', -1, 64)
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue)
	default:
		return ""
	}
}
