package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/pusher"
	"github.com/andyjmorgan/slipspace-gateway/internal/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/connector/testfs"
	"github.com/andyjmorgan/slipspace-gateway/internal/spool"
)

func webhookConn(name, url string) contractsconfig.Connector {
	return contractsconfig.Connector{Name: name, Type: contractsconfig.ConnectorTypeWebhook, URL: url, SecretRef: "env:X", TimeoutMS: 1000}
}

func s3Conn(name, bucket string) contractsconfig.Connector {
	return contractsconfig.Connector{Name: name, Type: contractsconfig.ConnectorTypeS3, Bucket: bucket, Region: "eu-west-1"}
}

func names(cs []contractsconfig.Connector) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func equalStrings(a, b []string) bool {
	return slices.Equal(a, b)
}

func TestDiffConnectors(t *testing.T) {
	a := webhookConn("a", "https://a")
	b := s3Conn("b", "bucket-b")
	cases := []struct {
		name        string
		old, cur    contractsconfig.ConnectorsConfig
		wantRemoved []string
		wantAdded   []string
	}{
		{"both empty", nil, nil, nil, nil},
		{"unchanged", contractsconfig.ConnectorsConfig{a, b}, contractsconfig.ConnectorsConfig{a, b}, nil, nil},
		{"reorder only", contractsconfig.ConnectorsConfig{a, b}, contractsconfig.ConnectorsConfig{b, a}, nil, nil},
		{"added", contractsconfig.ConnectorsConfig{a}, contractsconfig.ConnectorsConfig{a, b}, nil, []string{"b"}},
		{"removed", contractsconfig.ConnectorsConfig{a, b}, contractsconfig.ConnectorsConfig{a}, []string{"b"}, nil},
		{"edited is remove+add", contractsconfig.ConnectorsConfig{a, b}, contractsconfig.ConnectorsConfig{webhookConn("a", "https://a2"), b}, []string{"a"}, []string{"a"}},
		{"type change is remove+add", contractsconfig.ConnectorsConfig{b}, contractsconfig.ConnectorsConfig{webhookConn("b", "https://b")}, []string{"b"}, []string{"b"}},
		{"rotation sub-struct change", contractsconfig.ConnectorsConfig{b}, contractsconfig.ConnectorsConfig{func() contractsconfig.Connector {
			c := b
			c.Rotation = &contractsconfig.ConnectorRotation{MaxBytes: 1}
			return c
		}()}, []string{"b"}, []string{"b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := diffConnectors(c.old, c.cur)
			if got := names(d.removed); !equalStrings(got, c.wantRemoved) {
				t.Errorf("removed = %v, want %v", got, c.wantRemoved)
			}
			if got := names(d.added); !equalStrings(got, c.wantAdded) {
				t.Errorf("added = %v, want %v", got, c.wantAdded)
			}
			if d.empty() != (len(c.wantRemoved) == 0 && len(c.wantAdded) == 0) {
				t.Errorf("empty() = %v inconsistent with %v/%v", d.empty(), c.wantRemoved, c.wantAdded)
			}
		})
	}
}

