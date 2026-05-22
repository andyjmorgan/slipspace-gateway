package spool

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/connector/testfs"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// ---------- New / RegisterTrack ----------

func TestNew_RequiresRoot(t *testing.T) {
	_, err := New(Options{})
	if err == nil {
		t.Error("expected error for empty Root")
	}
}

func TestNew_AppliesDefaults(t *testing.T) {
	s, err := New(Options{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if s.logger == nil {
		t.Error("logger should default")
	}
	if s.now == nil {
		t.Error("now should default")
	}
}

func TestRegisterTrack_RequiresConnector(t *testing.T) {
	s := newTestSpool(t)
	err := s.RegisterTrack(RegisterTrackOptions{})
	if err == nil {
		t.Error("expected error for missing Connector")
	}
}

func TestRegisterTrack_RejectsDuplicates(t *testing.T) {
	s := newTestSpool(t)
	c := newTestfs(t, "dup", t.TempDir())
	if err := s.RegisterTrack(RegisterTrackOptions{Connector: c}); err != nil {
		t.Fatal(err)
	}
	err := s.RegisterTrack(RegisterTrackOptions{Connector: c})
	if err == nil {
		t.Error("expected duplicate registration to error")
	}
}

func TestRegisterTrack_RejectsAfterStart(t *testing.T) {
	s := newTestSpool(t)
	if err := s.RegisterTrack(RegisterTrackOptions{Connector: newTestfs(t, "a", t.TempDir())}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop(2 * time.Second) })
	err := s.RegisterTrack(RegisterTrackOptions{Connector: newTestfs(t, "b", t.TempDir())})
	if err == nil {
		t.Error("RegisterTrack after Start should error")
	}
}

func TestRegisterTrack_RejectsEmptyName(t *testing.T) {
	s := newTestSpool(t)
	err := s.RegisterTrack(RegisterTrackOptions{Connector: &namedFake{name: ""}})
	if err == nil {
		t.Error("expected empty-name rejection")
	}
}

// ---------- Start / Stop ----------

func TestStart_RejectsWithoutTracks(t *testing.T) {
	s := newTestSpool(t)
	err := s.Start(context.Background())
	if err == nil {
		t.Error("expected error starting without tracks")
	}
}

func TestStart_RejectsDoubleStart(t *testing.T) {
	s := newTestSpool(t)
	if err := s.RegisterTrack(RegisterTrackOptions{Connector: newTestfs(t, "a", t.TempDir())}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop(2 * time.Second) })
	if err := s.Start(ctx); err == nil {
		t.Error("expected double-Start error")
	}
}

// ---------- End-to-end: testfs ----------

func TestSpool_RecordsLandInTestfsDestination(t *testing.T) {
	root := t.TempDir()
	destDir := t.TempDir()
	c := newTestfs(t, "ship-it", destDir)

	s := mustSpool(t, Options{Root: root, Logger: discardLogger()})
	mustRegister(t, s, RegisterTrackOptions{
		Connector:          c,
		Rotation:           RotationOpts{MaxBytes: 1 << 16, MaxAge: 100 * time.Millisecond},
		UploadPollInterval: 50 * time.Millisecond,
	})
	startStop(t, s)

	for i := 0; i < 5; i++ {
		s.Enqueue(makeTestRecord("rec", uint64(i+1)), "ship-it")
	}

	// Force rotation by sleeping past MaxAge, then wait for upload.
	if !waitFor(2*time.Second, func() bool {
		matches, _ := filepath.Glob(filepath.Join(destDir, "records", "instance=*", "date=*", "hour=*", "*.ndjson.zst"))
		return len(matches) >= 1
	}) {
		t.Fatal("no segment landed in testfs destination")
	}
}

func TestSpool_UnknownConnectorNameIsIgnored(t *testing.T) {
	s := mustSpool(t, Options{Root: t.TempDir(), Logger: discardLogger()})
	mustRegister(t, s, RegisterTrackOptions{Connector: newTestfs(t, "real", t.TempDir())})
	startStop(t, s)

	// Should not panic or leak — just silently drop.
	s.Enqueue(makeTestRecord("x", 1), "does-not-exist")
}

func TestSpool_EnqueueWithNoConnectorsIsNoop(t *testing.T) {
	s := mustSpool(t, Options{Root: t.TempDir(), Logger: discardLogger()})
	mustRegister(t, s, RegisterTrackOptions{Connector: newTestfs(t, "real", t.TempDir())})
	startStop(t, s)
	s.Enqueue(makeTestRecord("x", 1)) // no names
	// Stats should show zero enqueues on the track.
	st := s.Stats()
	if got := st.Tracks["real"].Enqueued; got != 0 {
		t.Errorf("expected 0 enqueued, got %d", got)
	}
}

