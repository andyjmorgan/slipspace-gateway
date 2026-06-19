package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	otellog "go.opentelemetry.io/otel/log"
	logglobal "go.opentelemetry.io/otel/log/global"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	oteltrace "go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Supported OTLP transport identifiers.
const (
	OTLPProtocolGRPC         = "grpc"
	OTLPProtocolHTTPProtobuf = "http/protobuf"
)

// EnvDeploymentEnvironment names the environment variable consulted to
// populate the OTel deployment.environment resource attribute. We read
// it once at startup; production deployments set it via downward API or
// Helm values.
const EnvDeploymentEnvironment = "SLIPSPACE_ENV"

// BuildInfo identifies the running binary in resource attributes and
// log enrichment.
type BuildInfo struct {
	// Service is the OTel service.name resource attribute and the
	// "service" slog field. Typically the binary name, e.g. "gateway".
	Service string

	// Version is the OTel service.version resource attribute and the
	// "version" slog field. Sourced from internal/version.Version.
	Version string
}

// Config carries the observability inputs Setup needs. Empty Prometheus
// or OTLP fields disable the respective exporter; LogFormat / LogLevel
// must already be the validated, normalized values from ServerEnv.
type Config struct {
	// PrometheusBind is the host:port for the /metrics scrape listener.
	// Empty disables the Prometheus exporter and PromHandler is nil.
	PrometheusBind string

	// OTLPEndpoint is the OTLP exporter target. Empty disables OTLP.
	OTLPEndpoint string

	// OTLPProtocol selects the OTLP transport ("grpc" or "http/protobuf").
	// Only consulted when OTLPEndpoint is non-empty.
	OTLPProtocol string

	// LogFormat selects the slog handler ("json" or "text").
	LogFormat string

	// LogLevel is the minimum slog level ("debug"/"info"/"warn"/"error").
	LogLevel string

	// SnapshotInterval overrides the snapshotter's sample cadence.
	// Zero (the default) falls back to the snapshotter's built-in
	// default (5 minutes). E2E harnesses drop this to ~200ms so the
	// dashboard reflects real traffic in test wall-clock.
	SnapshotInterval time.Duration
}

// Provider bundles the live telemetry pipeline. Shutdown must be invoked
// during graceful termination so the OTLP exporter can flush its
// in-memory queue.
type Provider struct {
	// Meters is the pre-built instrument bundle. Always non-nil.
	Meters *Meters

	// MeterProvider is the underlying SDK provider. The same one is
	// registered globally via otel.SetMeterProvider so any code that
	// reaches for the global meter still routes here.
	MeterProvider metric.MeterProvider

	// TracerProvider is the span pipeline. Always non-nil: when OTLP is
	// disabled it is a no-op provider so callers can start spans
	// unconditionally without nil-checking. When OTLP is enabled it is
	// an SDK provider registered globally via otel.SetTracerProvider and
	// drained on Shutdown.
	TracerProvider oteltrace.TracerProvider

	// LoggerProvider is the OTel logs pipeline carrying the GenAI events
	// (gen_ai.client.inference.operation.details and
	// gen_ai.client.operation.exception). Always non-nil: a no-op provider
	// when OTLP is disabled so callers emit unconditionally; an SDK provider
	// registered globally and drained on Shutdown when OTLP is enabled.
	// Distinct from Logger (slog) — this is the OTel log signal, not the
	// gateway's own structured request logging.
	LoggerProvider otellog.LoggerProvider

	// Logger is the service-enriched root logger. Per-request loggers
	// are derived from this and stashed on context.Context.
	Logger *slog.Logger

	// PromHandler serves the Prometheus exposition format when
	// Prometheus scrape is enabled. It is nil otherwise — callers should
	// branch on the field rather than calling it unconditionally.
	PromHandler http.Handler

	// Snapshotter is the in-process windowed snapshot store fed by a
	// ManualReader attached to the SDK MeterProvider. Always non-nil —
	// the admin console reads from it to compute dashboard windows.
	// Caller must invoke Snapshotter.Start(ctx) once at startup.
	Snapshotter *Snapshotter

	// Shutdown gracefully terminates the telemetry pipeline: it ForceFlushes
	// every SDK provider (meter, tracer, logger) so buffered spans, logs, and
	// metric points hit the wire, then shuts each down — bounded by ctx.
	// Safe to call multiple times: the inner once.Do makes subsequent
	// invocations no-ops that return the same joined error as the first call.
	Shutdown func(ctx context.Context) error
}

