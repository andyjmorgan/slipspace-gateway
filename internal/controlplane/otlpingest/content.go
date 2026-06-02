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

// captureContent extracts bounded gen_ai content from a span's events — the
// OTel GenAI semconv emits prompt/completion as gen_ai.* span events — into a
// compact JSON array. Returns nil when the span carries no gen_ai content, and
// a small {"truncated":...} marker when the content exceeds maxContentBytes.
func captureContent(span *tracepb.Span) []byte {
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
	b, err := json.Marshal(events)
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
