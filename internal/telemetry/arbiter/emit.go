package arbiter

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/config"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// tracerName is the instrumentation scope for Arbiter's enriched spans.
const tracerName = "slipspace-arbiter"

// AttrEnriched flags a span Arbiter emitted, so Arbiter's own OTLP receiver
// declines to re-admit it (ADR-002 loop prevention). Exported so the receiver
// can reference the same key.
const AttrEnriched = "slipspace.enriched"

// verdictEmitter re-emits a reduced verdict as an enriched OTLP span so findings
// travel back over OTel rather than a separate stream (ADR-002). A no-op when
// no export endpoint is configured.
type verdictEmitter interface {
	EmitVerdict(ctx context.Context, correlationID string, v store.Verdict)
	shutdown(ctx context.Context)
}

// noopEmitter is the default when enriched export is unconfigured.
type noopEmitter struct{}

func (noopEmitter) EmitVerdict(context.Context, string, store.Verdict) {}
func (noopEmitter) shutdown(context.Context)                           {}

// otelEmitter emits enriched verdict spans through an OTLP trace exporter.
type otelEmitter struct {
	tp     *sdktrace.TracerProvider
	tracer trace.Tracer
}

func newOTelEmitter(tp *sdktrace.TracerProvider) *otelEmitter {
	return &otelEmitter{tp: tp, tracer: tp.Tracer(tracerName)}
}

// EmitVerdict starts and ends a short span carrying the security verdict plus
// the AttrEnriched flag, referencing the original request by correlation_id.
// Batched and non-blocking — a slow/absent collector never stalls the reduce
// loop.
func (e *otelEmitter) EmitVerdict(ctx context.Context, correlationID string, v store.Verdict) {
	_, span := e.tracer.Start(ctx, "arbiter.verdict")
	span.SetAttributes(
		attribute.String("sluice.correlation_id", correlationID),
		attribute.Bool(AttrEnriched, true),
		attribute.String("slipspace.security.verdict", v.State),
		attribute.Float64("slipspace.security.max_score", float64(v.MaxScore)),
		attribute.String("slipspace.security.top_category", v.TopCategory),
		attribute.Int("slipspace.security.finding_count", v.FindingCount),
		attribute.StringSlice("slipspace.security.inconclusive", v.Inconclusive),
	)
	span.End()
}

func (e *otelEmitter) shutdown(ctx context.Context) { _ = e.tp.Shutdown(ctx) }

// newEmitter builds a verdict emitter from config. An empty endpoint yields a
// no-op emitter (enriched export disabled — the default).
func newEmitter(ctx context.Context, cfg config.EnrichedExport) (verdictEmitter, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return noopEmitter{}, nil
	}
	exp, err := newSpanExporter(ctx, cfg.Endpoint, cfg.Protocol)
	if err != nil {
		return nil, err
	}
	return newOTelEmitter(sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))), nil
}

// newSpanExporter builds the OTLP trace exporter for the configured transport.
// Empty protocol defaults to gRPC; both run insecure (the collector is reached
// over the cluster network, same posture as the gateway's exporter).
func newSpanExporter(ctx context.Context, endpoint, protocol string) (sdktrace.SpanExporter, error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "", "grpc":
		return otlptracegrpc.New(ctx, otlptracegrpc.WithInsecure(), otlptracegrpc.WithEndpoint(endpoint))
	case "http":
		return otlptracehttp.New(ctx, otlptracehttp.WithInsecure(), otlptracehttp.WithEndpoint(endpoint))
	default:
		return nil, fmt.Errorf("arbiter: unsupported enriched_export protocol %q", protocol)
	}
}
