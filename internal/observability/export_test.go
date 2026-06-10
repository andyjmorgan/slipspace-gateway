package observability

import (
	"context"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// NewOTLPSpanExporterForTest exposes newOTLPSpanExporter to the external
// observability_test package so the transport switch and the
// unsupported-protocol error path can be exercised in isolation, the
// same way newOTLPReader is covered through Setup.
func NewOTLPSpanExporterForTest(ctx context.Context, endpoint, protocol string) (sdktrace.SpanExporter, error) {
	return newOTLPSpanExporter(ctx, endpoint, protocol)
}

// NewOTLPLogExporterForTest exposes newOTLPLogExporter to the external
// observability_test package, mirroring the span exporter accessor.
func NewOTLPLogExporterForTest(ctx context.Context, endpoint, protocol string) (sdklog.Exporter, error) {
	return newOTLPLogExporter(ctx, endpoint, protocol)
}

// FlushThenShutdown exposes flushThenShutdown so the flush-before-shutdown
// ordering contract (the graceful-termination fix for the June 2026 span-loss
// incident) is covered by an explicit test rather than inferred from Setup.
var FlushThenShutdown = flushThenShutdown

// ExportErrorHandler exposes exportErrorHandler so the OTLP-export-failure
// counter wiring can be exercised without a failing exporter.
var ExportErrorHandler = exportErrorHandler
