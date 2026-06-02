package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
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

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/crypto/bcrypt"

	contractsadmin "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/otlpingest"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/receipt"
	"github.com/andyjmorgan/sluice-gateway/internal/safego"
	"github.com/andyjmorgan/sluice-gateway/internal/version"
)

const (
	binaryName      = "api"
	defaultHTTPBind = "0.0.0.0:8484"
	defaultGRPCBind = "0.0.0.0:8485"
	receiptKeyID    = "cp-ed25519"
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
// heartbeat) and the HTTP read API the console reads. The CP is Postgres-backed
// (registry, config, versions) and runs as N stateless replicas; it refuses to
// start without a database. Cardinal invariant CP-0 holds — nothing here sits
// on a gateway's request path.
func run(ctx context.Context) error {
	logger := slog.Default()
	cfg := loadConfig()

	rt, err := buildConfigProvider(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer rt.cleanup()

	// The CP is Postgres-backed and runs as N stateless replicas, so the fleet
	// registry is the shared database — never per-process memory.
	reg := controlplane.NewDBRegistry(rt.adminDB)

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

	// OTLP trace ingest: gateways push gen_ai spans to the same gRPC channel
	// (the bootstrap token authenticates them); the receiver stores request
	// events and signs tamper-evidence receipts.
	signer, generated, err := receipt.LoadSigner(receiptKeyID, cfg.signingKey)
	if err != nil {
		return fmt.Errorf("api: load signing key: %w", err)
	}
	if generated {
		logger.Warn("no SLUICE_CP_SIGNING_KEY — generated an EPHEMERAL receipt signing key (per-replica, lost on restart); set a stable seed for fleet-wide verifiable chains",
			"public_key", signer.PublicBase64())
	} else {
		logger.Info("receipt signing key loaded", "key_id", receiptKeyID, "public_key", signer.PublicBase64())
	}
	collectortrace.RegisterTraceServiceServer(grpcSrv, otlpingest.NewReceiver(rt.adminDB, signer, logger))

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
	mux.Handle("/api/v1/config/", controlplane.NewConfigAdminHandler(rt.adminDB, logger))
	mux.Handle("/", controlplane.ConsoleHandler())

	// Admin credentials live in Postgres (shared across replicas). Seed on
	// first boot, then authenticate every request against the stored hash.
	authenticator, err := ensureAdmin(ctx, rt.adminDB, cfg.adminPassword, logger)
	if err != nil {
		return err
	}
	httpHandler := basicAuthExceptHealth(authenticator.Verify, mux)
	logger.Info("HTTP API protected by Basic auth (user: admin, credential from Postgres)")

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

// configRuntime is the assembled config source: a Postgres-backed provider that
// serves the active version per fetch, plus the DB handle the write API edits.
type configRuntime struct {
	provider controlplane.ConfigProvider
	adminDB  *configdb.DB
	cleanup  func()
}

// ensureAdmin makes the control-plane admin credential exist in Postgres and
// returns an authenticator over it. On first boot it seeds the admin: with
// SLUICE_CP_ADMIN_PASSWORD if set, otherwise a generated password logged once
// for the operator to capture. On later boots the stored hash is the source of
// truth and the env var is ignored — a password change goes through the DB, not
// the environment.
func ensureAdmin(ctx context.Context, db *configdb.DB, envPassword string, logger *slog.Logger) (*controlplane.AdminAuthenticator, error) {
	username := contractsadmin.Username

	seedPassword := envPassword
	generated := false
	if seedPassword == "" {
		p, err := randomPassword()
		if err != nil {
			return nil, err
		}
		seedPassword = p
		generated = true
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(seedPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("api: hash admin password: %w", err)
	}
	seeded, err := db.SeedAdmin(ctx, username, string(hash))
	if err != nil {
		return nil, err
	}
	switch {
	case seeded && generated:
		logger.Warn("seeded control-plane admin with a GENERATED password — store it now, it is not recoverable",
			"username", username,
			"password", seedPassword,
		)
	case seeded:
		logger.Info("seeded control-plane admin from SLUICE_CP_ADMIN_PASSWORD", "username", username)
	default:
		logger.Info("control-plane admin already provisioned in Postgres", "username", username)
	}

	// The seed may have lost a race with another replica; the DB is the source
	// of truth, so authenticate against whatever hash actually persisted.
	storedHash, err := db.GetAdminHash(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("api: load admin hash: %w", err)
	}
	return controlplane.NewAdminAuthenticator(username, storedHash), nil
}

// randomPassword returns a URL-safe random password for seeding the admin.
func randomPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("api: generate admin password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// basicAuthExceptHealth wraps next with Basic auth, leaving /healthz open so
// k8s liveness/readiness probes don't need credentials.
func basicAuthExceptHealth(verify func(username, password string) bool, next http.Handler) http.Handler {
	authed := controlplane.BasicAuth(verify, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		authed.ServeHTTP(w, r)
	})
}

// buildConfigProvider builds the Postgres-backed config source. The control
// plane is Postgres or nothing: there is no in-memory or file-backed mode, so
// every replica shares one source of truth for fleet and config state. Without
// SLUICE_CP_DATABASE_URL the CP refuses to start. SLUICE_CONFIG_DIR remains an
// optional first-boot seed into Postgres (and is the only use of files).
func buildConfigProvider(ctx context.Context, cfg apiConfig, logger *slog.Logger) (*configRuntime, error) {
	if cfg.databaseURL == "" {
		return nil, errors.New("api: SLUICE_CP_DATABASE_URL is required — the control plane is Postgres-backed (no in-memory or file-backed mode)")
	}
	return buildDBConfigProvider(ctx, cfg, logger)
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

	// Validate + log the active version at boot (fail fast on an unparseable
	// active config). The provider then reads the active version from Postgres
	// per fetch, so there is no served-config cache to keep in sync.
	switch active, err := db.ActiveVersion(ctx); {
	case err == nil:
		rc, rerr := config.ResolveClosure(active.Body)
		if rerr != nil {
			db.Close()
			return nil, fmt.Errorf("api: resolve active config %s: %w", active.ID, rerr)
		}
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

	return &configRuntime{
		provider: controlplane.NewDBConfigProvider(db),
		adminDB:  db,
		cleanup:  db.Close,
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
	signingKey    string
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
		signingKey:    os.Getenv("SLUICE_CP_SIGNING_KEY"),
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