func TestSpool_QueueFullDropsAtRing(t *testing.T) {
	s := mustSpool(t, Options{Root: t.TempDir(), Logger: discardLogger()})
	c := newTestfs(t, "tiny", t.TempDir())
	mustRegister(t, s, RegisterTrackOptions{
		Connector: c,
		QueueSize: 1,
		Rotation:  RotationOpts{MaxBytes: 1 << 30, MaxAge: time.Hour},
	})
	// Don't Start — leave records sitting in the queue so the second
	// Enqueue overflows.
	s.Enqueue(makeTestRecord("a", 1), "tiny")
	s.Enqueue(makeTestRecord("b", 2), "tiny")
	st := s.Stats()
	if st.Tracks["tiny"].Enqueued != 1 || st.Tracks["tiny"].DroppedRing != 1 {
		t.Errorf("expected 1 enqueued + 1 dropped, got %+v", st.Tracks["tiny"])
	}
}

// ---------- Rotation: size-based ----------

func TestSpool_RotatesOnSizeCap(t *testing.T) {
	root := t.TempDir()
	destDir := t.TempDir()
	c := newTestfs(t, "rot", destDir)
	s := mustSpool(t, Options{Root: root, Logger: discardLogger()})
	mustRegister(t, s, RegisterTrackOptions{
		Connector:          c,
		Rotation:           RotationOpts{MaxBytes: 200, MaxAge: time.Hour}, // tiny — rotates after a few records
		UploadPollInterval: 50 * time.Millisecond,
	})
	startStop(t, s)

	for i := 0; i < 20; i++ {
		s.Enqueue(makeTestRecord("r", uint64(i+1)), "rot")
	}

	// Should produce >1 segment because of size-based rotation.
	if !waitFor(2*time.Second, func() bool {
		matches, _ := filepath.Glob(filepath.Join(destDir, "records", "instance=*", "date=*", "hour=*", "*.ndjson.zst"))
		return len(matches) >= 2
	}) {
		t.Errorf("expected >=2 sealed segments, got fewer")
	}
}

// ---------- Permanent error path ----------

func TestSpool_PermanentUploadErrorMovesToDeadletter(t *testing.T) {
	root := t.TempDir()
	c := &namedFake{name: "fails-perm", failWith: &cc.Permanent{Err: errors.New("bad auth")}}
	s := mustSpool(t, Options{Root: root, Logger: discardLogger()})
	mustRegister(t, s, RegisterTrackOptions{
		Connector:          c,
		Rotation:           RotationOpts{MaxBytes: 1 << 16, MaxAge: 50 * time.Millisecond},
		UploadPollInterval: 25 * time.Millisecond,
	})
	startStop(t, s)

	s.Enqueue(makeTestRecord("p", 1), "fails-perm")

	dlDir := filepath.Join(root, "records", "fails-perm", "deadletter")
	if !waitFor(2*time.Second, func() bool {
		entries, _ := os.ReadDir(dlDir)
		return len(entries) >= 1
	}) {
		t.Fatal("permanent failure did not reach deadletter")
	}
	if c.Calls() == 0 {
		t.Error("connector should have been called at least once")
	}
	// Stats reflect the DLQ outcome.
	if got := s.Stats().Tracks["fails-perm"].UploadsDLQ; got == 0 {
		t.Error("UploadsDLQ should be > 0")
	}
}

// ---------- Retryable error path with eventual success ----------

func TestSpool_RetryableThenSuccessUploadsOnce(t *testing.T) {
	root := t.TempDir()
	c := &namedFake{
		name:           "flaky",
		failTimes:      2,
		failWith:       &cc.Retryable{Err: errors.New("transient")},
		afterFailures:  successAfterFailures,
		failureBackoff: 0,
	}
	s := mustSpool(t, Options{Root: root, Logger: discardLogger()})
	mustRegister(t, s, RegisterTrackOptions{
		Connector: c,
		Rotation:  RotationOpts{MaxBytes: 1 << 16, MaxAge: 50 * time.Millisecond},
		Retry: RetryOpts{
			BaseBackoff: time.Millisecond,
			MaxBackoff:  10 * time.Millisecond,
			MaxAttempts: 8,
			Multiplier:  2.0,
		},
		UploadPollInterval: 25 * time.Millisecond,
	})
	startStop(t, s)

	s.Enqueue(makeTestRecord("r", 1), "flaky")

	if !waitFor(2*time.Second, func() bool {
		return s.Stats().Tracks["flaky"].UploadsOK >= 1
	}) {
		t.Fatalf("never observed successful upload, stats=%+v", s.Stats().Tracks["flaky"])
	}
	if c.Calls() < 3 {
		t.Errorf("connector should have retried; calls=%d", c.Calls())
	}
}

