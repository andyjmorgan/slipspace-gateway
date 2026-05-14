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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
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
const EnvDeploymentEnvironment = "SLUICE_ENV"

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
}

// Provider bundles the live telemetry pipeline. Shutdown must be invoked
// during graceful termination so the OTLP exporter can flush its
// in-memory queue.
type Provider struct {
	// Meters is the pre-built instrument bundle. Always non-nil — Setup
	// installs the no-op bundle when no exporter is enabled.
	Meters *Meters

	// MeterProvider is the underlying provider. The same one is
	// registered globally via otel.SetMeterProvider so any code that
	// reaches for the global meter still routes here. May be a
	// sdkmetric.MeterProvider with one or two Readers (Prometheus and/or
	// OTLP) or a noop.MeterProvider in the no-exporter case.
	MeterProvider metric.MeterProvider

	// Logger is the service-enriched root logger. Per-request loggers
	// are derived from this and stashed on context.Context.
	Logger *slog.Logger

	// PromHandler serves the Prometheus exposition format when
	// Prometheus scrape is enabled. It is nil otherwise — callers should
	// branch on the field rather than calling it unconditionally.
	PromHandler http.Handler

	// Shutdown flushes and shuts down the OTLP exporter and the
	// MeterProvider. Safe to call multiple times: the inner once.Do
	// makes subsequent invocations no-ops that return the same joined
	// error as the first call.
	Shutdown func(ctx context.Context) error
}

// Setup constructs the telemetry stack from the supplied configuration.
// It is safe to call with both, either, or neither exporter enabled:
// when neither is on it installs a no-op MeterProvider so middleware can
// emit metrics without nil checks.
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

	if !promEnabled && !otlpEnabled {
		return newNoopProvider(logger)
	}

	res, err := buildResource(ctx, build)
	if err != nil {
		return nil, err
	}

	readers := make([]sdkmetric.Reader, 0, 2)

	shutdownFns := make([]func(context.Context) error, 0, 2)

	var promHandler http.Handler

	if promEnabled {
		reg := prometheus.NewRegistry()
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
		readers = append(readers, exp)
		shutdownFns = append(shutdownFns, exp.Shutdown)
	}

	opts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	for _, r := range readers {
		opts = append(opts, sdkmetric.WithReader(r))
	}
	mp := sdkmetric.NewMeterProvider(opts...)
	otel.SetMeterProvider(mp)
	shutdownFns = append(shutdownFns, mp.Shutdown)

	meters, err := NewMeters(mp.Meter(MeterName))
	if err != nil {
		_ = mp.Shutdown(ctx)
		return nil, err
	}

	return &Provider{
		Meters:        meters,
		MeterProvider: mp,
		Logger:        logger,
		PromHandler:   promHandler,
		Shutdown:      idempotentShutdown(shutdownFns),
	}, nil
}

func newNoopProvider(logger *slog.Logger) (*Provider, error) {
	mp := noop.NewMeterProvider()
	otel.SetMeterProvider(mp)
	meters, err := NewMeters(mp.Meter(MeterName))
	if err != nil {
		return nil, err
	}
	return &Provider{
		Meters:        meters,
		MeterProvider: mp,
		Logger:        logger,
		Shutdown:      idempotentShutdown(nil),
	}, nil
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
		opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithInsecure()}
		if endpoint != "" {
			opts = append(opts, otlpmetricgrpc.WithEndpoint(endpoint))
		}
		exp, err = otlpmetricgrpc.New(ctx, opts...)
	case OTLPProtocolHTTPProtobuf, "http":
		opts := []otlpmetrichttp.Option{otlpmetrichttp.WithInsecure()}
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

// idempotentShutdown collapses a slice of shutdown callbacks into one
// callable that may be invoked many times. Subsequent invocations
// short-circuit to nil so graceful shutdown handlers can re-run without
// double-flushing the SDK.
func idempotentShutdown(fns []func(context.Context) error) func(context.Context) error {
	var (
		once sync.Once
		err  error
	)
	return func(ctx context.Context) error {
		once.Do(func() {
			var errs []error
			for _, fn := range fns {
				if e := fn(ctx); e != nil {
					errs = append(errs, e)
				}
			}
			err = errors.Join(errs...)
		})
		return err
	}
}