func TestPusherSet_NilSafeAndLifecycle(t *testing.T) {
	var nilSet *pusherSet
	if nilSet.get("x") != nil {
		t.Error("nil set get should be nil")
	}
	if nilSet.names() != nil {
		t.Error("nil set names should be nil")
	}
	nilSet.closeAll(time.Second) // must not panic

	set := newPusherSet(nil)
	p := pusher.New(pusher.Options{Endpoint: "http://127.0.0.1:0", Logger: discardLogger(), Workers: 1})
	set.put("a", p)
	if set.get("a") != p {
		t.Error("get after put returned a different pusher")
	}
	if got := set.names(); !equalStrings(got, []string{"a"}) {
		t.Errorf("names = %v", got)
	}
	if set.remove("a") != p || set.get("a") != nil || set.remove("a") != nil {
		t.Error("remove semantics broken")
	}
	set.put("b", p)
	set.closeAll(time.Second)
	if len(set.names()) != 0 {
		t.Error("closeAll should empty the set")
	}
	if p.Enqueue(cc.Record{}) {
		t.Error("closed pusher should refuse Enqueue")
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// reconcilerFixture wires a sinkReconciler over a real spool (testfs
// connectors) and real pushers (pointing at an httptest receiver) with
// counting builders, so tests can assert what was built and what landed.
type reconcilerFixture struct {
	r             *sinkReconciler
	spool         *spool.Spool
	pushers       *pusherSet
	destDir       string
	received      atomic.Int64
	pusherBuilds  atomic.Int64
	connBuilds    atomic.Int64
	failConnector atomic.Bool
	failPusher    atomic.Bool
}

func newReconcilerFixture(t *testing.T, boot contractsconfig.ConnectorsConfig, withSpool bool) *reconcilerFixture {
	t.Helper()
	f := &reconcilerFixture{destDir: t.TempDir(), pushers: newPusherSet(nil)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.received.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if withSpool {
		s, err := spool.New(spool.Options{Root: t.TempDir(), Logger: discardLogger()})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Stop(2 * time.Second) })
		f.spool = s
	}
	f.r = &sinkReconciler{
		ctx:     ctx,
		logger:  discardLogger(),
		spool:   f.spool,
		pushers: f.pushers,
		buildConnector: func(_ context.Context, cfg contractsconfig.Connector) (connector.Connector, error) {
			f.connBuilds.Add(1)
			if f.failConnector.Load() {
				return nil, errors.New("synthetic build failure")
			}
			return testfs.New(testfs.Options{Name: cfg.Name, Dir: f.destDir})
		},
		buildPusher: func(cfg contractsconfig.Connector) (*pusher.Pusher, error) {
			f.pusherBuilds.Add(1)
			if f.failPusher.Load() {
				return nil, errors.New("synthetic secret failure")
			}
			return pusher.New(pusher.Options{Endpoint: srv.URL, GatewayID: cfg.GatewayID, Secret: "s", Logger: discardLogger(), Workers: 1}), nil
		},
		unregisterTimeout: 2 * time.Second,
		last:              boot,
	}
	t.Cleanup(func() { f.pushers.closeAll(time.Second) })
	return f
}

func snapshotWith(conns ...contractsconfig.Connector) *config.ResolvedConfig {
	return &config.ResolvedConfig{Connectors: conns}
}

func TestSinkReconciler_ImmediateSubscribeCallIsNoop(t *testing.T) {
	boot := contractsconfig.ConnectorsConfig{webhookConn("boot", "https://boot")}
	f := newReconcilerFixture(t, boot, true)
	store := config.NewStore(snapshotWith(boot...))
	store.Subscribe(f.r.onSnapshot)
	if f.pusherBuilds.Load() != 0 || f.connBuilds.Load() != 0 {
		t.Errorf("Subscribe's immediate call rebuilt boot sinks: pushers=%d conns=%d", f.pusherBuilds.Load(), f.connBuilds.Load())
	}
	f.r.onSnapshot(nil) // must not panic
}

func TestSinkReconciler_WebhookCreateEditDelete(t *testing.T) {
	f := newReconcilerFixture(t, nil, false)
	hook := webhookConn("hook", "https://one")

	// Create: the pusher exists as soon as onSnapshot returns and delivers.
	f.r.onSnapshot(snapshotWith(hook))
	p1 := f.pushers.get("hook")
	if p1 == nil {
		t.Fatal("created webhook connector has no pusher")
	}
	if !p1.Enqueue(cc.Record{ID: "r1"}) {
		t.Fatal("new pusher refused a record")
	}
	deadline := time.Now().Add(3 * time.Second)
	for f.received.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.received.Load() < 1 {
		t.Fatal("record never reached the receiver through the live-built pusher")
	}

	// Edit: the old pusher is closed and a fresh one takes its name.
	f.r.onSnapshot(snapshotWith(webhookConn("hook", "https://two")))
	p2 := f.pushers.get("hook")
	if p2 == nil || p2 == p1 {
		t.Fatalf("edit did not swap the pusher: p1=%p p2=%p", p1, p2)
	}
	if p1.Enqueue(cc.Record{}) {
		t.Error("old pusher should be closed after an edit")
	}
	if got := f.pusherBuilds.Load(); got != 2 {
		t.Errorf("pusher builds = %d, want 2", got)
	}

	// Unchanged snapshot: nothing rebuilt.
	f.r.onSnapshot(snapshotWith(webhookConn("hook", "https://two")))
	if got := f.pusherBuilds.Load(); got != 2 {
		t.Errorf("unchanged snapshot rebuilt the pusher: builds=%d", got)
	}

	// Delete: unrouted immediately, pusher closed.
	f.r.onSnapshot(snapshotWith())
	if f.pushers.get("hook") != nil {
		t.Error("deleted webhook connector still has a pusher")
	}
	if p2.Enqueue(cc.Record{}) {
		t.Error("removed pusher should be closed")
	}
}

func TestSinkReconciler_SpoolCreateEditDelete(t *testing.T) {
	f := newReconcilerFixture(t, nil, true)
	arch := s3Conn("arch", "bucket-one")

	f.r.onSnapshot(snapshotWith(arch))
	if got := f.spool.TrackNames(); !equalStrings(got, []string{"arch"}) {
		t.Fatalf("TrackNames after create = %v, want [arch]", got)
	}
	f.spool.Enqueue(cc.Record{V: 1, ID: "x", TsNs: time.Now().UnixNano(), SchemaVersion: cc.SchemaVersion,
		Request: cc.RequestPart{Method: "POST", Path: "/x"}, Response: cc.ResponsePart{Status: 200}}, "arch")
	if st := f.spool.Stats(); st.Tracks["arch"].Enqueued != 1 {
		t.Errorf("record enqueued after live create was not accepted: %+v", st.Tracks["arch"])
	}

	// Edit: unregister-then-register under the same name; one more build.
	f.r.onSnapshot(snapshotWith(s3Conn("arch", "bucket-two")))
	if got := f.spool.TrackNames(); !equalStrings(got, []string{"arch"}) {
		t.Fatalf("TrackNames after edit = %v, want [arch]", got)
	}
	if got := f.connBuilds.Load(); got != 2 {
		t.Errorf("connector builds = %d, want 2", got)
	}
	// The edited track's records/<name>/ directory is shared, so the
	// pre-edit record was flushed by the old track's drain and is on disk
	// for the new track to ship.
	matches, _ := filepath.Glob(filepath.Join(f.destDir, "records", "instance=*", "date=*", "hour=*", "*.ndjson.zst"))
	deadline := time.Now().Add(3 * time.Second)
	for len(matches) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		matches, _ = filepath.Glob(filepath.Join(f.destDir, "records", "instance=*", "date=*", "hour=*", "*.ndjson.zst"))
	}
	if len(matches) == 0 {
		t.Errorf("record enqueued before the edit never reached the destination; stats=%+v", f.spool.Stats().Tracks["arch"])
	}

	// Delete: track gone, later records counted as unrouted.
	f.r.onSnapshot(snapshotWith())
	if got := f.spool.TrackNames(); len(got) != 0 {
		t.Fatalf("TrackNames after delete = %v, want empty", got)
	}
	f.spool.Enqueue(cc.Record{ID: "late"}, "arch")
	if got := f.spool.Stats().Unrouted["arch"]; got != 1 {
		t.Errorf("Unrouted[arch] = %d, want 1", got)
	}
}

