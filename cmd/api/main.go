package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
	"github.com/andyjmorgan/sluice-gateway/internal/safego"
	"github.com/andyjmorgan/sluice-gateway/internal/version"
)

const (
	binaryName      = "api"
	defaultHTTPBind = "0.0.0.0:8484"
	defaultGRPCBind = "0.0.0.0:8485"
	shutdownTimeout = 5 * time.Second
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

	slog.SetDefault(newLogger(os.Getenv("LOG_LEVEL")))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("api exited with error", "err", err)
		return 1
	}
	return 0
}

// run brings up the control plane: the gRPC fleet channel (registration +
// heartbeat) and the HTTP read API the console reads. Phase 1 uses an
// in-memory registry; config distribution and Postgres persistence land in
// later phases. Cardinal invariant CP-0 holds — nothing here sits on a
// gateway's request path.
func run(ctx context.Context) error {
	logger := slog.Default()
	cfg := loadConfig()

	reg := controlplane.NewMemoryRegistry()

	rt, err := buildConfigProvider(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer rt.cleanup()

	tlsCfg, err := cfg.tlsConfig()
	if err != nil {
		return fmt.Errorf("api: tls: %w", err)
	}
	if tlsCfg == nil {
		logger.Warn("control-plane gRPC serving WITHOUT TLS (plaintext) — trusted-network only")
	}
	if cfg.token == "" {
		logger.Warn("control-plane gRPC bootstrap-token auth DISABLED — trusted-network only")
	}

	grpcSrv := controlplane.NewGRPCServer(reg, logger, controlplane.GRPCServerOptions{
		Token:  cfg.token,
		TLS:    tlsCfg,
		Config: rt.provider,
	})
	lis, err := net.Listen("tcp", cfg.grpcBind)
	if err != nil {
		return fmt.Errorf("api: grpc listen %q: %w", cfg.grpcBind, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/api/v1/fleet", controlplane.NewFleetHTTPHandler(reg, cfg.staleAfter, cfg.offlineAfter))
	if rt.adminDB != nil {
		mux.Handle("/api/v1/config/", controlplane.NewConfigAdminHandler(rt.adminDB, rt.liveStore, logger))
		logger.Info("config write API enabled at /api/v1/config")
	}
	mux.Handle("/", controlplane.ConsoleHandler())
	var httpHandler http.Handler = mux
	if cfg.adminPassword != "" {
		httpHandler = basicAuthExceptHealth(cfg.adminPassword, mux)
		logger.Info("HTTP API protected by Basic auth (user: admin)")
	} else {
		logger.Warn("HTTP API UNAUTHENTICATED — set SLUICE_CP_ADMIN_PASSWORD; serve only on a trusted network (ClusterIP, no public ingress)")
	}

	httpSrv := &http.Server{
		Addr:              cfg.httpBind,
		Handler:           httpHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	safego.Go(ctx, "api.grpc.serve", logger, nil, func() {
		logger.Info("control-plane gRPC listening", "addr", cfg.grpcBind, "tls", tlsCfg != nil)
		if err := grpcSrv.Serve(lis); err != nil {
			errCh <- fmt.Errorf("grpc serve: %w", err)
			return
		}
		errCh <- nil
	})
	safego.Go(ctx, "api.http.serve", logger, nil, func() {
		logger.Info("control-plane HTTP listening", "addr", cfg.httpBind)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http serve: %w", err)
			return
		}
		errCh <- nil
	})

	select {
	case <-ctx.Done():
		logger.Info("shutting down", "signal", ctx.Err())
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("api: %w", err)
		}
		return nil
	}

	grpcSrv.GracefulStop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	// Detached from the cancelled signal context on purpose — the shutdown
	// budget must outlive the SIGTERM that triggered it.
	if err := httpSrv.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck
		return fmt.Errorf("api: shutdown: %w", err)
	}
	return nil
}

// configRuntime is the assembled config source. adminDB + liveStore are set
// only in Postgres-backed mode, which enables the config write API.
type configRuntime struct {
	provider  controlplane.ConfigProvider
	adminDB   *configdb.DB
	liveStore *config.Store
	cleanup   func()
}

// basicAuthExceptHealth wraps next with Basic auth, leaving /healthz open so
// k8s liveness/readiness probes don't need credentials.
func basicAuthExceptHealth(password string, next http.Handler) http.Handler {
	authed := controlplane.BasicAuth(password, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		authed.ServeHTTP(w, r)
	})
}

// buildConfigProvider selects the config source for distribution:
//   - SLUICE_CP_DATABASE_URL set -> Postgres-backed (seed from SLUICE_CONFIG_DIR
//     on first boot, serve the active published version, expose the write API).
//   - else SLUICE_CONFIG_DIR set -> file-backed (read-only).
//   - else -> registration-only (nil provider).
func buildConfigProvider(ctx context.Context, cfg apiConfig, logger *slog.Logger) (*configRuntime, error) {
	noop := func() {}
	switch {
	case cfg.databaseURL != "":
		return buildDBConfigProvider(ctx, cfg, logger)
	case cfg.configDir != "":
		resolved, err := config.LoadV2(ctx, cfg.configDir)
		if err != nil {
			return nil, fmt.Errorf("api: load config %q: %w", cfg.configDir, err)
		}
		logger.Info("config distribution enabled (file-backed)",
			"config_dir", cfg.configDir,
			"configurations", len(resolved.Configurations),
			"api_keys", len(resolved.APIKeys),
		)
		return &configRuntime{
			provider: controlplane.NewStoreConfigProvider(config.NewStore(resolved)),
			cleanup:  noop,
		}, nil
	default:
		logger.Info("config distribution disabled (set SLUICE_CP_DATABASE_URL or SLUICE_CONFIG_DIR to enable)")
		return &configRuntime{cleanup: noop}, nil
	}
}

