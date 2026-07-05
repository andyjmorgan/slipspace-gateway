// Command arbiter is the SlipSpace Arbiter service: it ingests gen_ai +
// slipspace OTLP and HMAC-trusted large-payload webhooks from registered gateway
// appliances, stores them in Postgres, stitches them by correlation_id /
// session_id, and serves an operator console identical to the gateway's own
// dashboard + ring inspector.
//
// It binds two listeners: an HTTP surface (default :8686) carrying the
// liveness/readiness probes, the open HMAC-trusted Record webhook, and the
// Basic-auth operator console + query API; and an OTLP gRPC surface (default
// :8687) ingesting gen_ai spans + slipspace meters. Boot loads the config,
// connects to Postgres and runs the forward-only migrations, then serves both
// surfaces until SIGINT/SIGTERM triggers a bounded graceful shutdown. See
// docs/arbiter.md for the operator overview.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/arbiter"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/ingest"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/registry"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/server"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
	"github.com/andyjmorgan/slipspace-gateway/internal/safego"
	"github.com/andyjmorgan/slipspace-gateway/internal/version"
)

const (
	binaryName      = "arbiter"
	shutdownTimeout = 5 * time.Second
	storeOpenBudget = 15 * time.Second
)

func main() {
	os.Exit(mainErr())
}

func mainErr() int {
	configPath := flag.String("config", os.Getenv("SLIPSPACE_ARBITER_CONFIG"), "path to the Arbiter config YAML")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s %s\n", binaryName, version.Version)
		return 0
	}

	log := newLogger(os.Getenv("LOG_LEVEL"))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *configPath, log); err != nil {
		log.Error("arbiter exited with error", "err", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, configPath string, log *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	// Bound the initial connect so a wedged Postgres surfaces fast at boot
	// rather than hanging the process.
	openCtx, cancel := context.WithTimeout(ctx, storeOpenBudget)
	defer cancel()
	st, err := store.Open(openCtx, cfg.Postgres.DSN)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("arbiter: migrate: %w", err)
	}
	if v, err := st.SchemaVersion(ctx); err == nil {
		log.Info("store ready", "schema_version", v)
	}

	// The migration-v10 token-column backfill runs out-of-band by design (#327:
	// inline it and the boot scan outlasts the liveness probe). Batched, bound
	// to the process ctx, run-once via backfill_runs, and non-fatal — a failure
	// only leaves pre-v10 rows reading 0 tokens until the next boot retries.
	safego.Go(ctx, "arbiter.backfill.tokens", log, nil, func() {
		n, err := st.BackfillTokenColumns(ctx, 0)
		switch {
		case err != nil && ctx.Err() != nil:
			// Shutdown mid-backfill: partial progress, no completion row; the
			// next boot resumes idempotently.
			log.Info("token column backfill interrupted by shutdown", "rows_updated", n)
		case err != nil:
			log.Error("token column backfill failed", "rows_updated", n, "err", err)
		case n > 0:
			log.Info("token column backfill complete", "rows_updated", n)
		}
	})

	// The migration-v12 tags-column backfill runs out-of-band for the same
	// reason as the token columns (re-deriving tags from span_event detoasts
	// every span — the 30 s scan the column exists to replace). Run-once via
	// backfill_runs, non-fatal — a failure only leaves pre-v12 rows reading an
	// empty tag set (excluded from tag facets/filters) until the next boot.
	safego.Go(ctx, "arbiter.backfill.tags", log, nil, func() {
		n, err := st.BackfillTags(ctx, 0)
		switch {
		case err != nil && ctx.Err() != nil:
			log.Info("tags column backfill interrupted by shutdown", "rows_updated", n)
		case err != nil:
			log.Error("tags column backfill failed", "rows_updated", n, "err", err)
		case n > 0:
			log.Info("tags column backfill complete", "rows_updated", n)
		}
	})

	// The migration-v20 cost-column backfill runs out-of-band for the same
	// reason as the token columns (re-deriving cost from span_event detoasts
	// every span). Run-once via backfill_runs, non-fatal — a failure only
	// leaves pre-v20 rows reading $0 in the session/thread spend rollups
	// until the next boot retries.
	safego.Go(ctx, "arbiter.backfill.cost", log, nil, func() {
		n, err := st.BackfillCost(ctx, 0)
		switch {
		case err != nil && ctx.Err() != nil:
			log.Info("cost column backfill interrupted by shutdown", "rows_updated", n)
		case err != nil:
			log.Error("cost column backfill failed", "rows_updated", n, "err", err)
		case n > 0:
			log.Info("cost column backfill complete", "rows_updated", n)
		}
	})

	// Arbiter security scanner (optional, REF-008). When enabled it explodes
	// each ingested span into check tasks at ingest (atomically with the span)
	// and drains the outbox in the background. Disabled => ingest is untouched.
	var exploder ingest.Exploder
	if cfg.ScannerEnabled() {
		scanner, err := arbiter.New(ctx, cfg, log)
		if err != nil {
			return fmt.Errorf("arbiter: scanner: %w", err)
		}
		exploder = scanner
		scanner.Run(ctx, st)
		log.Info("arbiter scanner enabled", "check_types", scanner.CheckTypes(), "workers", cfg.ScannerWorkers())
	}

	// Ingest surfaces: HMAC Record webhook (HTTP) for the full per-request
	// digital record, OTLP gRPC for the gen_ai telemetry feed + slipspace meters.
	reg := registry.New(cfg.Gateways)
	recordIngest := ingest.NewRecordHandler(reg, st, log)
	otlp := ingest.NewOTLPServer(
		ingest.NewTraceReceiver(st, log, cfg.ContentCap(), exploder),
		ingest.NewMetricsReceiver(st, log),
	)

	// WithAppliedConfig serves the redacted snapshot at GET /api/v1/settings so
	// an operator can see the running config (incl. the scanner block) without
	// any secret leaking — the password hash, HMAC secrets, evidence key, and DSN
	// password are all redacted by Redacted().
	httpSrv := server.New(cfg.Console, st, st, recordIngest, cfg.SpanFieldCap(), log).
		WithAppliedConfig(cfg.Redacted()).
		HTTPServer(cfg.HTTPBind)

	errCh := make(chan error, 1)
	safego.Go(ctx, "arbiter.serve.http", log, nil, func() {
		log.Info("http listening", "addr", cfg.HTTPBind)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	})

	lis, err := net.Listen("tcp", cfg.OTLPBind)
	if err != nil {
		return fmt.Errorf("arbiter: otlp listen: %w", err)
	}
	safego.Go(ctx, "arbiter.serve.otlp", log, nil, func() {
		log.Info("otlp listening", "addr", cfg.OTLPBind)
		if err := otlp.Serve(lis); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	})

	select {
	case <-ctx.Done():
		log.Info("shutting down", "signal", ctx.Err())
	case err := <-errCh:
		if err != nil {
			otlp.GracefulStop()
			return fmt.Errorf("arbiter: serve: %w", err)
		}
		return nil
	}

	otlp.GracefulStop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Detached from the cancelled signal context on purpose — the shutdown
	// budget must outlive the SIGTERM that triggered it.
	if err := httpSrv.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck
		return fmt.Errorf("arbiter: shutdown: %w", err)
	}
	return nil
}

func newLogger(levelEnv string) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(strings.TrimSpace(levelEnv)) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	case "", "info":
		level = slog.LevelInfo
	default:
		level = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	return slog.New(handler).With(
		"service", binaryName,
		"version", version.Version,
	)
}