// Tracer returns the gateway-scoped tracer from the provider's
// TracerProvider. TracerProvider is always non-nil, so this is safe to
// call whether or not OTLP tracing is enabled — when disabled the
// returned tracer is a no-op that never records spans.
func (p *Provider) Tracer() oteltrace.Tracer {
	return p.TracerProvider.Tracer(TracerName)
}

// EventLogger returns the gateway-scoped OTel logger for emitting GenAI
// events. LoggerProvider is always non-nil, so this is safe whether or not
// OTLP is enabled — when disabled the returned logger is a no-op that
// records nothing.
func (p *Provider) EventLogger() otellog.Logger {
	return p.LoggerProvider.Logger(LoggerName)
}

// Setup constructs the telemetry stack from the supplied configuration.
// It is safe to call with both, either, or neither external exporter
// enabled — the SDK MeterProvider is always built with at least an
// in-process ManualReader so the snapshotter (and therefore the admin
// console's dashboard) works without depending on Prometheus or OTLP.
//
// Setup also calls otel.SetMeterProvider on the returned provider so any
// stray code that reaches for the global meter still routes to the same
// pipeline.
func Setup(ctx context.Context, cfg Config, build BuildInfo) (*Provider, error) {
	logger, err := NewLogger(cfg.LogFormat, cfg.LogLevel)
	if err != nil {
		return nil, err
	}
	logger = EnrichLogger(logger, build.Service, build.Version)

	promEnabled := cfg.PrometheusBind != ""
	otlpEnabled := cfg.OTLPEndpoint != ""

	res, err := buildResource(ctx, build)
	if err != nil {
		return nil, err
	}

	readers := make([]sdkmetric.Reader, 0, 3)

	// flushFns run before shutdownFns inside the composed Shutdown: every
	// pipeline is ForceFlushed first so buffered spans/logs/metric points hit
	// the wire even if a later provider's Shutdown errors or times out. The
	// June 2026 binary-swap incident lost ~90 buffered spans on exit; flush
	// ordering is now an explicit, tested contract (see flushThenShutdown).
	flushFns := make([]func(context.Context) error, 0, 3)
	shutdownFns := make([]func(context.Context) error, 0, 4)

	var promHandler http.Handler

	if promEnabled {
		reg := prometheus.NewRegistry()
		// Register the Go runtime + process collectors alongside the
		// OTel-bridged meters so /metrics carries go_memstats_*,
		// go_goroutines, process_resident_memory_bytes,
		// process_cpu_seconds_total, process_open_fds, and friends.
		// These are the diagnostics the load-test reports lean on; the
		// gateway's own gateway_* counters never replace runtime
		// telemetry.
		reg.MustRegister(
			collectors.NewGoCollector(),
			collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		)
		exp, perr := otelprom.New(otelprom.WithRegisterer(reg))
		if perr != nil {
			return nil, fmt.Errorf("observability: prometheus exporter: %w", perr)
		}
		readers = append(readers, exp)
		promHandler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})
	}

	if otlpEnabled {
		exp, oerr := newOTLPReader(ctx, cfg.OTLPEndpoint, cfg.OTLPProtocol)
		if oerr != nil {
			return nil, oerr
		}
		// No separate exp.Shutdown registration: the reader is owned by the
		// MeterProvider, and mp.Shutdown (below) shuts down every attached
		// reader. Registering both made the second call return
		// ErrReaderShutdown, which surfaced as a false "telemetry flush on
		// shutdown failed" WARN on every graceful exit once shutdown errors
		// stopped being swallowed (first seen on the 2026-06-10 prod roll).
		readers = append(readers, exp)
	}

	// Always attach a ManualReader. The snapshotter (and the admin
	// dashboard handler downstream of it) pulls from this; it does
	// nothing externally without a Collect call so the cost when neither
	// prom nor otlp is configured is essentially zero.
	manual := sdkmetric.NewManualReader()
	readers = append(readers, manual)

	opts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	for _, r := range readers {
		opts = append(opts, sdkmetric.WithReader(r))
	}
	mp := sdkmetric.NewMeterProvider(opts...)
	otel.SetMeterProvider(mp)
	// ForceFlush drives every reader (periodic OTLP included) through a final
	// collect+export before any Shutdown runs.
	flushFns = append(flushFns, mp.ForceFlush)

	// Install the W3C trace-context + baggage propagator so the data plane
	// can extract an inbound traceparent and nest its spans under a caller's
	// distributed trace. Harmless when tracing is disabled (the no-op tracer
	// records nothing); set unconditionally so propagation works the moment
	// OTLP is enabled.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	shutdownFns = append(shutdownFns, mp.Shutdown)

	meters, err := NewMeters(mp.Meter(MeterName))
	if err != nil {
		_ = mp.Shutdown(ctx)
		return nil, err
	}

	snap, err := NewSnapshotter(SnapshotterOptions{
		Reader:   manual,
		Interval: cfg.SnapshotInterval,
	})
	if err != nil {
		_ = mp.Shutdown(ctx)
		return nil, fmt.Errorf("observability: snapshotter: %w", err)
	}

	// TracerProvider is always non-nil. With OTLP disabled it is a no-op
	// so callers start spans unconditionally; with OTLP enabled it is an
	// SDK provider registered globally and drained on Shutdown.
	var tp oteltrace.TracerProvider = tracenoop.NewTracerProvider()
	if otlpEnabled {
		spanExp, terr := newOTLPSpanExporter(ctx, cfg.OTLPEndpoint, cfg.OTLPProtocol)
		if terr != nil {
			_ = mp.Shutdown(ctx)
			return nil, terr
		}
		// TODO(v1.5+): per-configuration sampling. AlwaysSample for now —
		// the request path attaches no sampler-influencing attributes yet.
		sdkTP := sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(spanExp),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		)
		otel.SetTracerProvider(sdkTP)
		flushFns = append(flushFns, sdkTP.ForceFlush)
		shutdownFns = append(shutdownFns, sdkTP.Shutdown)
		tp = sdkTP
	}

	// LoggerProvider mirrors TracerProvider: a no-op provider when OTLP is
	// disabled, an SDK provider with an OTLP exporter + batch processor when
	// enabled. Events (operation details, exceptions) emit out of band via
	// the batch processor, never on the request path.
	var lp otellog.LoggerProvider = lognoop.NewLoggerProvider()
	if otlpEnabled {
		logExp, lerr := newOTLPLogExporter(ctx, cfg.OTLPEndpoint, cfg.OTLPProtocol)
		if lerr != nil {
			_ = flushThenShutdown(nil, shutdownFns)(ctx)
			return nil, lerr
		}
		sdkLP := sdklog.NewLoggerProvider(
			sdklog.WithResource(res),
			sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
		)
		logglobal.SetLoggerProvider(sdkLP)
		flushFns = append(flushFns, sdkLP.ForceFlush)
		shutdownFns = append(shutdownFns, sdkLP.Shutdown)
		lp = sdkLP
	}

	// Surface SDK-internal errors — overwhelmingly OTLP export failures from
	// the periodic reader and batch processors — as a counter + warn log.
	// Without this, a dead OTLP endpoint silently drops every span and metric
	// point (the default handler writes to stderr once). The counter rides
	// the Prometheus reader too, so the failure is visible even when the
	// OTLP path itself is what's broken.
	otel.SetErrorHandler(exportErrorHandler(meters.OTelExportFailuresTotal, logger)) //nolint:contextcheck // the handler outlives Setup's ctx; it runs from SDK exporter goroutines with no caller context

	return &Provider{
		Meters:         meters,
		MeterProvider:  mp,
		TracerProvider: tp,
		LoggerProvider: lp,
		Logger:         logger,
		PromHandler:    promHandler,
		Snapshotter:    snap,
		Shutdown:       flushThenShutdown(flushFns, shutdownFns),
	}, nil
}