func buildDBConfigProvider(ctx context.Context, cfg apiConfig, logger *slog.Logger) (*configRuntime, error) {
	db, err := configdb.Open(ctx, cfg.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("api: open config db: %w", err)
	}

	// Seed from files on first boot (no active version yet).
	if _, aerr := db.ActiveVersion(ctx); errors.Is(aerr, configdb.ErrNoActiveConfig) {
		if cfg.configDir != "" {
			if serr := seedConfigDB(ctx, db, cfg.configDir, logger); serr != nil {
				db.Close()
				return nil, serr
			}
		}
	} else if aerr != nil {
		db.Close()
		return nil, fmt.Errorf("api: read active config: %w", aerr)
	}

	// Build the served store from the active version (empty until first publish
	// when there is no seed). The write API publishes into the same store.
	resolved := &config.ResolvedConfigV2{}
	switch active, err := db.ActiveVersion(ctx); {
	case err == nil:
		rc, rerr := config.ResolveClosure(active.Body)
		if rerr != nil {
			db.Close()
			return nil, fmt.Errorf("api: resolve active config %s: %w", active.ID, rerr)
		}
		resolved = rc
		logger.Info("config distribution enabled (postgres-backed)",
			"version", active.ID,
			"hash", active.Hash,
			"configurations", len(rc.Configurations),
			"api_keys", len(rc.APIKeys),
		)
	case errors.Is(err, configdb.ErrNoActiveConfig):
		logger.Warn("config db has no active version; serving empty until a version is published")
	default:
		db.Close()
		return nil, fmt.Errorf("api: load active config: %w", err)
	}

	store := config.NewStore(resolved)
	return &configRuntime{
		provider:  controlplane.NewStoreConfigProvider(store),
		adminDB:   db,
		liveStore: store,
		cleanup:   db.Close,
	}, nil
}

// seedConfigDB imports the file config into entities and publishes the first
// version. Runs only when the store has no active version.
func seedConfigDB(ctx context.Context, db *configdb.DB, dir string, logger *slog.Logger) error {
	resolved, err := config.LoadV2(ctx, dir)
	if err != nil {
		return fmt.Errorf("api: seed load config %q: %w", dir, err)
	}
	entities, err := controlplane.EntityFromConfig(resolved)
	if err != nil {
		return fmt.Errorf("api: seed compose entities: %w", err)
	}
	for _, e := range entities {
		if err := db.UpsertEntity(ctx, e.Kind, e.Name, e.Body, "seed"); err != nil {
			return fmt.Errorf("api: seed upsert %s/%s: %w", e.Kind, e.Name, err)
		}
	}
	body, err := config.MarshalConfig(resolved)
	if err != nil {
		return fmt.Errorf("api: seed marshal config: %w", err)
	}
	v, err := db.Publish(ctx, body, "seed")
	if err != nil {
		return fmt.Errorf("api: seed publish: %w", err)
	}
	logger.Info("seeded config db from files",
		"config_dir", dir,
		"entities", len(entities),
		"version", v.ID,
	)
	return nil
}

// apiConfig is the control-plane bootstrap, env-driven to match the gateway's
// ServerEnv convention (no bootstrap YAML).
type apiConfig struct {
	httpBind      string
	grpcBind      string
	token         string
	tlsCert       string
	tlsKey        string
	configDir     string
	databaseURL   string
	adminPassword string
	staleAfter    time.Duration
	offlineAfter  time.Duration
}

func loadConfig() apiConfig {
	return apiConfig{
		httpBind:      envOr("SLUICE_CP_HTTP_BIND", defaultHTTPBind),
		grpcBind:      envOr("SLUICE_CP_GRPC_BIND", defaultGRPCBind),
		token:         os.Getenv("SLUICE_CP_TOKEN"),
		tlsCert:       os.Getenv("SLUICE_CP_TLS_CERT"),
		tlsKey:        os.Getenv("SLUICE_CP_TLS_KEY"),
		configDir:     os.Getenv("SLUICE_CONFIG_DIR"),
		databaseURL:   os.Getenv("SLUICE_CP_DATABASE_URL"),
		adminPassword: os.Getenv("SLUICE_CP_ADMIN_PASSWORD"),
		staleAfter:    envSeconds("SLUICE_CP_STALE_AFTER_SECONDS", 45*time.Second),
		offlineAfter:  envSeconds("SLUICE_CP_OFFLINE_AFTER_SECONDS", 120*time.Second),
	}
}

// tlsConfig builds the server TLS config from the cert/key pair, or returns
// (nil, nil) when neither is set (plaintext, trusted-network only). Setting
// only one is an error — a half-configured TLS is never what an operator
// intends.
func (c apiConfig) tlsConfig() (*tls.Config, error) {
	if c.tlsCert == "" && c.tlsKey == "" {
		return nil, nil
	}
	if c.tlsCert == "" || c.tlsKey == "" {
		return nil, errors.New("both SLUICE_CP_TLS_CERT and SLUICE_CP_TLS_KEY must be set")
	}
	cert, err := tls.LoadX509KeyPair(c.tlsCert, c.tlsKey)
	if err != nil {
		return nil, fmt.Errorf("load keypair: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envSeconds(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return time.Duration(n) * time.Second
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
