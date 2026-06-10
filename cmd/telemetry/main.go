// Command telemetry is the central telemetry service: it ingests gen_ai +
// sluice OTLP and HMAC-trusted large-payload webhooks from registered gateway
// appliances, stores them in Postgres, stitches them by correlation_id /
// session_id, and serves an operator console identical to the gateway's own
// dashboard + ring inspector.
//
// It binds two listeners: an HTTP surface (default :8686) carrying the
// liveness/readiness probes, the open HMAC-trusted Record webhook, and the
// Basic-auth operator console + query API; and an OTLP gRPC surface (default
// :8687) ingesting gen_ai spans + sluice meters. Boot loads the config,
// connects to Postgres and runs the forward-only migrations, then serves both
// surfaces until SIGINT/SIGTERM triggers a bounded graceful shutdown. See
// docs/telemetry-service.md for the operator overview.
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

	"github.com/andyjmorgan/sluice-gateway/internal/safego"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/config"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/ingest"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/registry"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/server"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
	"github.com/andyjmorgan/sluice-gateway/internal/version"
)

const (
	binaryName      = "telemetry"
	shutdownTimeout = 5 * time.Second
	storeOpenBudget = 15 * time.Second
)

func main() {
	os.Exit(mainErr())
}

func mainErr() int {
	configPath := flag.String("config", os.Getenv("SLUICE_TELEMETRY_CONFIG"), "path to the telemetry config YAML")
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
		log.Error("telemetry exited with error", "err", err)
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
		return fmt.Errorf("telemetry: migrate: %w", err)
	}
	if v, err := st.SchemaVersion(ctx); err == nil {
		log.Info("store ready", "schema_version", v)
	}

	// The migration-v10 token-column backfill runs out-of-band by design (#327:
	// inline it and the boot scan outlasts the liveness probe). Batched, bound
	// to the process ctx, run-once via backfill_runs, and non-fatal — a failure
	// only leaves pre-v10 rows reading 0 tokens until the next boot retries.
	safego.Go(ctx, "telemetry.backfill.tokens", log, nil, func() {
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

	// Ingest surfaces: HMAC Record webhook (HTTP) for the full per-request
	// digital record, OTLP gRPC for the gen_ai telemetry feed + sluice meters.
	reg := registry.New(cfg.Gateways)
	recordIngest := ingest.NewRecordHandler(reg, st, log)
	otlp := ingest.NewOTLPServer(
		ingest.NewTraceReceiver(st, log, cfg.ContentCap()),
		ingest.NewMetricsReceiver(st, log),
	)

	httpSrv := server.New(cfg.Console, st, st, recordIngest, log).HTTPServer(cfg.HTTPBind)

	errCh := make(chan error, 1)
	safego.Go(ctx, "telemetry.serve.http", log, nil, func() {
		log.Info("http listening", "addr", cfg.HTTPBind)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	})

	lis, err := net.Listen("tcp", cfg.OTLPBind)
	if err != nil {
		return fmt.Errorf("telemetry: otlp listen: %w", err)
	}
	safego.Go(ctx, "telemetry.serve.otlp", log, nil, func() {
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
			return fmt.Errorf("telemetry: serve: %w", err)
		}
		return nil
	}

	otlp.GracefulStop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Detached from the cancelled signal context on purpose — the shutdown
	// budget must outlive the SIGTERM that triggered it.
	if err := httpSrv.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck
		return fmt.Errorf("telemetry: shutdown: %w", err)
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
