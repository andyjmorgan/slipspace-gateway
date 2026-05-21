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

	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/bus"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/httperr"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/observability/livefeed"
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

	liveFeed, err := buildLiveFeed(env, logger)
	if err != nil {
		return fmt.Errorf("gateway: live feed: %w", err)
	}

	bodyStore, err := buildBodyStore(env, logger)
	if err != nil {
		return fmt.Errorf("gateway: body store: %w", err)
	}

	reporter := newReporterFactory(publisher, logger, obs.Meters, liveFeed, bodyStore)
	observerFactory := reporter.Factory()
	forwarder := proxy.New(proxy.Options{Logger: logger, ObserverFactory: observerFactory})

	evaluator := rules.NewEvaluator(resolved.PerConfigurationRules, env.RulesMaxGroupDepth, obs.Meters)

	errs := httperr.New(obs.Meters.ErrorResponsesTotal, logger)
	dataPlane := buildDataPlaneHandler(router, resolver, forwarder, evaluator, observerFactory, resolved.Providers, obs.Meters, errs, logger)

	// responseCaptureMiddleware sits between recover and the data
	// plane so every panic is still logged, but the per-request
	// response buffer is allocated before any handler runs. Nil-safe
	// when bodies are disabled — the wrapper degrades to passthrough.
	captured := responseCaptureMiddleware(env.AdminLiveFeedBodyMaxBytes, bodyStore != nil, dataPlane)

	// recoverMiddleware sits between correlation (so the captured
	// log carries the correlation_id) and the data-plane chain, so
	// any panic in routing/auth/bodycapture/rules/forwarder is
	// converted to a logged 500 instead of crashing the goroutine.
	root := correlationMiddleware(logger, recoverMiddleware(obs.Meters, errs, captured))

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

	startAdmin(ctx, resolved, obs, logger, drain, startedAt, liveFeed, bodyStore)

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

// buildLiveFeed constructs the in-process ring that backs the admin
// console's live-messages pane. Returns (nil, nil) when the feature is
// disabled via SLUICE_ADMIN_LIVE_FEED_CAPACITY=0 — the reporter and
// admin mux both no-op against a nil ring, so the rest of the wiring
// stays uniform either way.
func buildLiveFeed(env *config.ServerEnv, logger *slog.Logger) (*livefeed.Ring, error) {
	if !env.LiveFeedEnabled() {
		logger.Info("admin live feed disabled (capacity=0)")
		return nil, nil
	}
	ring, err := livefeed.NewRing(env.AdminLiveFeedCapacity)
	if err != nil {
		return nil, fmt.Errorf("livefeed: %w", err)
	}
	logger.Info("admin live feed enabled", "capacity", env.AdminLiveFeedCapacity)
	return ring, nil
}

// buildBodyStore constructs the byte-bounded LRU that backs the
// /admin/api/v1/messages/{id}/body endpoint. Returns (nil, nil) when
// disabled — the reporter and the body endpoint both no-op against
// nil so wiring stays uniform.
func buildBodyStore(env *config.ServerEnv, logger *slog.Logger) (*livefeed.BodyStore, error) {
	if !env.LiveFeedBodiesEnabled() {
		logger.Info("admin live feed body capture disabled")
		return nil, nil
	}
	store, err := livefeed.NewBodyStore(env.AdminLiveFeedBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("livefeed bodystore: %w", err)
	}
	logger.Info("admin live feed body capture enabled",
		"total_bytes", env.AdminLiveFeedBodyBytes,
		"per_body_max_bytes", env.AdminLiveFeedBodyMaxBytes,
	)
	return store, nil
}

// responseCaptureMiddleware allocates a per-request ResponseBuffer,
// stashes it on context for the reporter to read at OnComplete, and
// wraps w so every Write tees into the buffer. When enabled is false,
// returns next unchanged — the rest of the chain is identical either
// way, and the reporter degrades to "no body captured" without a
// branch at every read site.
func responseCaptureMiddleware(maxBytes int, enabled bool, next http.Handler) http.Handler {
	if !enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := livefeed.NewResponseBuffer(maxBytes)
		ctx := livefeed.WithResponseBuffer(r.Context(), buf)
		wrapped := livefeed.WrapResponseWriter(w, buf)
		next.ServeHTTP(wrapped, r.WithContext(ctx))
	})
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
func startAdmin(ctx context.Context, resolved *config.ResolvedConfig, obs *observability.Provider, logger *slog.Logger, drain time.Duration, startedAt time.Time, liveFeed *livefeed.Ring, bodyStore *livefeed.BodyStore) {
	if resolved.Admin == nil || !resolved.Admin.Enabled {
		logger.InfoContext(ctx, "admin console disabled")
		return
	}

	providers := make([]string, 0, len(resolved.Providers))
	for name := range resolved.Providers {
		providers = append(providers, name)
	}

	ruleAttachments := buildRuleAttachments(resolved)
	tagAttachments := buildTagAttachments(resolved)

	handler := admin.NewMux(admin.MuxOptions{
		Password:         resolved.Admin.ResolvePassword(),
		Meters:           obs.Meters,
		Snapshotter:      obs.Snapshotter,
		Providers:        providers,
		RuleAttachments:  ruleAttachments,
		TagAttachments:   tagAttachments,
		GatewayStartedAt: startedAt,
		Resolved:         resolved,
		LiveFeed:         liveFeed,
		BodyStore:        bodyStore,
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

// buildTagAttachments derives the tag → [configuration, ...] map the
// dashboard's tags-fired panel joins against. Walks each
// configuration's RuleNames in declaration order, dereferences via
// RuleIndex, and pulls every AddTagAction tag onto the tag's
// configuration list. A tag attached by zero AddTagAction rules in
// any configuration is omitted (it would never appear on the
// gateway.tags.applied.total counter so the dashboard never needs
// to render it).
//
// The same configuration name can appear multiple times for the same
// tag if multiple rules in that configuration's chain attach it; we
// dedupe so the SPA sees one entry per (tag, configuration) pair.
func buildTagAttachments(resolved *config.ResolvedConfig) map[string][]string {
	out := map[string][]string{}
	for configName, cfg := range resolved.Configurations {
		for _, ruleName := range cfg.RuleNames {
			rule, ok := resolved.RuleIndex[ruleName]
			if !ok || rule == nil {
				continue
			}
			for _, action := range rule.Actions {
				addTag, ok := action.(*rulescontract.AddTagAction)
				if !ok {
					continue
				}
				tag := addTag.Tag
				if tag == "" {
					continue
				}
				existing := out[tag]
				found := false
				for _, c := range existing {
					if c == configName {
						found = true
						break
					}
				}
				if !found {
					out[tag] = append(existing, configName)
				}
			}
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
