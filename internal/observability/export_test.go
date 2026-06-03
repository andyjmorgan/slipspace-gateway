package observability

import (
	"context"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// BuildResourceForTest exposes buildResource so the external
// observability_test package can assert which attributes (notably
// sluice.gateway_id) land on the OTel resource.
func BuildResourceForTest(ctx context.Context, build BuildInfo, gatewayID string) (*resource.Resource, error) {
	return buildResource(ctx, build, gatewayID)
}

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

// CPAuthHeaderForTest exposes cpAuthHeader so the bearer-vs-empty metadata
// mapping can be asserted in isolation.
func CPAuthHeaderForTest(token string) map[string]string {
	return cpAuthHeader(token)
}

// NewCPSpanExporterForTest exposes newCPSpanExporter so its success path is
// covered the same way the external span exporter is.
func NewCPSpanExporterForTest(ctx context.Context, endpoint, token string) (sdktrace.SpanExporter, error) {
	return newCPSpanExporter(ctx, endpoint, token)
}
