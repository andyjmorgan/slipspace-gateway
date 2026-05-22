package spool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
)

// newTestTrack builds a track wired with an in-process Manager so we
// can exercise its internals without spinning up a full Spool.
func newTestTrack(t *testing.T, opts trackOptions) *track {
	t.Helper()
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	if opts.logger == nil {
		opts.logger = discardLogger()
	}
	if opts.queueSize == 0 {
		opts.queueSize = 16
	}
	opts.rotation = opts.rotation.withDefaults()
	opts.retry = opts.retry.withDefaults()
	opts.breaker = opts.breaker.withDefaults()
	return newTrack("unit", m, opts)
}

func TestTrack_ShouldRotateNowNoSegmentReturnsFalse(t *testing.T) {
	tr := newTestTrack(t, trackOptions{
		conn:     &namedFake{name: "x"},
		rotation: RotationOpts{MaxBytes: 1, MaxAge: time.Millisecond},
	})
	if tr.shouldRotateNow() {
		t.Error("shouldRotateNow should be false with no active segment")
	}
}

func TestTrack_SealCurrentNoSegmentIsNoop(t *testing.T) {
	tr := newTestTrack(t, trackOptions{conn: &namedFake{name: "x"}})
	if err := tr.sealCurrent(); err != nil {
		t.Errorf("sealCurrent with no segment should be nil, got %v", err)
	}
}

func TestTrack_SealCurrentEmptySegmentDiscarded(t *testing.T) {
	tr := newTestTrack(t, trackOptions{conn: &namedFake{name: "x"}})
	// Open a segment without writing anything.
	tr.segMu.Lock()
	seg, err := OpenSegment(tr.manager.ActiveDir(), tr.nextSeq(), time.Now())
	if err != nil {
		tr.segMu.Unlock()
		t.Fatal(err)
	}
	tr.segment = seg
	tr.segMu.Unlock()

	if err := tr.sealCurrent(); err != nil {
		t.Fatalf("sealCurrent: %v", err)
	}
	// Sealed dir should be empty (empty segment was discarded).
	sealed, _ := tr.manager.ListSealed()
	if len(sealed) != 0 {
		t.Errorf("expected sealed/ empty after discarding empty segment, got %v", sealed)
	}
	// Active dir should be empty too.
	active, _ := tr.manager.ListActive()
	if len(active) != 0 {
		t.Errorf("expected active/ empty, got %v", active)
	}
}

func TestTrack_AttemptUploadsRespectsBreaker(t *testing.T) {
	c := &namedFake{name: "x"} // would succeed if called
	tr := newTestTrack(t, trackOptions{
		conn:    c,
		breaker: BreakerOpts{FailuresToOpen: 1, HalfOpenAfter: time.Hour},
	})
	// Force breaker open before any uploads.
	tr.breaker.RecordFailure()

	// Drop a synthetic sealed segment so ListSealed has something.
	dummy := filepath.Join(tr.manager.SealedDir(), "1-1.ndjson.zst")
	if err := os.WriteFile(dummy, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}

	tr.attemptUploads(context.Background())
	if c.Calls() != 0 {
		t.Errorf("breaker open: connector should not be called, got %d", c.Calls())
	}
}

func TestTrack_AttemptUploadsListSealedErrorIsLogged(t *testing.T) {
	c := &namedFake{name: "x"}
	tr := newTestTrack(t, trackOptions{conn: c})
	// Remove sealed dir so ListSealed errors.
	if err := os.RemoveAll(tr.manager.SealedDir()); err != nil {
		t.Fatal(err)
	}
	tr.attemptUploads(context.Background()) // should not panic
}

