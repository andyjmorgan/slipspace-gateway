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

	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/connector/factory"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/reconciler"
	"github.com/andyjmorgan/sluice-gateway/internal/headers"
	"github.com/andyjmorgan/sluice-gateway/internal/httperr"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	resiliencemw "github.com/andyjmorgan/sluice-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/observability/livefeed"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
	"github.com/andyjmorgan/sluice-gateway/internal/safego"
	"github.com/andyjmorgan/sluice-gateway/internal/server"
	"github.com/andyjmorgan/sluice-gateway/internal/spool"
	"github.com/andyjmorgan/sluice-gateway/internal/version"
)

const (
	binaryName = "gateway"

	// spoolStopTimeout bounds graceful drain of the spool's compress +
	// upload workers on SIGTERM. Slightly larger than the request drain
	// so segments in flight when the listener stops still finish.
	spoolStopTimeout = 30 * time.Second
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

	resolved, bootedEmpty, err := config.LoadStartupV2(ctx, env.ConfigDir, env.ControlPlaneManaged())
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

	if bootedEmpty {
		logger.Warn("no local config; booting empty and fetching from control plane",
			"config_dir", env.ConfigDir,
			"control_plane_endpoint", env.ControlPlaneEndpoint,
		)
	}

	spoolInst, spoolCleanup, err := setupSpool(ctx, env, resolved, logger)
	if err != nil {
		return fmt.Errorf("gateway: spool setup: %w", err)
	}
	defer spoolCleanup()

	// The Store owns the live ResolvedConfig and brokers swaps via
	// subscribers. Consumers that pre-derive state (routing.Router)
	// subscribe; hot-readers (auth.Resolver, rules.Evaluator, admin
	// handlers, reporter) call Snapshot per operation. Phase 2 adds
	// the admin write path that calls store.Replace.
	store := config.NewStore(resolved)

	// Shared between config sync (writes the applied closure hash) and the
	// reconciler (reports it on heartbeat) so the control plane sees config drift.
	appliedHash := &reconciler.AppliedHash{}

	// When CP-managed, fetch + apply the control-plane config before the data
	// plane starts serving (and keep it synced after). No-op otherwise.
	startControlPlaneConfigSync(ctx, env, store, appliedHash, obs, logger)

	resolver := auth.NewResolver(store)

	liveFeed, err := buildLiveFeed(env, logger)
	if err != nil {
		return fmt.Errorf("gateway: live feed: %w", err)
	}

	bodyStore, err := buildBodyStore(env, logger)
	if err != nil {
		return fmt.Errorf("gateway: body store: %w", err)
	}

	contentCaps := resolved.Telemetry.ContentCapture.Resolve()
	reporter := newReporterFactory(spoolInst, store, logger, obs.Meters, liveFeed, bodyStore, obs.Tracer(), obs.EventLogger(), env.OTelCaptureContent, contentCaps)
	observerFactory := reporter.Factory()
	// One Redactor for the whole process — the built-in substring
	// list plus any operator-supplied extras from
	// SLUICE_REDACT_EXTRA_HEADERS. Threaded into the proxy (for
	// debug header traces), bodycapture (for the Captured.Headers
	// surface), and livefeed (for the tee-writer response headers).
	redactor := headers.NewRedactor(env.RedactExtraHeaders)
	if extras := redactor.Extras(); len(extras) > 0 {
		logger.Info("redactor configured", "extra_substrings", extras)
	}

	// sessionResolver promotes a client-supplied session/bundle id to a
	// first-class field. X-Sluice-Session-Id is authoritative; the
	// shipped client defaults plus SLUICE_SESSION_ID_HEADERS follow.
	sessionResolver := observability.NewSessionResolver(env.SessionIDHeaders)
	forwarder := proxy.New(proxy.Options{
		Logger:                logger,
		ObserverFactory:       observerFactory,
		Redactor:              redactor,
		ResponseHeaderTimeout: time.Duration(env.UpstreamResponseHeaderTimeoutSeconds) * time.Second,
		// Response-phase body rewrites (response.body.* rule targets)
		// apply here, in the proxy's ModifyResponse hook. The adapter
		// keeps internal/proxy decoupled from the rules engine.
		ResponseBodyTransform: func(ctx context.Context, resp *http.Response, streaming bool) error {
			return rules.ApplyResponseRewrites(ctx, obs.Meters, env.ExternalURL, resp, streaming)
		},
	})

	evaluator := rules.NewEvaluator(store, env.RulesMaxGroupDepth, obs.Meters)

	errs := httperr.New(obs.Meters.ErrorResponsesTotal, logger)
	// The CB StateListener fires from inside breaker.mu without a
	// request ctx — see circuitBreakerTransitionListener for the
	// context.Background() rationale.
	breakers := resiliencemw.NewInMemoryBreakerStore(circuitBreakerTransitionListener(obs.Meters)) //nolint:contextcheck // listener has no ctx by design
	if err := registerBreakerStateGauge(obs, breakers); err != nil {
		return fmt.Errorf("gateway: register cb.state gauge: %w", err)
	}
	dataPlane := buildDataPlaneHandler(resolver, forwarder, evaluator, observerFactory, store, breakers, obs.Meters, errs, redactor, logger)

	// responseCaptureMiddleware sits between recover and the data
	// plane so every panic is still logged, but the per-request
	// response buffer is allocated before any handler runs. Nil-safe
	// when bodies are disabled — the wrapper degrades to passthrough.
	captured := responseCaptureMiddleware(env.AdminLiveFeedBodyMaxBytes, bodyStore != nil, redactor, dataPlane)

	// recoverMiddleware sits between correlation (so the captured
	// log carries the correlation_id) and the data-plane chain, so
	// any panic in routing/auth/bodycapture/rules/forwarder is
	// converted to a logged 500 instead of crashing the goroutine.
	root := correlationMiddleware(logger, sessionResolver, redactor, recoverMiddleware(obs.Meters, errs, captured))

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

	startAdmin(ctx, store, obs, logger, drain, startedAt, liveFeed, bodyStore, breakerStoreAdapter{store: breakers}, env.ConfigDir)

	startControlPlaneReconciler(ctx, env, appliedHash, obs, logger)

	logger.InfoContext(ctx, "gateway starting",
		"bind", env.HTTPBind,
		"config_dir", env.ConfigDir,
		"backends", len(resolved.Backends),
		"configurations", len(resolved.Configurations),
		"api_keys", len(resolved.APIKeys),
		"admin_enabled", resolved.Admin != nil && resolved.Admin.Enabled,
	)

	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("gateway: run: %w", err)
	}
	return nil
}

