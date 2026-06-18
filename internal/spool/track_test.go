package spool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
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

func TestTrack_RunDrainExitsOnContextCancel(t *testing.T) {
	// Pin runDrain's exit to the ctx.Done branch by cancelling the context
	// and never touching stopCh, so the select has exactly one ready case.
	// The integration Stop path leaves BOTH ctx.Done and stopCh ready at the
	// same instant and Go's select picks one at random, so coverage of this
	// branch (track.go runDrain ctx.Done → sealCurrentBestEffort) was flaky
	// and dipped the package under 95% under full-suite load (#113). A large
	// MaxAge keeps the rotation timer from racing the cancel.
	tr := newTestTrack(t, trackOptions{
		conn:     &namedFake{name: "x"},
		rotation: RotationOpts{MaxAge: time.Hour, MaxBytes: 1 << 30},
	})
	if err := tr.writeRecord(makeTestRecord("a", 1)); err != nil {
		t.Fatalf("writeRecord: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tr.runDrain(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runDrain did not exit on ctx cancel")
	}

	// The ctx.Done branch seals best-effort, so the written record lands in
	// sealed/.
	sealed, _ := tr.manager.ListSealed()
	if len(sealed) != 1 {
		t.Errorf("expected 1 sealed segment after ctx-cancel drain, got %d", len(sealed))
	}
}

func TestTrack_RunDrainExitsOnStopCh(t *testing.T) {
	// Pin runDrain's exit to the stopCh branch (drainQueue + seal + return):
	// close stopCh with a live context, empty queue, and the rotation timer
	// parked, so the select has exactly one ready case. This is the branch
	// (track.go:147-150) whose coverage flipped under full-suite load (#113) —
	// at integration Stop both ctx.Done and stopCh are ready and the select
	// picks at random.
	tr := newTestTrack(t, trackOptions{
		conn:     &namedFake{name: "x"},
		rotation: RotationOpts{MaxAge: time.Hour, MaxBytes: 1 << 30},
	})
	// writeRecord opens the segment directly (queue stays empty), so the
	// stopCh branch's drainQueue is a no-op and only the seal has work — keeps
	// the select deterministic (no queue case racing stopCh).
	if err := tr.writeRecord(makeTestRecord("a", 1)); err != nil {
		t.Fatalf("writeRecord: %v", err)
	}
	done := make(chan struct{})
	go func() {
		tr.runDrain(context.Background())
		close(done)
	}()
	close(tr.stopCh)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runDrain did not exit on stopCh")
	}
	sealed, _ := tr.manager.ListSealed()
	if len(sealed) != 1 {
		t.Errorf("expected 1 sealed segment after stopCh drain, got %d", len(sealed))
	}
}

func TestTrack_SealCurrentDropsKickWhenChannelFull(t *testing.T) {
	// Drive sealCurrent's default (kick-dropped) branch by pre-filling the
	// buffered uploadKick so the non-blocking send finds no room. A dropped
	// kick is safe — the uploader's periodic poll still finds the sealed
	// segment. This default branch (track.go:243) was the flaky one under
	// load (#113); the send branch is covered by the sibling test above.
	tr := newTestTrack(t, trackOptions{conn: &namedFake{name: "x"}})
	tr.uploadKick <- struct{}{} // fill the buffer-1 channel
	if err := tr.writeRecord(makeTestRecord("a", 1)); err != nil {
		t.Fatalf("writeRecord: %v", err)
	}
	if err := tr.sealCurrent(); err != nil {
		t.Fatalf("sealCurrent: %v", err)
	}
	// The seal's send was dropped, so the channel still holds exactly the one
	// we pre-loaded (not two, and not drained).
	if got := len(tr.uploadKick); got != 1 {
		t.Errorf("expected uploadKick to still hold 1 after a dropped kick, got %d", got)
	}
}

func TestTrack_RunUploaderExitsOnStopCh(t *testing.T) {
	// Pin runUploader's exit to the stopCh branch: close stopCh while the
	// context stays live and the poll ticker is parked far in the future, so
	// the select has exactly one ready case. Same root cause as #113 — at
	// integration Stop both ctx.Done and stopCh are ready and the select
	// picks at random, leaving this branch's coverage load-dependent.
	tr := newTestTrack(t, trackOptions{
		conn:       &namedFake{name: "x"},
		uploadPoll: time.Hour,
	})
	done := make(chan struct{})
	go func() {
		tr.runUploader(context.Background())
		close(done)
	}()
	close(tr.stopCh)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runUploader did not exit on stopCh")
	}
}

func TestTrack_SealCurrentKicksUploaderOnNonEmptySeal(t *testing.T) {
	// Sealing a non-empty segment sends on the buffered uploadKick so the
	// uploader wakes without waiting for its poll. With a fresh (empty) kick
	// channel the send always succeeds; the integration path covered this
	// branch only when the uploader hadn't already drained the kick, so it
	// was flaky under load (#113, track.go sealCurrent uploadKick send).
	tr := newTestTrack(t, trackOptions{conn: &namedFake{name: "x"}})
	if err := tr.writeRecord(makeTestRecord("a", 1)); err != nil {
		t.Fatalf("writeRecord: %v", err)
	}
	if err := tr.sealCurrent(); err != nil {
		t.Fatalf("sealCurrent: %v", err)
	}
	select {
	case <-tr.uploadKick:
		// kick delivered — the send branch ran
	default:
		t.Error("sealCurrent did not kick the uploader after sealing a non-empty segment")
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
