package observability

import (
	"context"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// NewOTLPSpanExporterForTest exposes newOTLPSpanExporter to the external
// observability_test package so the transport switch and the
// unsupported-protocol error path can be exercised in isolation, the
// same way newOTLPReader is covered through Setup.
func NewOTLPSpanExporterForTest(ctx context.Context, endpoint, protocol string) (sdktrace.SpanExporter, error) {
	return newOTLPSpanExporter(ctx, endpoint, protocol)
}