func TestSinkReconciler_BuildFailuresAreNonFatal(t *testing.T) {
	f := newReconcilerFixture(t, nil, true)
	f.failConnector.Store(true)
	f.failPusher.Store(true)

	f.r.onSnapshot(snapshotWith(s3Conn("arch", "b"), webhookConn("hook", "https://h")))
	if got := f.spool.TrackNames(); len(got) != 0 {
		t.Errorf("failed connector build still registered a track: %v", got)
	}
	if f.pushers.get("hook") != nil {
		t.Error("failed pusher build still registered a pusher")
	}
	// The snapshot is remembered even so: a retry needs a real change.
	f.failConnector.Store(false)
	f.r.onSnapshot(snapshotWith(s3Conn("arch", "b"), webhookConn("hook", "https://h")))
	if got := f.connBuilds.Load(); got != 1 {
		t.Errorf("unchanged snapshot retried the build: builds=%d", got)
	}
	f.r.onSnapshot(snapshotWith(s3Conn("arch", "b2"), webhookConn("hook", "https://h")))
	if got := f.spool.TrackNames(); !equalStrings(got, []string{"arch"}) {
		t.Errorf("edited connector did not register after the earlier failure: %v", got)
	}
	// Removing a connector that never got a sink is a no-op, not an error.
	f.r.onSnapshot(snapshotWith(s3Conn("arch", "b2")))
	f.r.onSnapshot(snapshotWith())
}

func TestSinkReconciler_NoSpoolIsSafe(t *testing.T) {
	f := newReconcilerFixture(t, nil, false)
	f.r.onSnapshot(snapshotWith(s3Conn("arch", "b")))
	f.r.onSnapshot(snapshotWith())
	if f.connBuilds.Load() != 0 {
		t.Error("connector built with no spool to register it into")
	}
}

func TestSpoolStatsAdapter_Snapshot(t *testing.T) {
	s, err := spool.New(spool.Options{Root: t.TempDir(), Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	c, err := testfs.New(testfs.Options{Name: "real", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterTrack(spool.RegisterTrackOptions{Connector: c, QueueSize: 1}); err != nil {
		t.Fatal(err)
	}
	// Not started: records sit on the ring so the second one drops.
	s.Enqueue(cc.Record{}, "real")
	s.Enqueue(cc.Record{}, "real")
	s.Enqueue(cc.Record{}, "ghost")

	rows := spoolStatsAdapter{spool: s}.Snapshot()
	if len(rows) != 2 || rows[0].Connector != "ghost" || rows[1].Connector != "real" {
		t.Fatalf("rows = %+v, want [ghost real] sorted", rows)
	}
	ghost, real := rows[0], rows[1]
	if ghost.Registered || ghost.DroppedNoTrack != 1 {
		t.Errorf("ghost row = %+v, want unregistered with 1 no_track drop", ghost)
	}
	if !real.Registered || real.Enqueued != 1 || real.DroppedRing != 1 || real.BreakerStateName != "closed" || real.BreakerState != 0 {
		t.Errorf("real row = %+v", real)
	}
}

func TestClampUint64(t *testing.T) {
	if got := clampUint64(42); got != 42 {
		t.Errorf("clampUint64(42) = %d", got)
	}
	if got := clampUint64(^uint64(0)); got != int64(^uint64(0)>>1) {
		t.Errorf("clampUint64(max) = %d, want saturated int64 max", got)
	}
}

func TestRegisterSpoolInstruments_NilGuards(t *testing.T) {
	if err := registerSpoolInstruments(nil, nil); err != nil {
		t.Errorf("nil provider + nil spool: %v", err)
	}
	s, err := spool.New(spool.Options{Root: t.TempDir(), Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if err := registerSpoolInstruments(nil, s); err != nil {
		t.Errorf("nil provider: %v", err)
	}
}
