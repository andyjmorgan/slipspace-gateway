package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// cpAuthHeader builds the gRPC metadata the control plane's
// TokenAuthInterceptor expects: an "authorization: Bearer <token>" pair.
// An empty token yields no header — the CP runs without token auth only on a
// trusted network, and the gateway must not invent a bearer in that case.
func cpAuthHeader(token string) map[string]string {
	if token == "" {
		return nil
	}
	return map[string]string{"authorization": "Bearer " + token}
}

// newCPMetricReader builds a periodic metric reader that pushes to the control
// plane's gRPC OTLP receiver (the same :8485 server that serves the fleet
// channel). Always gRPC and insecure — the fleet channel is plaintext until M6
// adds TLS — with the bootstrap token carried in gRPC metadata so the CP's
// interceptor admits the export.
func newCPMetricReader(ctx context.Context, endpoint, token string) (*sdkmetric.PeriodicReader, error) {
	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(endpoint),
		otlpmetricgrpc.WithInsecure(),
	}
	if h := cpAuthHeader(token); h != nil {
		opts = append(opts, otlpmetricgrpc.WithHeaders(h))
	}
	exp, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("observability: create control-plane metric exporter: %w", err)
	}
	return sdkmetric.NewPeriodicReader(exp), nil
}

// newCPSpanExporter builds an OTLP trace exporter targeting the control plane's
// gRPC receiver, carrying the bootstrap token in gRPC metadata. The caller
// wraps it in a BatchSpanProcessor; on its own it performs no I/O until spans
// are pushed through.
func newCPSpanExporter(ctx context.Context, endpoint, token string) (sdktrace.SpanExporter, error) {
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	}
	if h := cpAuthHeader(token); h != nil {
		opts = append(opts, otlptracegrpc.WithHeaders(h))
	}
	exp, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("observability: create control-plane trace exporter: %w", err)
	}
	return exp, nil
}
