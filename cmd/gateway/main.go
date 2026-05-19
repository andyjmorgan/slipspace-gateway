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
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/andyjmorgan/sluice-gateway/internal/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/bus"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/httperr"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
	"github.com/andyjmorgan/sluice-gateway/internal/routing"
	"github.com/andyjmorgan/sluice-gateway/internal/safego"
	"github.com/andyjmorgan/sluice-gateway/internal/server"
	"github.com/andyjmorgan/sluice-gateway/internal/version"
)

const (
	binaryName = "gateway"

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
	startedAt := time.Now().UTC()

	env, err := config.LoadEnv()
	if err != nil {
		return fmt.Errorf("gateway: load env: %w", err)
	}
	if err := env.Validate(); err != nil {
		return fmt.Errorf("gateway: validate env: %w", err)
	}

	resolved, err := config.Load(ctx, env.ConfigDir)
	if err != nil {
		return fmt.Errorf("gateway: load config %q: %w", env.ConfigDir, err)
	}

	build := observability.BuildInfo{Service: binaryName, Version: version.Version}
	obs, err := observability.Setup(ctx, observability.Config{
		PrometheusBind:   env.PrometheusBind,
		OTLPEndpoint:     env.OTLPEndpoint,
		OTLPProtocol:     env.OTLPProtocol,
		LogFormat:        env.LogFormat,
		LogLevel:         env.LogLevel,
		SnapshotInterval: time.Duration(env.AdminSnapshotIntervalMs) * time.Millisecond,
	}, build)
	if err != nil {
		return fmt.Errorf("gateway: observability setup: %w", err)
	}
	defer shutdownObservability(obs) //nolint:contextcheck // detached on purpose; see shutdownObservability

	logger := obs.Logger

	publisher, busCleanup, err := setupBus(ctx, env, logger, obs.Meters)
	if err != nil {
		return fmt.Errorf("gateway: bus setup: %w", err)
	}
	defer busCleanup()

	router, err := routing.New(resolved)
	if err != nil {
		return fmt.Errorf("gateway: router: %w", err)
	}

	resolver := auth.NewResolver(resolved)

	reporter := newReporterFactory(publisher, logger, obs.Meters)
	observerFactory := reporter.Factory()
	forwarder := proxy.New(proxy.Options{Logger: logger, ObserverFactory: observerFactory})

	evaluator := rules.NewEvaluator(resolved.PerConfigurationRules, env.RulesMaxGroupDepth, obs.Meters)

	errs := httperr.New(obs.Meters.ErrorResponsesTotal, logger)
	dataPlane := buildDataPlaneHandler(router, resolver, forwarder, evaluator, observerFactory, resolved.Providers, obs.Meters, errs, logger)

	// recoverMiddleware sits between correlation (so the captured
	// log carries the correlation_id) and the data-plane chain, so
	// any panic in routing/auth/bodycapture/rules/forwarder is
	// converted to a logged 500 instead of crashing the goroutine.
	root := correlationMiddleware(logger, recoverMiddleware(obs.Meters, errs, dataPlane))

	drain := time.Duration(env.ShutdownDrainSeconds) * time.Second

	srv := server.New(server.Options{
		Bind:         env.HTTPBind,
		Handler:      root,
		DrainTimeout: drain,
		Logger:       logger,
		Meters:       obs.Meters,
	})

	if obs.PromHandler != nil {
		startPrometheus(ctx, env.PrometheusBind, obs.PromHandler, logger)
	}

	// Kick off the snapshotter that backs the admin dashboard. Lifetime
	// is bound to ctx; the loop exits on cancellation.
	obs.Snapshotter.Start(ctx)

	if err := startAdmin(ctx, resolved, obs, logger, drain, startedAt); err != nil {
		return fmt.Errorf("gateway: admin listener: %w", err)
	}

	logger.InfoContext(ctx, "gateway starting",
		"bind", env.HTTPBind,
		"config_dir", env.ConfigDir,
		"providers", len(resolved.Providers),
		"configurations", len(resolved.Configurations),
		"api_keys", len(resolved.APIKeys),
		"admin_enabled", resolved.Admin != nil && resolved.Admin.Enabled,
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
func setupBus(ctx context.Context, env *config.ServerEnv, logger *slog.Logger, meters *observability.Meters) (*bus.Publisher, func(), error) {
	noop := func() {}
	if !env.ReportingEnabled() {
		return nil, noop, nil
	}

	nc, err := nats.Connect(env.NATSURL)
	if err != nil {
		logger.WarnContext(ctx, "reporting enabled but nats connect failed; disabling publisher",
			"err", err.Error(),
			"url", env.NATSURL,
		)
		return nil, noop, nil
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, noop, fmt.Errorf("jetstream: %w", err)
	}

	if err := bus.EnsureStream(ctx, js, env.NATSStream, []string{"gateway.>"}); err != nil {
		nc.Close()
		return nil, noop, fmt.Errorf("ensure stream: %w", err)
	}
	store, err := bus.EnsureObjectStore(ctx, js, env.NATSBucket)
	if err != nil {
		nc.Close()
		return nil, noop, fmt.Errorf("ensure object store: %w", err)
	}

	pub := bus.New(bus.Options{
		JS:             js,
		ObjectStore:    store,
		StashBucket:    env.NATSBucket,
		QueueSize:      env.NATSPublishQueueSize,
		StashThreshold: env.NATSStashThresholdBytes,
		Logger:         logger,
		Meters:         meters,
	})
	pub.Start(ctx)

	cleanup := func() { //nolint:contextcheck // pub.Stop's internal join goroutine uses context.Background intentionally — shutdown must not race the parent ctx cancel
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
	safego.Go(ctx, "gateway.prometheus.serve", logger, nil, func() {
		logger.InfoContext(ctx, "prometheus listening", "addr", bind)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "prometheus listener exited", "err", err.Error())
		}
	})
	safego.Go(ctx, "gateway.prometheus.shutdown_watcher", logger, nil, func() {
		<-ctx.Done()
		shutdownPromServer(srv) //nolint:contextcheck // shutdown context is intentionally detached
	})
}

// shutdownPromServer detaches the shutdown context from the cancelled parent
// so the drain budget outlives the SIGTERM that triggered it.
func shutdownPromServer(srv *http.Server) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// startAdmin brings up the management-console listener on a second
// http.Server when resolved.Admin is non-nil and Enabled. The console
// is fully off when the feature flag is false: no goroutine is spawned,
// no port is opened.
//
// The drain budget mirrors the data plane's so a SIGTERM gives in-flight
// admin requests the same shutdown headroom as proxy requests.
func startAdmin(ctx context.Context, resolved *config.ResolvedConfig, obs *observability.Provider, logger *slog.Logger, drain time.Duration, startedAt time.Time) error {
	if resolved.Admin == nil || !resolved.Admin.Enabled {
		logger.InfoContext(ctx, "admin console disabled")
		return nil
	}

	providers := make([]string, 0, len(resolved.Providers))
	for name := range resolved.Providers {
		providers = append(providers, name)
	}

	ruleAttachments := buildRuleAttachments(resolved)

	handler := admin.NewMux(admin.MuxOptions{
		Password:         resolved.Admin.ResolvePassword(),
		Meters:           obs.Meters,
		Snapshotter:      obs.Snapshotter,
		Providers:        providers,
		RuleAttachments:  ruleAttachments,
		GatewayStartedAt: startedAt,
	})
	bind := resolved.Admin.EffectiveBindAddr()
	srv := &http.Server{
		Addr:              bind,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	safego.Go(ctx, "gateway.admin.serve", logger, nil, func() {
		logger.InfoContext(ctx, "admin console listening", "addr", bind)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "admin listener exited", "err", err.Error())
		}
	})
	safego.Go(ctx, "gateway.admin.shutdown_watcher", logger, nil, func() {
		<-ctx.Done()
		shutdownAdminServer(srv, drain) //nolint:contextcheck // shutdown context is intentionally detached
	})
	return nil
}

// shutdownAdminServer drains the admin listener with a detached context
// so the budget survives the SIGTERM that initiated shutdown.
func shutdownAdminServer(srv *http.Server, drain time.Duration) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), drain)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// buildRuleAttachments inverts the Configuration → RuleNames map into
// the rule_name → [configuration, ...] shape the dashboard handler
// joins against. A rule referenced by zero configurations is omitted —
// it would never appear in a rules-fired event so the dashboard never
// needs to render it.
func buildRuleAttachments(resolved *config.ResolvedConfig) map[string][]string {
	out := map[string][]string{}
	for name, cfg := range resolved.Configurations {
		for _, ruleName := range cfg.RuleNames {
			out[ruleName] = append(out[ruleName], name)
		}
	}
	return out
}

// shutdownObservability flushes the telemetry pipeline with a fresh, detached
// context so the OTLP exporter is not killed by the same signal that triggered
// shutdown.
func shutdownObservability(p *observability.Provider) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), publisherStopTimeout)
	defer cancel()
	_ = p.Shutdown(shutdownCtx)
}