// setupSpool wires the connector spool. Builds concrete connectors via
// factory.BuildAll, constructs the Spool at env.SpoolRoot, registers
// one track per connector, and starts the workers. Returns
// (nil, noop, nil) when ResolvedConfig.Connectors is empty — the
// reporter then short-circuits at OnComplete and the request path
// pays nothing.
//
// Per design note "Connector + Spool Architecture": startup recovery
// (uploading/ → sealed/, torn active/ → quarantine/) runs at New /
// RegisterTrack time inside each track's manager. A failed startup
// recovery aborts gateway boot; an operator-visible error is the
// right escalation since silently dropping records on a misconfigured
// spool root is worse than refusing to start.
func setupSpool(ctx context.Context, env *config.ServerEnv, resolved *config.ResolvedConfigV2, logger *slog.Logger) (*spool.Spool, func(), error) {
	noop := func() {}
	if len(resolved.Connectors) == 0 {
		logger.InfoContext(ctx, "no connectors configured; spool disabled")
		return nil, noop, nil
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "local"
	}

	conns, err := factory.BuildAll(ctx, resolved.Connectors, factory.Options{
		InstanceID: hostname,
	})
	if err != nil {
		return nil, noop, fmt.Errorf("build connectors: %w", err)
	}

	s, err := spool.New(spool.Options{
		Root:   env.SpoolRoot,
		Logger: logger,
	})
	if err != nil {
		return nil, noop, fmt.Errorf("spool: %w", err)
	}

	for i, c := range conns {
		cfg := resolved.Connectors[i]
		opts := spool.RegisterTrackOptions{Connector: c}
		if cfg.Rotation != nil {
			opts.Rotation = spool.RotationOpts{
				MaxBytes: cfg.Rotation.MaxBytes,
				MaxAge:   time.Duration(cfg.Rotation.MaxAgeSeconds) * time.Second,
			}
		}
		if err := s.RegisterTrack(opts); err != nil {
			return nil, noop, fmt.Errorf("register track %q: %w", c.Name(), err)
		}
	}

	if err := s.Start(ctx); err != nil {
		return nil, noop, fmt.Errorf("spool start: %w", err)
	}
	logger.InfoContext(ctx, "spool started",
		"root", env.SpoolRoot,
		"tracks", len(conns),
	)

	cleanup := func() { //nolint:contextcheck // spool.Stop's join goroutine uses context.Background intentionally — shutdown must not race the parent ctx cancel
		if ok := s.Stop(spoolStopTimeout); !ok {
			logger.Warn("spool stop timed out", "timeout", spoolStopTimeout)
		}
	}
	return s, cleanup, nil
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
func responseCaptureMiddleware(maxBytes int, enabled bool, redactor *headers.Redactor, next http.Handler) http.Handler {
	if !enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := livefeed.NewResponseBuffer(maxBytes)
		ctx := livefeed.WithResponseBuffer(r.Context(), buf)
		wrapped := livefeed.WrapResponseWriter(w, buf, redactor)
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
// http.Server when store.Snapshot().Admin is non-nil and Enabled. The
// console is fully off when the feature flag is false: no goroutine is
// spawned, no port is opened.
//
// The drain budget mirrors the data plane's so a SIGTERM gives in-flight
// admin requests the same shutdown headroom as proxy requests.
//
// The non-mutating snapshot fields read here (Admin, Providers names,
// RuleNames, AddTag actions, BindAddr) are sampled at startup. The
// admin write path (Phase 2) does not allow editing the Admin block
// nor the provider list, so caching these at boot is safe; the
// per-request reads happen through opts.Store inside the handlers.
func startAdmin(ctx context.Context, store *config.Store, obs *observability.Provider, logger *slog.Logger, drain time.Duration, startedAt time.Time, liveFeed *livefeed.Ring, bodyStore *livefeed.BodyStore, breakerStates admin.CircuitBreakerStateSource, configDir string) {
	resolved := store.Snapshot()
	if resolved.Admin == nil || !resolved.Admin.Enabled {
		logger.InfoContext(ctx, "admin console disabled")
		return
	}

	providers := make([]string, 0, len(resolved.Backends))
	for name := range resolved.Backends {
		providers = append(providers, name)
	}

	ruleAttachments := buildRuleAttachments(resolved)
	tagAttachments := buildTagAttachments(resolved)

	hostname, err := os.Hostname()
	if err != nil {
		// Hostname is informational metadata on the export manifest;
		// a lookup failure should not block the admin listener.
		hostname = "unknown"
		logger.WarnContext(ctx, "admin: hostname lookup failed", "err", err.Error())
	}

	handler := admin.NewMux(admin.MuxOptions{
		Password:         resolved.Admin.ResolvePassword(),
		Meters:           obs.Meters,
		Snapshotter:      obs.Snapshotter,
		Providers:        providers,
		RuleAttachments:  ruleAttachments,
		TagAttachments:   tagAttachments,
		GatewayStartedAt: startedAt,
		Store:            store,
		LiveFeed:         liveFeed,
		BodyStore:        bodyStore,
		BreakerStates:    breakerStates,
		ConfigDir:        configDir,
		Hostname:         hostname,
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
func buildRuleAttachments(resolved *config.ResolvedConfigV2) map[string][]string {
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
func buildTagAttachments(resolved *config.ResolvedConfigV2) map[string][]string {
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.Shutdown(shutdownCtx)
}

// startControlPlaneReconciler launches the gateway->control-plane registration
// loop when SLUICE_CONTROL_PLANE_ENDPOINT is set. It runs in a background
// goroutine bound to ctx and never touches the request path (invariant CP-0):
// a construction error is logged and the gateway keeps serving from local
// config. No-op in standalone (file-backed) mode.
func startControlPlaneReconciler(ctx context.Context, env *config.ServerEnv, applied *reconciler.AppliedHash, obs *observability.Provider, logger *slog.Logger) {
	if !env.ControlPlaneEnabled() {
		return
	}

	gatewayID := env.GatewayID
	if gatewayID == "" {
		if hn, err := os.Hostname(); err == nil && hn != "" {
			gatewayID = hn
		} else {
			gatewayID = binaryName
		}
	}

	rec, err := reconciler.New(reconciler.Options{
		Endpoint:  env.ControlPlaneEndpoint,
		Token:     env.ControlPlaneToken,
		TLS:       env.ControlPlaneTLSEnabled,
		GatewayID: gatewayID,
		Version:   version.Version,
		Labels:    parseLabels(env.GatewayLabels),
		Applied:   applied,
		Interval:  time.Duration(env.ControlPlaneHeartbeatSeconds) * time.Second,
		Logger:    logger,
	})
	if err != nil {
		logger.WarnContext(ctx, "control-plane reconciler not started", "err", err.Error())
		return
	}

	logger.InfoContext(ctx, "control-plane reconciler starting",
		"endpoint", env.ControlPlaneEndpoint,
		"gateway_id", gatewayID,
		"tls", env.ControlPlaneTLSEnabled,
	)
	safego.Go(ctx, "gateway.controlplane.reconciler", logger, obs.Meters, func() {
		rec.Run(ctx)
	})
}

// startControlPlaneConfigSync, when the gateway is CP-managed with a bootstrap
// api-key, fetches its configuration closure from the control plane and applies
// it before the data plane begins serving (Bootstrap), then keeps it in sync in
// the background (Run). No-op without an endpoint + bootstrap key — the gateway
// then serves its local file-backed config. CP-0 holds: an unreachable control
// plane leaves the locally-loaded config in place (serve-stale).
func startControlPlaneConfigSync(ctx context.Context, env *config.ServerEnv, store *config.Store, applied *reconciler.AppliedHash, obs *observability.Provider, logger *slog.Logger) {
	if !env.ControlPlaneEnabled() || env.ControlPlaneBootstrapAPIKey == "" {
		return
	}

	syncer, err := reconciler.NewConfigSyncer(reconciler.ConfigSyncerOptions{
		Endpoint:  env.ControlPlaneEndpoint,
		Token:     env.ControlPlaneToken,
		TLS:       env.ControlPlaneTLSEnabled,
		APIKey:    env.ControlPlaneBootstrapAPIKey,
		CachePath: env.ControlPlaneCachePath,
		Store:     store,
		Applied:   applied,
		Logger:    logger,
	})
	if err != nil {
		logger.WarnContext(ctx, "control-plane config sync not started", "err", err.Error())
		return
	}

	logger.InfoContext(ctx, "control-plane config sync: bootstrapping before serve",
		"endpoint", env.ControlPlaneEndpoint,
	)
	syncer.Bootstrap(ctx)
	safego.Go(ctx, "gateway.controlplane.configsync", logger, obs.Meters, func() {
		syncer.Run(ctx)
	})
}

// parseLabels turns a "k=v" list into a map. Entries without "=" or with an
// empty key are skipped.
func parseLabels(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
