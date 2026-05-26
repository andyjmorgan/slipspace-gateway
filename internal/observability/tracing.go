package observability

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TracerName is the OpenTelemetry tracer scope under which all gateway
// spans are recorded. Mirrors MeterName so spans and metrics emitted by
// the gateway share a single instrumentation-scope identity.
const TracerName = "sluice-gateway"

// newOTLPSpanExporter builds an OTLP trace exporter for the given
// endpoint, selecting the transport from protocol. It mirrors
// newOTLPReader's shape: empty protocol defaults to gRPC, both
// transports run WithInsecure, and an unknown protocol is a hard error.
//
// The returned exporter is wrapped in a BatchSpanProcessor by the
// caller; on its own it performs no I/O until spans are pushed through.
func newOTLPSpanExporter(ctx context.Context, endpoint, protocol string) (sdktrace.SpanExporter, error) {
	proto := strings.ToLower(strings.TrimSpace(protocol))
	if proto == "" {
		proto = OTLPProtocolGRPC
	}

	var (
		exp sdktrace.SpanExporter
		err error
	)
	switch proto {
	case OTLPProtocolGRPC:
		opts := []otlptracegrpc.Option{otlptracegrpc.WithInsecure()}
		if endpoint != "" {
			opts = append(opts, otlptracegrpc.WithEndpoint(endpoint))
		}
		exp, err = otlptracegrpc.New(ctx, opts...)
	case OTLPProtocolHTTPProtobuf, "http":
		opts := []otlptracehttp.Option{otlptracehttp.WithInsecure()}
		if endpoint != "" {
			opts = append(opts, otlptracehttp.WithEndpoint(endpoint))
		}
		exp, err = otlptracehttp.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("observability: unsupported OTLP protocol %q", protocol)
	}
	if err != nil {
		return nil, fmt.Errorf("observability: create OTLP trace exporter: %w", err)
	}
	return exp, nil
}
