// Package otlpingest maps OTLP telemetry the gateways push into the control
// plane's observability store. This file is the pure extraction layer: an OTLP
// span (plus its resource attributes) becomes a configdb.RequestEvent. The gRPC
// OTLP receiver that drives it — and writes the events + signs receipts — wires
// this in.
//
// Attribute conventions: OTel GenAI semconv (gen_ai.*) for model/provider/usage,
// plus sluice.* for the post-rule fleet labels (gateway, configuration, backend,
// protocol) and the correlation id that joins an event to its body + receipt.
package otlpingest

import (
	"strconv"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

const (
	attrCorrelationID = "sluice.correlation_id"
	attrGatewayID     = "sluice.gateway_id"
	attrConfiguration = "sluice.configuration"
	attrBackend       = "sluice.backend"
	attrProtocol      = "sluice.protocol"
	attrModel         = "gen_ai.request.model"
	attrProvider      = "gen_ai.provider.name"
	attrInputTokens   = "gen_ai.usage.input_tokens"  //nolint:gosec // OTLP attribute key, not a credential
	attrOutputTokens  = "gen_ai.usage.output_tokens" //nolint:gosec // OTLP attribute key, not a credential
	attrStatusCode    = "http.response.status_code"
)

// EventFromSpan extracts a request event from a span and its resource
// attributes. Resource attributes are merged under the span's own (span wins).
// Returns ok=false when the span is not a sluice request event — i.e. it
// carries no correlation id, the key everything joins on.
func EventFromSpan(resourceAttrs []*commonpb.KeyValue, span *tracepb.Span) (configdb.RequestEvent, bool) {
	if span == nil {
		return configdb.RequestEvent{}, false
	}
	attrs := mergeAttrs(resourceAttrs, span.GetAttributes())

	corr := strAttr(attrs, attrCorrelationID)
	if corr == "" {
		return configdb.RequestEvent{}, false
	}

	backend := strAttr(attrs, attrBackend)
	if backend == "" {
		backend = strAttr(attrs, attrProvider)
	}

	return configdb.RequestEvent{
		CorrelationID: corr,
		GatewayID:     strAttr(attrs, attrGatewayID),
		Configuration: strAttr(attrs, attrConfiguration),
		Backend:       backend,
		Model:         strAttr(attrs, attrModel),
		Protocol:      strAttr(attrs, attrProtocol),
		StatusCode:    int(intAttr(attrs, attrStatusCode)),
		TokensIn:      intAttr(attrs, attrInputTokens),
		TokensOut:     intAttr(attrs, attrOutputTokens),
		LatencyMs:     spanLatencyMs(span),
		GenAIContent:  captureContent(span),
	}, true
}

func mergeAttrs(resource, span []*commonpb.KeyValue) map[string]*commonpb.AnyValue {
	out := make(map[string]*commonpb.AnyValue, len(resource)+len(span))
	for _, kv := range resource {
		if kv != nil {
			out[kv.GetKey()] = kv.GetValue()
		}
	}
	for _, kv := range span {
		if kv != nil {
			out[kv.GetKey()] = kv.GetValue() // span attributes win over resource
		}
	}
	return out
}

func strAttr(attrs map[string]*commonpb.AnyValue, key string) string {
	v := attrs[key]
	if v == nil {
		return ""
	}
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10)
	default:
		return ""
	}
}

func intAttr(attrs map[string]*commonpb.AnyValue, key string) int64 {
	v := attrs[key]
	if v == nil {
		return 0
	}
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_IntValue:
		return x.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return int64(x.DoubleValue)
	case *commonpb.AnyValue_StringValue:
		n, _ := strconv.ParseInt(x.StringValue, 10, 64)
		return n
	default:
		return 0
	}
}

func spanLatencyMs(span *tracepb.Span) int64 {
	start, end := span.GetStartTimeUnixNano(), span.GetEndTimeUnixNano()
	if end <= start {
		return 0
	}
	return int64((end - start) / 1_000_000) //nolint:gosec // a span duration in ms never overflows int64
}