// exportErrorHandler builds the global OTel error handler: bump the export
// failures counter and warn. Kept tiny and allocation-light — the SDK may
// invoke it from exporter goroutines on every failed batch.
func exportErrorHandler(counter metric.Int64Counter, logger *slog.Logger) otel.ErrorHandler {
	return otel.ErrorHandlerFunc(func(err error) {
		counter.Add(context.Background(), 1)
		logger.Warn("otel sdk error (likely OTLP export failure)", "err", err.Error())
	})
}

func buildResource(ctx context.Context, build BuildInfo) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(build.Service),
		semconv.ServiceVersion(build.Version),
	}
	if env := strings.TrimSpace(os.Getenv(EnvDeploymentEnvironment)); env != "" {
		attrs = append(attrs, attribute.String("deployment.environment", env))
	}
	res, err := resource.New(ctx,
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: build resource: %w", err)
	}
	return res, nil
}

func newOTLPReader(ctx context.Context, endpoint, protocol string) (*sdkmetric.PeriodicReader, error) {
	proto := strings.ToLower(strings.TrimSpace(protocol))
	if proto == "" {
		proto = OTLPProtocolGRPC
	}

	var (
		exp sdkmetric.Exporter
		err error
	)
	switch proto {
	case OTLPProtocolGRPC:
		opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithInsecure(), otlpmetricgrpc.WithTemporalitySelector(deltaTemporality)}
		if endpoint != "" {
			opts = append(opts, otlpmetricgrpc.WithEndpoint(endpoint))
		}
		exp, err = otlpmetricgrpc.New(ctx, opts...)
	case OTLPProtocolHTTPProtobuf, "http":
		opts := []otlpmetrichttp.Option{otlpmetrichttp.WithInsecure(), otlpmetrichttp.WithTemporalitySelector(deltaTemporality)}
		if endpoint != "" {
			opts = append(opts, otlpmetrichttp.WithEndpoint(endpoint))
		}
		exp, err = otlpmetrichttp.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("observability: unsupported OTLP protocol %q", protocol)
	}
	if err != nil {
		return nil, fmt.Errorf("observability: create OTLP exporter: %w", err)
	}
	return sdkmetric.NewPeriodicReader(exp), nil
}

