package ingest

import (
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// EventFromSpan must decline a span Arbiter itself emitted (flagged
// slipspace.enriched) so an enriched span routed back into the collector cannot
// loop into re-ingestion (ADR-002), even though it carries a correlation id.
func TestEventFromSpan_DeclinesEnriched(t *testing.T) {
	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		strKV(attrCorrelationID, "corr-enriched"),
		boolKV(attrEnriched, true),
	}}
	if _, ok := EventFromSpan(nil, span, testContentCap); ok {
		t.Error("enriched span should be declined (loop prevention)")
	}
}