// ---------- Retryable exhausts retries → DLQ ----------

func TestSpool_RetryableExhaustsToDeadletter(t *testing.T) {
	root := t.TempDir()
	c := &namedFake{
		name:          "always-retry",
		failWith:      &cc.Retryable{Err: errors.New("transient forever")},
		afterFailures: keepFailing,
	}
	s := mustSpool(t, Options{Root: root, Logger: discardLogger()})
	mustRegister(t, s, RegisterTrackOptions{
		Connector: c,
		Rotation:  RotationOpts{MaxBytes: 1 << 16, MaxAge: 30 * time.Millisecond},
		Retry: RetryOpts{
			BaseBackoff: time.Microsecond,
			MaxBackoff:  time.Millisecond,
			MaxAttempts: 3,
			Multiplier:  2.0,
		},
		UploadPollInterval: 20 * time.Millisecond,
	})
	startStop(t, s)

	s.Enqueue(makeTestRecord("r", 1), "always-retry")

	dlDir := filepath.Join(root, "records", "always-retry", "deadletter")
	if !waitFor(3*time.Second, func() bool {
		entries, _ := os.ReadDir(dlDir)
		return len(entries) >= 1
	}) {
		t.Fatalf("retryable failures never moved to deadletter; calls=%d stats=%+v", c.Calls(), s.Stats().Tracks["always-retry"])
	}
	if c.Calls() < 3 {
		t.Errorf("expected MaxAttempts=3 calls, got %d", c.Calls())
	}
}

// ---------- Write error → handleRecord error branch ----------

func TestSpool_InvalidRecordIncrementsWriteErrors(t *testing.T) {
	root := t.TempDir()
	c := newTestfs(t, "errs", t.TempDir())
	s := mustSpool(t, Options{Root: root, Logger: discardLogger()})
	mustRegister(t, s, RegisterTrackOptions{
		Connector: c,
		Rotation:  RotationOpts{MaxBytes: 1 << 30, MaxAge: time.Hour}, // no rotation
	})
	startStop(t, s)

	bad := makeTestRecord("bad", 1)
	bad.Request.Body = []byte("not json {")
	s.Enqueue(bad, "errs")

	if !waitFor(2*time.Second, func() bool {
		return s.Stats().Tracks["errs"].WriteErrors >= 1
	}) {
		t.Errorf("WriteErrors should be > 0 after invalid record")
	}
}

// ---------- UploadAttemptTimeout wraps the per-call ctx ----------

func TestSpool_UploadAttemptTimeoutWrapsContext(t *testing.T) {
	root := t.TempDir()
	c := &namedFake{
		name:           "slow-and-failing",
		failWith:       &cc.Retryable{Err: errors.New("transient")},
		afterFailures:  keepFailing,
		failureBackoff: 200 * time.Millisecond, // longer than UploadAttemptTimeout
	}
	s := mustSpool(t, Options{Root: root, Logger: discardLogger()})
	mustRegister(t, s, RegisterTrackOptions{
		Connector:            c,
		Rotation:             RotationOpts{MaxBytes: 1 << 16, MaxAge: 30 * time.Millisecond},
		UploadAttemptTimeout: 30 * time.Millisecond,
		UploadPollInterval:   20 * time.Millisecond,
		Retry: RetryOpts{
			BaseBackoff: time.Microsecond,
			MaxBackoff:  time.Microsecond,
			MaxAttempts: 2,
			Multiplier:  2.0,
		},
	})
	startStop(t, s)

	s.Enqueue(makeTestRecord("t", 1), "slow-and-failing")

	// We don't care WHICH error path fires — the timeout might surface
	// as ctx.Err() (Retryable) or be turned into the connector's
	// Retryable. Either way the track should retry and eventually DLQ.
	dlDir := filepath.Join(root, "records", "slow-and-failing", "deadletter")
	if !waitFor(3*time.Second, func() bool {
		entries, _ := os.ReadDir(dlDir)
		return len(entries) >= 1
	}) {
		t.Errorf("UploadAttemptTimeout path did not eventually DLQ")
	}
}

// ---------- RegisterTrack manager-creation failure ----------