// deltaTemporality selects delta temporality for the OTLP metric export so the
// Arbiter can SUM a window of metric_points to an exact count
// (cumulative would carry running totals that double-count when summed). Sums
// and histograms go delta; up-down counters (gateway.active_requests) and gauges
// (gateway.cb.state) MUST stay cumulative — delta is undefined for them. Only
// the OTLP reader uses this; the Prometheus reader stays cumulative for /metrics.
func deltaTemporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	switch kind {
	case sdkmetric.InstrumentKindCounter, sdkmetric.InstrumentKindHistogram, sdkmetric.InstrumentKindObservableCounter:
		return metricdata.DeltaTemporality
	default:
		return metricdata.CumulativeTemporality
	}
}

// flushThenShutdown composes the provider's graceful-termination contract
// into one idempotent callable: every flush callback runs first (ForceFlush
// pushes buffered spans, logs, and metric points to the wire), then every
// shutdown callback, all bounded by the caller's ctx. A flush failure does
// not skip the remaining flushes or the shutdowns — each error is collected
// and joined. Subsequent invocations short-circuit to the first call's result
// so shutdown handlers can re-run without double-flushing the SDK.
func flushThenShutdown(flushFns, shutdownFns []func(context.Context) error) func(context.Context) error {
	var (
		once sync.Once
		err  error
	)
	return func(ctx context.Context) error {
		once.Do(func() {
			var errs []error
			for _, fn := range flushFns {
				if e := fn(ctx); e != nil {
					errs = append(errs, fmt.Errorf("observability: flush: %w", e))
				}
			}
			for _, fn := range shutdownFns {
				if e := fn(ctx); e != nil {
					errs = append(errs, fmt.Errorf("observability: shutdown: %w", e))
				}
			}
			err = errors.Join(errs...)
		})
		return err
	}
}