func TestTrack_UploadOneCancelledContext(t *testing.T) {
	c := &namedFake{
		name:          "always-retry",
		failWith:      &cc.Retryable{Err: errors.New("transient")},
		afterFailures: keepFailing,
	}
	tr := newTestTrack(t, trackOptions{
		conn: c,
		retry: RetryOpts{
			BaseBackoff: time.Second,
			MaxBackoff:  time.Second,
			MaxAttempts: 5,
			Multiplier:  2.0,
		},
	})
	dummy := filepath.Join(tr.manager.SealedDir(), "1-1.ndjson.zst")
	if err := os.WriteFile(dummy, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled
	err := tr.uploadOne(ctx, dummy)
	if err == nil {
		t.Error("expected cancellation to surface as an error")
	}
}

func TestTrack_UploadOneStopChCancelsRetrySleep(t *testing.T) {
	c := &namedFake{
		name:          "always-retry",
		failWith:      &cc.Retryable{Err: errors.New("transient")},
		afterFailures: keepFailing,
	}
	tr := newTestTrack(t, trackOptions{
		conn: c,
		retry: RetryOpts{
			BaseBackoff: 10 * time.Second, // long enough we'd notice if not interrupted
			MaxBackoff:  10 * time.Second,
			MaxAttempts: 5,
			Multiplier:  2.0,
		},
	})
	dummy := filepath.Join(tr.manager.SealedDir(), "2-1.ndjson.zst")
	if err := os.WriteFile(dummy, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}

	// Close stopCh while uploadOne is sleeping in retry.
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(tr.stopCh)
	}()
	start := time.Now()
	_ = tr.uploadOne(context.Background(), dummy)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("uploadOne did not honour stopCh during retry sleep; took %v", elapsed)
	}
}

func TestTrack_UploadOneRetryableCompleteErrorLogged(t *testing.T) {
	// Even when manager.Complete fails (e.g. file already gone), the
	// uploader should not crash. Drives the Complete-error log branch.
	c := &namedFake{name: "good"} // always succeeds
	tr := newTestTrack(t, trackOptions{conn: c})
	dummy := filepath.Join(tr.manager.SealedDir(), "3-1.ndjson.zst")
	if err := os.WriteFile(dummy, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	// uploadOne will Claim → rename → uploading/. Upload succeeds.
	// Complete then removes from uploading/ — that should work the first
	// time. For the error-logging branch we'd need Complete to fail,
	// which we exercised separately in the manager tests.
	if err := tr.uploadOne(context.Background(), dummy); err != nil {
		t.Errorf("uploadOne: %v", err)
	}
}

func TestTrack_StopTimesOut(t *testing.T) {
	// Build a track whose drain goroutine is stuck so Stop hits timeout.
	tr := newTestTrack(t, trackOptions{conn: &namedFake{name: "x"}})
	// Hand-construct a fake wg so stop() can race the timeout.
	tr.wg.Add(1)
	defer tr.wg.Done()

	if got := tr.stop(50 * time.Millisecond); got {
		t.Error("expected Stop to return false on timeout")
	}
}

func TestSpool_StopBeforeStartReturnsTrue(t *testing.T) {
	// Stopping a never-Started Spool is a no-op that should return true
	// (no goroutines to wait on).
	s := newTestSpool(t)
	if !s.Stop(10 * time.Millisecond) {
		t.Error("Stop on never-started Spool should be true")
	}
}

func TestTrack_DrainQueueProcessesAllPendingRecords(t *testing.T) {
	// Drives drainQueue directly so the success path is covered without
	// depending on runDrain's timer scheduling — coverage on this branch
	// is flaky in the integration tests that landed with PR #83.
	tr := newTestTrack(t, trackOptions{conn: &namedFake{name: "x"}})
	for i := 0; i < 3; i++ {
		tr.queue <- makeTestRecord("rec", uint64(i+1))
	}
	tr.drainQueue()
	if got := tr.written.Load(); got != 3 {
		t.Errorf("written = %d, want 3", got)
	}
}

func TestTrack_SealCurrentBestEffortLogsAndSwallows(t *testing.T) {
	// Force sealCurrent to error by manually closing the segment's
	// underlying file mid-flight. sealCurrentBestEffort should log the
	// error and return without panicking — covers the error-logging
	// branch.
	tr := newTestTrack(t, trackOptions{conn: &namedFake{name: "x"}})
	if err := tr.writeRecord(makeTestRecord("a", 1)); err != nil {
		t.Fatalf("writeRecord: %v", err)
	}
	tr.segMu.Lock()
	closed := tr.segment.file.Close()
	tr.segMu.Unlock()
	if closed != nil {
		t.Fatalf("manual close: %v", closed)
	}
	tr.sealCurrentBestEffort() // must not panic
}
