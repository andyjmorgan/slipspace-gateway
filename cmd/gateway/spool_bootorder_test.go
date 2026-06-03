package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/spool"
)

// webhookConnector returns a minimal valid webhook connector config plus the
// env wiring its secret_ref + loopback-URL SSRF guard need so factory.BuildAll
// succeeds in-process.
func webhookConnector(t *testing.T, name string) contractsconfig.Connector {
	t.Helper()
	t.Setenv("SLUICE_WEBHOOK_ALLOW_PRIVATE", "true")
	t.Setenv("SLUICE_TEST_WEBHOOK_SECRET", "shh")
	return contractsconfig.Connector{ //nolint:gosec // test fixture: secret_ref points at a test env var, not a credential
		Name:      name,
		Type:      contractsconfig.ConnectorTypeWebhook,
		URL:       "http://127.0.0.1:1/ingest",
		SecretRef: "env:SLUICE_TEST_WEBHOOK_SECRET",
		TimeoutMS: 1000,
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// TestSetupSpool_FromPostBootstrapSnapshot is the regression proof for the
// CP-managed boot-ordering bug: a gateway booted on an empty (no-connector)
// bootstrap config, then handed a control-plane closure carrying a connector,
// must wire the spool from the POST-bootstrap snapshot — not the empty
// bootstrap — so body capture turns on. It mirrors run()'s ordering: empty
// store, Replace with the CP closure, then setupSpool(store.Snapshot()).
func TestSetupSpool_FromPostBootstrapSnapshot(t *testing.T) {
	ctx := context.Background()
	logger := discardLogger()

	bootstrap := &config.ResolvedConfigV2{}
	store := config.NewStore(bootstrap)

	bootSpool, bootCleanup, err := setupSpool(ctx, &config.ServerEnv{SpoolRoot: t.TempDir()}, store.Snapshot(), logger)
	if err != nil {
		t.Fatalf("setupSpool(bootstrap): unexpected error: %v", err)
	}
	t.Cleanup(bootCleanup)
	if bootSpool != nil {
		t.Fatal("spool wired from empty bootstrap config; should be nil before the CP closure arrives")
	}

	cpClosure := &config.ResolvedConfigV2{
		Connectors: []contractsconfig.Connector{webhookConnector(t, "cp-distributed")},
	}
	store.Replace(cpClosure)

	s, cleanup, err := setupSpool(ctx, &config.ServerEnv{SpoolRoot: t.TempDir()}, store.Snapshot(), logger)
	if err != nil {
		t.Fatalf("setupSpool(post-bootstrap): unexpected error: %v", err)
	}
	t.Cleanup(cleanup)

	if s == nil {
		t.Fatal("spool is nil after post-bootstrap setup; CP-distributed connector did not enable capture")
	}
	names := s.TrackNames()
	if len(names) != 1 || names[0] != "cp-distributed" {
		t.Fatalf("track names = %v, want [cp-distributed]", names)
	}
}

// TestSetupSpool_NoConnectors covers both the non-CP file-backed gateway with
// no connectors and the CP-managed gateway whose post-bootstrap config carries
// none: the spool stays disabled and boot proceeds.
func TestSetupSpool_NoConnectors(t *testing.T) {
	ctx := context.Background()
	resolved := &config.ResolvedConfigV2{}

	s, cleanup, err := setupSpool(ctx, &config.ServerEnv{SpoolRoot: t.TempDir()}, resolved, discardLogger())
	if err != nil {
		t.Fatalf("setupSpool: unexpected error: %v", err)
	}
	t.Cleanup(cleanup)
	if s != nil {
		t.Fatalf("spool = %v, want nil with no connectors", s)
	}
}

// TestSetupSpool_FileBackedConnector proves the non-CP path is unchanged: a
// file-backed gateway whose loaded config carries a connector wires the spool
// directly (no control plane involved).
func TestSetupSpool_FileBackedConnector(t *testing.T) {
	ctx := context.Background()
	resolved := &config.ResolvedConfigV2{
		Connectors: []contractsconfig.Connector{webhookConnector(t, "file-backed")},
	}

	s, cleanup, err := setupSpool(ctx, &config.ServerEnv{SpoolRoot: t.TempDir()}, resolved, discardLogger())
	if err != nil {
		t.Fatalf("setupSpool: unexpected error: %v", err)
	}
	t.Cleanup(cleanup)
	if s == nil {
		t.Fatal("spool is nil for a file-backed connector")
	}
	if names := s.TrackNames(); len(names) != 1 || names[0] != "file-backed" {
		t.Fatalf("track names = %v, want [file-backed]", names)
	}
}

// captureRecord is one slog record captured for assertion.
type captureRecord struct {
	level slog.Level
	msg   string
}

// captureHandler is a minimal slog.Handler that records level+message so the
// body-capture-state logging decisions can be asserted without a real sink.
type captureHandler struct {
	records *[]captureRecord
}

func (h captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	*h.records = append(*h.records, captureRecord{level: r.Level, msg: r.Message})
	return nil
}

func (h captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h captureHandler) WithGroup(string) slog.Handler { return h }

func TestLogBodyCaptureState(t *testing.T) {
	ctx := context.Background()
	cpEnv := &config.ServerEnv{ //nolint:gosec // test fixture: bootstrap api key is a literal, not a real credential
		ControlPlaneEndpoint:        "cp:50051",
		ControlPlaneBootstrapAPIKey: "sk_live_boot",
	}
	fileEnv := &config.ServerEnv{}
	withConnectors := &config.ResolvedConfigV2{
		Connectors: []contractsconfig.Connector{{Name: "c", Type: contractsconfig.ConnectorTypeWebhook}},
	}
	empty := &config.ResolvedConfigV2{}

	cases := []struct {
		name      string
		env       *config.ServerEnv
		resolved  *config.ResolvedConfigV2
		spool     *spool.Spool
		wantLevel slog.Level
		wantSub   string
	}{
		{
			name:      "spool wired logs enabled at info",
			env:       cpEnv,
			resolved:  withConnectors,
			spool:     &spool.Spool{},
			wantLevel: slog.LevelInfo,
			wantSub:   "body capture enabled",
		},
		{
			name:     "file-backed no connectors stays quiet",
			env:      fileEnv,
			resolved: empty,
			spool:    nil,
			wantSub:  "",
		},
		{
			name:      "cp-managed no connectors warns capture off",
			env:       cpEnv,
			resolved:  empty,
			spool:     nil,
			wantLevel: slog.LevelWarn,
			wantSub:   "no connectors in its post-bootstrap config",
		},
		{
			name:      "cp-managed connectors but no spool warns regression",
			env:       cpEnv,
			resolved:  withConnectors,
			spool:     nil,
			wantLevel: slog.LevelWarn,
			wantSub:   "startup-ordering regression",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var recs []captureRecord
			logger := slog.New(captureHandler{records: &recs})
			logBodyCaptureState(ctx, c.env, c.resolved, c.spool, logger)

			if c.wantSub == "" {
				if len(recs) != 0 {
					t.Fatalf("expected no log records, got %+v", recs)
				}
				return
			}
			if len(recs) != 1 {
				t.Fatalf("expected one log record, got %+v", recs)
			}
			got := recs[0]
			if got.level != c.wantLevel {
				t.Errorf("level = %v, want %v", got.level, c.wantLevel)
			}
			if !strings.Contains(got.msg, c.wantSub) {
				t.Errorf("msg = %q, want substring %q", got.msg, c.wantSub)
			}
		})
	}
}
