package reconciler

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"log/slog"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	fleetpb "github.com/andyjmorgan/sluice-gateway/internal/controlplane/fleetpb"
)

// ConfigSyncerOptions configures a ConfigSyncer.
type ConfigSyncerOptions struct {
	// Endpoint, Token, TLS, TLSConfig mirror the reconciler's CP connection.
	Endpoint  string
	Token     string
	TLS       bool
	TLSConfig *tls.Config

	// APIKey is the bootstrap Sluice api-key whose per-configuration closure
	// this gateway serves. The closure carries every api-key of that
	// configuration, so one bootstrap key loads the whole configuration.
	APIKey string

	// CachePath is the file the last-good closure is persisted to, so a
	// restart while the CP is down still comes up on last-known-good. Empty
	// disables the disk cache.
	CachePath string

	// Interval is the resync cadence. Defaults to 60s when non-positive.
	Interval time.Duration

	// Store is the live config store the syncer Replaces on a new closure.
	Store *config.Store

	// Applied, when set, receives the hash of each applied closure so the
	// reconciler can report it on heartbeat (control-plane drift detection).
	Applied *AppliedHash

	// Logger is used for out-of-band status. May be nil.
	Logger *slog.Logger
}

// ConfigSyncer keeps a gateway's live config in sync with the control plane:
// it fetches the bootstrap key's closure, validates it, and Replaces the store,
// persisting the last-good closure to disk for serve-stale across restarts.
//
// CP-0: every operation is out of band. A fetch failure, a dial failure, or an
// invalid closure leaves the current snapshot in place (serve-stale) — config
// sync never blocks a request and never fails the gateway.
type ConfigSyncer struct {
	opts     ConfigSyncerOptions
	lastHash string
}

// NewConfigSyncer validates opts and constructs a ConfigSyncer.
func NewConfigSyncer(opts ConfigSyncerOptions) (*ConfigSyncer, error) {
	if opts.Endpoint == "" {
		return nil, errors.New("config syncer: endpoint is required")
	}
	if opts.APIKey == "" {
		return nil, errors.New("config syncer: bootstrap api key is required")
	}
	if opts.Store == nil {
		return nil, errors.New("config syncer: store is required")
	}
	if opts.Interval <= 0 {
		opts.Interval = 60 * time.Second
	}
	return &ConfigSyncer{opts: opts}, nil
}

// Bootstrap performs one best-effort sync before the gateway starts serving:
// it fetches and applies the closure, or falls back to the on-disk cache. It
// never fails — an unreachable CP with no cache simply leaves the store on
// whatever the gateway already loaded (CP-0).
func (s *ConfigSyncer) Bootstrap(ctx context.Context) {
	conn, err := dialControlPlane(s.opts.Endpoint, s.opts.Token, s.opts.TLS, s.opts.TLSConfig)
	if err != nil {
		s.logWarn("config bootstrap dial failed; trying disk cache", err)
		s.applyCache()
		return
	}
	defer func() { _ = conn.Close() }()

	if err := s.syncOnce(ctx, fleetpb.NewFleetServiceClient(conn)); err != nil {
		s.logWarn("config bootstrap fetch failed; trying disk cache", err)
		s.applyCache()
	}
}

// Run resyncs on Interval until ctx is cancelled. A dial failure ends the loop
// (the gateway keeps serving its current config); per-tick fetch failures are
// logged and the current snapshot is kept (serve-stale).
func (s *ConfigSyncer) Run(ctx context.Context) {
	conn, err := dialControlPlane(s.opts.Endpoint, s.opts.Token, s.opts.TLS, s.opts.TLSConfig)
	if err != nil {
		s.logError("config sync dial failed; resync disabled", err)
		return
	}
	defer func() { _ = conn.Close() }()
	client := fleetpb.NewFleetServiceClient(conn)

	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.syncOnce(ctx, client); err != nil {
				s.logWarn("config sync failed; serving stale", err)
			}
		}
	}
}

// syncOnce fetches the closure conditional on lastHash. not_modified is a
// no-op; a new body is resolved + validated (CP-2) and applied, then persisted.
// A resolve/validate failure aborts the apply so a bad closure never reaches
// the live snapshot.
func (s *ConfigSyncer) syncOnce(ctx context.Context, client fleetpb.FleetServiceClient) error {
	callCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	resp, err := client.FetchConfig(callCtx, &fleetpb.FetchConfigRequest{
		ApiKey:    s.opts.APIKey,
		KnownHash: s.lastHash,
	})
	if err != nil {
		return err
	}
	if resp.GetNotModified() {
		return nil
	}

	resolved, err := config.ResolveClosure(resp.GetBody())
	if err != nil {
		return fmt.Errorf("resolve closure: %w", err)
	}
	s.opts.Store.Replace(resolved)
	s.lastHash = resp.GetHash()
	s.opts.Applied.Set(resp.GetHash())
	s.persist(resp.GetBody())
	if s.opts.Logger != nil {
		s.opts.Logger.Info("applied config from control plane",
			"configuration", resp.GetConfiguration(),
			"hash", resp.GetHash(),
		)
	}
	return nil
}

// applyCache loads the last-good closure from disk and applies it. Missing or
// invalid cache is a no-op (the gateway keeps its current config).
func (s *ConfigSyncer) applyCache() {
	if s.opts.CachePath == "" {
		return
	}
	data, err := os.ReadFile(s.opts.CachePath) //nolint:gosec // cache path is operator-configured
	if err != nil {
		return
	}
	resolved, err := config.ResolveClosure(data)
	if err != nil {
		s.logWarn("config disk cache invalid; ignoring", err)
		return
	}
	s.opts.Store.Replace(resolved)
	if s.opts.Logger != nil {
		s.opts.Logger.Info("applied config from disk cache", "path", s.opts.CachePath)
	}
}

// persist writes body to CachePath atomically (temp + rename).
func (s *ConfigSyncer) persist(body []byte) {
	if s.opts.CachePath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.opts.CachePath), 0o700); err != nil {
		s.logWarn("config cache mkdir failed", err)
		return
	}
	tmp := s.opts.CachePath + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		s.logWarn("config cache write failed", err)
		return
	}
	if err := os.Rename(tmp, s.opts.CachePath); err != nil {
		s.logWarn("config cache rename failed", err)
	}
}

func (s *ConfigSyncer) logWarn(msg string, err error) {
	if s.opts.Logger != nil {
		s.opts.Logger.Warn(msg, "endpoint", s.opts.Endpoint, "err", err.Error())
	}
}

func (s *ConfigSyncer) logError(msg string, err error) {
	if s.opts.Logger != nil {
		s.opts.Logger.Error(msg, "endpoint", s.opts.Endpoint, "err", err.Error())
	}
}
