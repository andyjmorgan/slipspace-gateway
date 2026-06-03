package otlpingest

import (
	"encoding/json"
	"strconv"
	"strings"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// maxContentBytes bounds the captured gen_ai content per request. Content is a
// best-effort console aid, not the audit record (that is the spool/request_
// bodies channel), so it is capped hard and replaced with a marker when it
// would exceed the limit.
const maxContentBytes = 16 * 1024

// Content span-attribute keys the gateway's reporter writes (see
// cmd/gateway/reporter.go::appendSpanContent and the consts in
// internal/observability/genai.go). Each attribute's value is itself a JSON
// string: input/output messages are [{role, parts:[...]}], system_instructions
// is a bare parts array, tool definitions are [{type, name, description,
// parameters}]. The gateway emits these on the span (not as span events), so
// the attribute path below is the primary capture; the event scan stays as a
// fallback for any producer that emits gen_ai.* span events instead.
const (
	attrInputMessages      = "gen_ai.input.messages"
	attrOutputMessages     = "gen_ai.output.messages"
	attrToolDefinitions    = "gen_ai.tool.definitions"
	attrSystemInstructions = "gen_ai.system_instructions"
)

// captureContent extracts bounded gen_ai content from a span. The gateway's
// reporter writes the prompt/response/tool content as gen_ai.* span attributes
// whose values are JSON strings; this is the primary path, producing a
// structured object {input_messages, output_messages, tool_definitions,
// system_instructions}. When no content attributes are present it falls back to
// the older gen_ai.* span-event scan, producing the legacy event array. Returns
// nil when the span carries no gen_ai content, and a small {"truncated":...}
// marker when the content exceeds maxContentBytes.
func captureContent(span *tracepb.Span) []byte {
	if b := captureContentAttrs(span); b != nil {
		return b
	}
	return captureContentEvents(span)
}

// captureContentAttrs builds the structured content object from the span's
// content attributes. Each attribute value is a JSON string emitted by the
// gateway; it is re-embedded as json.RawMessage so the wire payload is a real
// nested object rather than a doubly-escaped string. Attributes whose value is
// not valid JSON are skipped. Returns nil when no content attribute is present.
func captureContentAttrs(span *tracepb.Span) []byte {
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
	return marshalBounded(out)
}

// rawJSON returns the attribute string as a json.RawMessage when it is valid
// JSON, or nil otherwise so a malformed value omits rather than corrupting the
// envelope.
func rawJSON(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	if !json.Valid([]byte(s)) {
		return nil
	}
	return json.RawMessage(s)
}

// captureContentEvents is the legacy fallback: scan the span's gen_ai.* events
// into a compact JSON array. Retained for producers that emit content as span
// events rather than attributes.
func captureContentEvents(span *tracepb.Span) []byte {
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
	return marshalBounded(events)
}

// marshalBounded marshals v, returning a {"truncated":...} marker in place of
// the payload when it exceeds maxContentBytes, and nil on a marshal error.
func marshalBounded(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	if len(b) > maxContentBytes {
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