func TestRegisterTrack_ManagerCreationFailureSurfaces(t *testing.T) {
	root := t.TempDir()
	// Drop a regular file at the path where the manager would mkdir
	// 'records/blocked/active'. The mkdir collides.
	blocker := filepath.Join(root, "records")
	if err := os.MkdirAll(blocker, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocker, "blocked"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := mustSpool(t, Options{Root: root, Logger: discardLogger()})
	err := s.RegisterTrack(RegisterTrackOptions{Connector: &namedFake{name: "blocked"}})
	if err == nil {
		t.Error("expected manager-creation failure")
	}
}

// ---------- deliveryIDFromFilename ----------

func TestDeliveryIDFromFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"1715000000000000001-42.ndjson.zst", "1715000000000000001-42"},
		{"missing-ext", "missing-ext"},
		{"short.zst", "short.zst"},
		{"only.ndjson.zst", "only"},
	}
	for _, tc := range cases {
		if got := deliveryIDFromFilename(tc.in); got != tc.want {
			t.Errorf("deliveryIDFromFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------- Stats / TrackNames ----------

func TestSpool_TrackNamesSorted(t *testing.T) {
	s := mustSpool(t, Options{Root: t.TempDir(), Logger: discardLogger()})
	mustRegister(t, s, RegisterTrackOptions{Connector: newTestfs(t, "zebra", t.TempDir())})
	mustRegister(t, s, RegisterTrackOptions{Connector: newTestfs(t, "alpha", t.TempDir())})
	mustRegister(t, s, RegisterTrackOptions{Connector: newTestfs(t, "mango", t.TempDir())})

	names := s.TrackNames()
	want := []string{"alpha", "mango", "zebra"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("TrackNames = %v, want %v", names, want)
	}
}

// ---------- Helpers ----------

func newTestSpool(t *testing.T) *Spool {
	t.Helper()
	return mustSpool(t, Options{Root: t.TempDir(), Logger: discardLogger()})
}

func mustSpool(t *testing.T, opts Options) *Spool {
	t.Helper()
	s, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func mustRegister(t *testing.T, s *Spool, opts RegisterTrackOptions) {
	t.Helper()
	if err := s.RegisterTrack(opts); err != nil {
		t.Fatalf("RegisterTrack: %v", err)
	}
}

func newTestfs(t *testing.T, name, dir string) *testfs.Connector {
	t.Helper()
	c, err := testfs.New(testfs.Options{Name: name, Dir: dir})
	if err != nil {
		t.Fatalf("testfs.New: %v", err)
	}
	return c
}

func startStop(t *testing.T, s *Spool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		s.Stop(3 * time.Second)
	})
}

func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func makeTestRecord(id string, seq uint64) cc.Record {
	return cc.Record{
		V:             1,
		ID:            id,
		TsNs:          time.Now().UnixNano(),
		Seq:           seq,
		InstanceID:    "spool-test",
		CorrelationID: "c-" + id,
		Configuration: "test",
		Provider:      "openai",
		Endpoint:      "chat_completions",
		Request:       cc.RequestPart{Method: "POST", Path: "/x"},
		Response:      cc.ResponsePart{Status: 200},
		SchemaVersion: cc.SchemaVersion,
	}
}

// namedFake is a programmable Connector. Behaviour:
//   - Always satisfies Name()/Type().
//   - First failTimes calls return failWith.
//   - Subsequent calls return nil unless afterFailures is set to keepFailing.
type namedFake struct {
	name           string
	failTimes      int
	failWith       error
	afterFailures  failureMode
	failureBackoff time.Duration

	mu    sync.Mutex
	calls int
}

type failureMode int

const (
	successAfterFailures failureMode = iota
	keepFailing
)

func (f *namedFake) Name() string { return f.name }

func (f *namedFake) Type() string { return "fake" }

func (f *namedFake) Upload(ctx context.Context, seg cc.SealedSegment) error {
	f.mu.Lock()
	f.calls++
	current := f.calls
	f.mu.Unlock()

	if f.failureBackoff > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(f.failureBackoff):
		}
	}

	// failWith=nil → always succeed.
	if f.failWith == nil {
		return nil
	}
	// failTimes > 0 + successAfterFailures → fail N then succeed.
	if f.failTimes > 0 && current > f.failTimes && f.afterFailures != keepFailing {
		return nil
	}
	// Otherwise fail (failTimes=0 means "fail forever"; explicit
	// keepFailing also failures forever).
	return f.failWith
}

func (f *namedFake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// callCounter is unused but kept as a sanity hook the tests can pivot
// to if call-order matters.
var _ atomic.Uint64
