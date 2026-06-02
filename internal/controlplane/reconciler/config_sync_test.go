package reconciler

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane"
	fleetpb "github.com/andyjmorgan/sluice-gateway/internal/controlplane/fleetpb"
)

func startCPWithConfig(t *testing.T, provider controlplane.ConfigProvider) string {
	t.Helper()
	srv := controlplane.NewGRPCServer(newStubRegistry(), nil, controlplane.GRPCServerOptions{Config: provider})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

// devConfigStore loads the real config-dev as a CP-side config source and
// returns it plus a bootstrap api-key drawn from it. Skips when config-dev
// isn't reachable from the test's working directory.
func devConfigStore(t *testing.T) (*config.Store, string) {
	t.Helper()
	resolved, err := config.LoadV2(context.Background(), "../../../config-dev")
	if err != nil {
		t.Skipf("config-dev not loadable: %v", err)
	}
	if len(resolved.APIKeys) == 0 {
		t.Skip("config-dev has no api keys")
	}
	return config.NewStore(resolved), resolved.APIKeys[0].Secret
}

func emptyGatewayStore() *config.Store {
	return config.NewStore(&config.ResolvedConfigV2{})
}

func TestNewConfigSyncer_Validation(t *testing.T) {
	st := emptyGatewayStore()
	if _, err := NewConfigSyncer(ConfigSyncerOptions{APIKey: "k", Store: st}); err == nil {
		t.Error("want error for missing endpoint")
	}
	if _, err := NewConfigSyncer(ConfigSyncerOptions{Endpoint: "e", Store: st}); err == nil {
		t.Error("want error for missing api key")
	}
	if _, err := NewConfigSyncer(ConfigSyncerOptions{Endpoint: "e", APIKey: "k"}); err == nil {
		t.Error("want error for missing store")
	}
	s, err := NewConfigSyncer(ConfigSyncerOptions{Endpoint: "e", APIKey: "k", Store: st})
	if err != nil {
		t.Fatal(err)
	}
	if s.opts.Interval != 60*time.Second {
		t.Fatalf("default interval = %v", s.opts.Interval)
	}
}

func TestConfigSyncer_Bootstrap_AppliesCPConfig(t *testing.T) {
	cpStore, bootKey := devConfigStore(t)
	addr := startCPWithConfig(t, controlplane.NewStoreConfigProvider(cpStore))

	gwStore := emptyGatewayStore()
	cachePath := filepath.Join(t.TempDir(), "closure.yaml")
	s, err := NewConfigSyncer(ConfigSyncerOptions{
		Endpoint:  addr,
		APIKey:    bootKey,
		Store:     gwStore,
		CachePath: cachePath,
		Logger:    discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Bootstrap(context.Background())

	if snap := gwStore.Snapshot(); snap == nil || snap.SecretIndex[bootKey] == nil {
		t.Fatal("bootstrap did not apply CP config to the gateway store")
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Errorf("closure not persisted to cache: %v", err)
	}
}

func TestConfigSyncer_SyncOnce_AppliesThenNotModified(t *testing.T) {
	cpStore, bootKey := devConfigStore(t)
	addr := startCPWithConfig(t, controlplane.NewStoreConfigProvider(cpStore))

	s, err := NewConfigSyncer(ConfigSyncerOptions{Endpoint: addr, APIKey: bootKey, Store: emptyGatewayStore(), Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dialControlPlane(addr, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := fleetpb.NewFleetServiceClient(conn)

	if err := s.syncOnce(context.Background(), client); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if s.lastHash == "" {
		t.Fatal("lastHash not set after first sync")
	}
	// Second sync: known hash matches -> not_modified branch, no error.
	if err := s.syncOnce(context.Background(), client); err != nil {
		t.Fatalf("second sync (not_modified): %v", err)
	}
}

func TestConfigSyncer_Bootstrap_CacheFallback(t *testing.T) {
	cpStore, bootKey := devConfigStore(t)
	snap := cpStore.Snapshot()
	body, _, err := config.MarshalClosure(snap, snap.APIKeys[0].Configuration)
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(t.TempDir(), "closure.yaml")
	if err := os.WriteFile(cachePath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	gwStore := emptyGatewayStore()
	s, err := NewConfigSyncer(ConfigSyncerOptions{
		Endpoint:  "127.0.0.1:1", // unreachable -> fetch fails -> cache
		APIKey:    bootKey,
		Store:     gwStore,
		CachePath: cachePath,
		Logger:    discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Bootstrap(context.Background())

	if gwStore.Snapshot().SecretIndex[bootKey] == nil {
		t.Fatal("cache fallback did not apply config")
	}
}

func TestConfigSyncer_ApplyCache_NoPathOrMissingOrBad(t *testing.T) {
	gwStore := emptyGatewayStore()

	noPath, _ := NewConfigSyncer(ConfigSyncerOptions{Endpoint: "e", APIKey: "k", Store: gwStore})
	noPath.applyCache() // CachePath empty -> no-op

	missing, _ := NewConfigSyncer(ConfigSyncerOptions{Endpoint: "e", APIKey: "k", Store: gwStore, CachePath: filepath.Join(t.TempDir(), "nope.yaml")})
	missing.applyCache() // read error -> no-op

	badPath := filepath.Join(t.TempDir(), "closure.yaml")
	if err := os.WriteFile(badPath, []byte("{{{garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad, _ := NewConfigSyncer(ConfigSyncerOptions{Endpoint: "e", APIKey: "k", Store: gwStore, CachePath: badPath, Logger: discardLogger()})
	bad.applyCache() // invalid -> no-op

	if len(gwStore.Snapshot().SecretIndex) != 0 {
		t.Fatal("no valid cache should have been applied")
	}
}

// garbageProvider serves an unparsable closure body, exercising the syncer's
// resolve-failure (serve-stale) path.
type garbageProvider struct{}

func (garbageProvider) ClosureForAPIKey(string) (controlplane.Closure, error) {
	return controlplane.Closure{Configuration: "x", Hash: "h", Body: []byte("{{{garbage")}, nil
}

func TestConfigSyncer_SyncOnce_BadClosureServesStale(t *testing.T) {
	addr := startCPWithConfig(t, garbageProvider{})
	gwStore := emptyGatewayStore()
	s, err := NewConfigSyncer(ConfigSyncerOptions{Endpoint: addr, APIKey: "k", Store: gwStore, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dialControlPlane(addr, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := s.syncOnce(context.Background(), fleetpb.NewFleetServiceClient(conn)); err == nil {
		t.Fatal("want resolve error for garbage closure")
	}
	if len(gwStore.Snapshot().SecretIndex) != 0 {
		t.Fatal("a bad closure must not be applied (serve-stale)")
	}
}

func TestConfigSyncer_Run_ResyncsAndStops(t *testing.T) {
	cpStore, bootKey := devConfigStore(t)
	addr := startCPWithConfig(t, controlplane.NewStoreConfigProvider(cpStore))
	gwStore := emptyGatewayStore()
	s, err := NewConfigSyncer(ConfigSyncerOptions{Endpoint: addr, APIKey: bootKey, Store: gwStore, Interval: 20 * time.Millisecond, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	deadline := time.Now().Add(2 * time.Second)
	for gwStore.Snapshot().SecretIndex[bootKey] == nil {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("Run never applied config")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestConfigSyncer_Bootstrap_DialErrorFallsToCache(t *testing.T) {
	// "%zz" fails grpc target parsing synchronously, so Bootstrap's dial
	// errors and it falls through to the (absent) cache without panicking.
	gwStore := emptyGatewayStore()
	s, err := NewConfigSyncer(ConfigSyncerOptions{Endpoint: "%zz", APIKey: "k", Store: gwStore, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	s.Bootstrap(context.Background())
	if len(gwStore.Snapshot().SecretIndex) != 0 {
		t.Fatal("nothing should have applied (no cache, dial failed)")
	}
}

func TestConfigSyncer_Persist_MkdirError(t *testing.T) {
	// Parent of CachePath is a regular file, so MkdirAll fails — persist must
	// log and return without panicking.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewConfigSyncer(ConfigSyncerOptions{
		Endpoint:  "e",
		APIKey:    "k",
		Store:     emptyGatewayStore(),
		CachePath: filepath.Join(f, "closure.yaml"),
		Logger:    discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.persist([]byte("data")) // must not panic
}

func TestConfigSyncer_Run_DialErrorIsNonFatal(t *testing.T) {
	s, err := NewConfigSyncer(ConfigSyncerOptions{Endpoint: "%zz", APIKey: "k", Store: emptyGatewayStore(), Interval: time.Second, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return on dial error")
	}
}
