package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/bus"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
	"github.com/andyjmorgan/sluice-gateway/internal/routing"
	"github.com/andyjmorgan/sluice-gateway/internal/server"
	"github.com/andyjmorgan/sluice-gateway/internal/version"
)

const (
	binaryName = "gateway"

	envConfigDir     = "SLUICE_CONFIG_DIR"
	defaultConfigDir = "/etc/sluice/"

	publisherStopTimeout = 5 * time.Second
)

func main() {
	os.Exit(mainErr())
}

func mainErr() int {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s %s\n", binaryName, version.Version)
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("gateway exited with error", "err", err)
		return 1
	}
	return 0
}

func run(ctx context.Context) error {
	dir := strings.TrimSpace(os.Getenv(envConfigDir))
	if dir == "" {
		dir = defaultConfigDir
	}

	resolved, err := config.Load(ctx, dir)
	if err != nil {
		return fmt.Errorf("gateway: load config %q: %w", dir, err)
	}

	build := observability.BuildInfo{Service: binaryName, Version: version.Version}
	obs, err := observability.Setup(ctx, resolved.Gateway.Observability, build)
	if err != nil {
		return fmt.Errorf("gateway: observability setup: %w", err)
	}
	defer shutdownObservability(obs) //nolint:contextcheck // detached on purpose; see shutdownObservability

	logger := obs.Logger

	publisher, busCleanup, err := setupBus(ctx, resolved.Gateway.Reporting, logger)
	if err != nil {
		return fmt.Errorf("gateway: bus setup: %w", err)
	}
	defer busCleanup()

	router, err := routing.New(resolved)
	if err != nil {
		return fmt.Errorf("gateway: router: %w", err)
	}

	resolver := auth.NewResolver(resolved)

	observer := newReporterObserver(publisher, logger, obs.Meters)
	forwarder := proxy.New(proxy.Options{Logger: logger, Observer: observer})

	dataPlane := buildDataPlaneHandler(router, resolver, forwarder, resolved.Providers, logger)

	root := correlationMiddleware(logger, dataPlane)

	bind := resolved.Gateway.HTTP.Bind
	if bind == "" {
		bind = "0.0.0.0:8585"
	}

	drain := time.Duration(resolved.Gateway.Shutdown.DrainTimeoutSeconds) * time.Second

	srv := server.New(server.Options{
		Bind:         bind,
		Handler:      root,
		DrainTimeout: drain,
		Logger:       logger,
	})

	if obs.PromHandler != nil {
		startPrometheus(ctx, resolved.Gateway.Observability.Prometheus.Bind, obs.PromHandler, logger)
	}

	logger.InfoContext(ctx, "gateway starting",
		"bind", bind,
		"config_dir", dir,
		"providers", len(resolved.Providers),
		"configurations", len(resolved.Configurations),
		"api_keys", len(resolved.APIKeys),
	)

	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("gateway: run: %w", err)
	}
	return nil
}

// setupBus wires the NATS publisher when reporting is enabled. A nil publisher
// is returned (with a no-op cleanup) when reporting is off or NATS is
// unreachable — the request path must never block on reporting, so a missing
// bus degrades gracefully into "events dropped" rather than aborting startup.
func setupBus(ctx context.Context, cfg contractsconfig.ReportingConfig, logger *slog.Logger) (*bus.Publisher, func(), error) {
	noop := func() {}
	if !cfg.Enabled {
		return nil, noop, nil
	}
	if strings.TrimSpace(cfg.NATS.URL) == "" {
		logger.WarnContext(ctx, "reporting enabled but nats.url empty; disabling publisher")
		return nil, noop, nil
	}

	nc, err := nats.Connect(cfg.NATS.URL)
	if err != nil {
		logger.WarnContext(ctx, "reporting enabled but nats connect failed; disabling publisher",
			"err", err.Error(),
			"url", cfg.NATS.URL,
		)
		return nil, noop, nil
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, noop, fmt.Errorf("jetstream: %w", err)
	}

	streamName := cfg.NATS.Stream
	if streamName == "" {
		streamName = bus.DefaultStreamName
	}
	bucket := cfg.NATS.Bucket
	if bucket == "" {
		bucket = bus.DefaultObjectStoreBucket
	}

	if err := bus.EnsureStream(ctx, js, streamName, []string{"gateway.>"}); err != nil {
		nc.Close()
		return nil, noop, fmt.Errorf("ensure stream: %w", err)
	}
	store, err := bus.EnsureObjectStore(ctx, js, bucket)
	if err != nil {
		nc.Close()
		return nil, noop, fmt.Errorf("ensure object store: %w", err)
	}

	pub := bus.New(bus.Options{
		JS:             js,
		ObjectStore:    store,
		StashBucket:    bucket,
		QueueSize:      cfg.NATS.PublishQueueSize,
		StashThreshold: cfg.NATS.StashThresholdBytes,
		Logger:         logger,
	})
	pub.Start(ctx)

	cleanup := func() {
		pub.Stop(publisherStopTimeout)
		nc.Close()
	}
	return pub, cleanup, nil
}

// startPrometheus mounts the scrape handler on a separate listener so the
// data-plane port is reserved for client traffic. The two goroutines exit on
// ctx cancellation.
func startPrometheus(ctx context.Context, bind string, handler http.Handler, logger *slog.Logger) {
	if bind == "" {
		return
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", handler)
	srv := &http.Server{
		Addr:              bind,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		logger.InfoContext(ctx, "prometheus listening", "addr", bind)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "prometheus listener exited", "err", err.Error())
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownPromServer(srv) //nolint:contextcheck // shutdown context is intentionally detached
	}()
}

// shutdownPromServer detaches the shutdown context from the cancelled parent
// so the drain budget outlives the SIGTERM that triggered it.
func shutdownPromServer(srv *http.Server) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// shutdownObservability flushes the telemetry pipeline with a fresh, detached
// context so the OTLP exporter is not killed by the same signal that triggered
// shutdown.
func shutdownObservability(p *observability.Provider) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), publisherStopTimeout)
	defer cancel()
	_ = p.Shutdown(shutdownCtx)
}
