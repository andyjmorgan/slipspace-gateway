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
	err := tr.uploadOne(context.Background(), dummy)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("uploadOne did not honour stopCh during retry sleep; took %v", elapsed)
	}
	// #553: the segment was not delivered, so the stop arm must not
	// look like one. Pre-fix it returned nil and the caller recorded a
	// breaker success for a segment still sitting in uploading/.
	if !errors.Is(err, errShutdown) {
		t.Errorf("uploadOne on stopCh = %v, want errShutdown", err)
	}
	if tr.uploadsOK.Load() != 0 {
		t.Errorf("uploadsOK = %d, want 0", tr.uploadsOK.Load())
	}
	uploading, _ := tr.manager.ListUploading()
	if len(uploading) != 1 {
		t.Errorf("segment should stay claimed in uploading/ for Recover, got %v", uploading)
	}
}

// TestAttemptUploads_ShutdownDoesNotResetBreaker is the breaker-side half
// of #553: a shutdown mid-backoff leaves the accumulated failure signal
// intact instead of erasing it with a spurious RecordSuccess.
func TestAttemptUploads_ShutdownDoesNotResetBreaker(t *testing.T) {
	c := &namedFake{
		name:          "always-retry",
		failWith:      &cc.Retryable{Err: errors.New("transient")},
		afterFailures: keepFailing,
	}
	tr := newTestTrack(t, trackOptions{
		conn:    c,
		breaker: BreakerOpts{FailuresToOpen: 100, HalfOpenAfter: time.Hour},
		retry: RetryOpts{
			BaseBackoff: 10 * time.Second,
			MaxBackoff:  10 * time.Second,
			MaxAttempts: 5,
			Multiplier:  2.0,
		},
	})
	// Three prior destination failures on the books.
	for i := 0; i < 3; i++ {
		tr.breaker.RecordFailure()
	}
	seedSegment(t, tr.manager.SealedDir(), 1, makeTestRecord("s", 1))

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(tr.stopCh)
	}()
	tr.attemptUploads(context.Background())

	// 3 prior + exactly 1 attempt before the backoff was interrupted.
	// A reset to 0 is the bug.
	tr.breaker.mu.Lock()
	fails := tr.breaker.consecutiveFails
	tr.breaker.mu.Unlock()
	if fails != 4 {
		t.Errorf("consecutiveFails after shutdown = %d, want 4 (3 prior + 1 attempt)", fails)
	}
}

// TestAttemptUploads_DeadletterContinuesScan pins #412: one segment
// quarantined into deadletter/ must not stop the scan — the sealed
// segments behind it ship in the same pass — and the transition itself is
// not a breaker failure on top of the attempts that caused it.
func TestAttemptUploads_DeadletterContinuesScan(t *testing.T) {
	// First Upload call → Permanent (→ DLQ); every later call succeeds.
	c := &namedFake{
		name:      "perm-once",
		failTimes: 1,
		failWith:  &cc.Permanent{Err: errors.New("403 forbidden")},
	}
	tr := newTestTrack(t, trackOptions{
		conn:    c,
		breaker: BreakerOpts{FailuresToOpen: 5, HalfOpenAfter: time.Hour},
	})
	seedSegment(t, tr.manager.SealedDir(), 1, makeTestRecord("a", 1))
	seedSegment(t, tr.manager.SealedDir(), 2, makeTestRecord("b", 2))
	seedSegment(t, tr.manager.SealedDir(), 3, makeTestRecord("c", 3))

	tr.attemptUploads(context.Background())

	if got := c.Calls(); got != 3 {
		t.Errorf("Upload calls = %d, want 3 (scan must continue past the DLQ)", got)
	}
	dl, _ := os.ReadDir(tr.manager.DeadletterDir())
	dlSegments := 0
	for _, e := range dl {
		if filepath.Ext(e.Name()) == ".zst" {
			dlSegments++
		}
	}
	if dlSegments != 1 {
		t.Errorf("deadletter segments = %d, want 1", dlSegments)
	}
	if sealed, _ := tr.manager.ListSealed(); len(sealed) != 0 {
		t.Errorf("sealed/ should be drained in one pass, still has %v", sealed)
	}
	if tr.uploadsOK.Load() != 2 || tr.uploadsDLQ.Load() != 1 {
		t.Errorf("uploadsOK=%d uploadsDLQ=%d, want 2/1", tr.uploadsOK.Load(), tr.uploadsDLQ.Load())
	}
	// One failed attempt then two successes: the successes closed the
	// breaker and reset the counter — nothing was double-counted.
	if tr.breaker.State() != breakerClosed {
		t.Errorf("breaker = %v, want closed", tr.breaker.State())
	}
}

// TestUploadOne_DeadletterReturnsTypedSentinel checks the sentinel wraps
// the cause so callers can still classify the upload error.
func TestUploadOne_DeadletterReturnsTypedSentinel(t *testing.T) {
	c := &namedFake{name: "perm", failWith: &cc.Permanent{Err: errors.New("bad auth")}}
	tr := newTestTrack(t, trackOptions{conn: c})
	seedSegment(t, tr.manager.SealedDir(), 1, makeTestRecord("a", 1))
	sealed, _ := tr.manager.ListSealed()

	err := tr.uploadOne(context.Background(), sealed[0])
	if !errors.Is(err, errDeadlettered) {
		t.Fatalf("uploadOne after DLQ = %v, want errDeadlettered", err)
	}
	if !cc.IsPermanent(err) {
		t.Errorf("errDeadlettered should wrap the Permanent cause: %v", err)
	}
}

// TestUploadOne_BreakerCountsPerAttempt: destination failures are recorded
// where they happen — per Upload call — so a retry schedule that exhausts
// into deadletter still opens the breaker, and the segment's own budget
// still runs to completion after it does.
func TestUploadOne_BreakerCountsPerAttempt(t *testing.T) {
	c := &namedFake{
		name:          "always-retry",
		failWith:      &cc.Retryable{Err: errors.New("503")},
		afterFailures: keepFailing,
	}
	tr := newTestTrack(t, trackOptions{
		conn:    c,
		breaker: BreakerOpts{FailuresToOpen: 2, HalfOpenAfter: time.Hour},
		retry: RetryOpts{
			BaseBackoff: time.Microsecond,
			MaxBackoff:  time.Microsecond,
			MaxAttempts: 4,
			Multiplier:  2.0,
		},
	})
	seedSegment(t, tr.manager.SealedDir(), 1, makeTestRecord("a", 1))
	sealed, _ := tr.manager.ListSealed()

	err := tr.uploadOne(context.Background(), sealed[0])
	if !errors.Is(err, errDeadlettered) {
		t.Fatalf("expected DLQ after exhausting retries, got %v", err)
	}
	if c.Calls() != 4 {
		t.Errorf("Upload calls = %d, want the full MaxAttempts=4 even after the breaker opened", c.Calls())
	}
	if tr.breaker.State() != breakerOpen {
		t.Errorf("breaker = %v, want open after 4 consecutive failed attempts (threshold 2)", tr.breaker.State())
	}
}

// TestAttemptUploads_HalfOpenEmptyScanReleasesProbe guards the reservation
// Allow hands out: a wake with nothing in sealed/ must give it back, or the
// breaker would refuse every later probe forever.
func TestAttemptUploads_HalfOpenEmptyScanReleasesProbe(t *testing.T) {
	tick := time.Now()
	tr := newTestTrack(t, trackOptions{
		conn:    &namedFake{name: "x"},
		breaker: BreakerOpts{FailuresToOpen: 1, HalfOpenAfter: time.Millisecond},
		now:     func() time.Time { return tick },
	})
	tr.breaker.RecordFailure() // open
	tick = tick.Add(time.Hour) // cooldown elapsed

	tr.attemptUploads(context.Background()) // nothing sealed
	if tr.breaker.State() != breakerHalfOpen {
		t.Fatalf("breaker = %v, want half-open after cooldown", tr.breaker.State())
	}
	if !tr.breaker.Allow() {
		t.Error("probe reservation leaked: Allow refused after an empty scan")
	}
}

// TestUploadOne_PopulatesSealedSegmentFromSidecar pins #440: the
// SealedSegment handed to Connector.Upload carries the stats the segment
// accumulated before it sealed, read back from the on-disk sidecar.
func TestUploadOne_PopulatesSealedSegmentFromSidecar(t *testing.T) {
	c := &namedFake{name: "capture"}
	tr := newTestTrack(t, trackOptions{conn: c})

	r1 := makeTestRecord("a", 1)
	r1.TsNs = 1_700_000_000_000_000_000
	r2 := makeTestRecord("b", 2)
	r2.TsNs = 1_700_000_005_000_000_000
	if err := tr.writeRecord(r1); err != nil {
		t.Fatal(err)
	}
	if err := tr.writeRecord(r2); err != nil {
		t.Fatal(err)
	}
	if err := tr.sealCurrent(); err != nil {
		t.Fatalf("sealCurrent: %v", err)
	}
	sealed, _ := tr.manager.ListSealed()
	if len(sealed) != 1 {
		t.Fatalf("sealed = %v, want one segment", sealed)
	}
	if _, err := os.Stat(metaPath(sealed[0])); err != nil {
		t.Fatalf("sidecar should sit next to the sealed segment: %v", err)
	}
	// Bytes is the compressed size on disk — capture it before Complete
	// removes the file.
	info, err := os.Stat(sealed[0])
	if err != nil {
		t.Fatal(err)
	}

	if err := tr.uploadOne(context.Background(), sealed[0]); err != nil {
		t.Fatalf("uploadOne: %v", err)
	}
	seg := c.LastSeg()
	if seg.Records != 2 {
		t.Errorf("Records = %d, want 2", seg.Records)
	}
	if seg.TsMinNs != r1.TsNs || seg.TsMaxNs != r2.TsNs {
		t.Errorf("Ts range = [%d, %d], want [%d, %d]", seg.TsMinNs, seg.TsMaxNs, r1.TsNs, r2.TsNs)
	}
	if seg.BytesUncompressed <= 0 {
		t.Errorf("BytesUncompressed = %d, want > 0", seg.BytesUncompressed)
	}
	if seg.Bytes != info.Size() {
		t.Errorf("Bytes = %d, want compressed file size %d", seg.Bytes, info.Size())
	}
	if seg.DeliveryID != deliveryIDFromFilename(filepath.Base(sealed[0])) {
		t.Errorf("DeliveryID = %q", seg.DeliveryID)
	}
	// Complete cleaned up both files.
	if entries, _ := os.ReadDir(tr.manager.UploadingDir()); len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("uploading/ should be empty after Complete, got %v", names)
	}
}

// TestUploadOne_MissingSidecarFallsBackToZero covers segments sealed by a
// pre-sidecar binary (or Recover-sealed active/ leftovers): they still
// ship, with the time-range fields zero so connectors use their clock.
func TestUploadOne_MissingSidecarFallsBackToZero(t *testing.T) {
	c := &namedFake{name: "capture"}
	tr := newTestTrack(t, trackOptions{conn: c})
	seedSegment(t, tr.manager.SealedDir(), 1, makeTestRecord("a", 1)) // no sidecar
	sealed, _ := tr.manager.ListSealed()

	if err := tr.uploadOne(context.Background(), sealed[0]); err != nil {
		t.Fatalf("uploadOne: %v", err)
	}
	seg := c.LastSeg()
	if seg.TsMinNs != 0 || seg.TsMaxNs != 0 || seg.Records != 0 || seg.BytesUncompressed != 0 {
		t.Errorf("no sidecar should leave stats zero, got %+v", seg)
	}
	if seg.Bytes <= 0 {
		t.Errorf("Bytes should still come from os.Stat, got %d", seg.Bytes)
	}
}

// TestUploadOne_CorruptSidecarIsLoggedNotFatal: a sidecar that fails to
// decode must not stop the segment shipping.
func TestUploadOne_CorruptSidecarIsLoggedNotFatal(t *testing.T) {
	c := &namedFake{name: "capture"}
	tr := newTestTrack(t, trackOptions{conn: c})
	seedSegment(t, tr.manager.SealedDir(), 1, makeTestRecord("a", 1))
	sealed, _ := tr.manager.ListSealed()
	if err := os.WriteFile(metaPath(sealed[0]), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := tr.uploadOne(context.Background(), sealed[0]); err != nil {
		t.Fatalf("uploadOne: %v", err)
	}
	if c.Calls() != 1 {
		t.Errorf("Upload calls = %d, want 1", c.Calls())
	}
	if seg := c.LastSeg(); seg.Records != 0 {
		t.Errorf("corrupt sidecar should be ignored, got %+v", seg)
	}
	if _, err := os.Stat(metaPath(filepath.Join(tr.manager.UploadingDir(), filepath.Base(sealed[0])))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("corrupt sidecar should be removed by Complete, stat err = %v", err)
	}
}

// TestAttemptUploads_StopsClaimingOnceBreakerOpens: per-attempt accounting
// opens the breaker mid-scan, and the Allow check between segments then
// leaves the rest of the backlog in sealed/ for the half-open probe.
func TestAttemptUploads_StopsClaimingOnceBreakerOpens(t *testing.T) {
	c := &namedFake{
		name:          "down",
		failWith:      &cc.Retryable{Err: errors.New("503")},
		afterFailures: keepFailing,
	}
	tr := newTestTrack(t, trackOptions{
		conn:    c,
		breaker: BreakerOpts{FailuresToOpen: 1, HalfOpenAfter: time.Hour},
		retry:   RetryOpts{BaseBackoff: time.Microsecond, MaxBackoff: time.Microsecond, MaxAttempts: 1, Multiplier: 2},
	})
	seedSegment(t, tr.manager.SealedDir(), 1, makeTestRecord("a", 1))
	seedSegment(t, tr.manager.SealedDir(), 2, makeTestRecord("b", 2))

	tr.attemptUploads(context.Background())

	if c.Calls() != 1 {
		t.Errorf("Upload calls = %d, want 1 — the second segment must wait for the probe", c.Calls())
	}
	if tr.breaker.State() != breakerOpen {
		t.Errorf("breaker = %v, want open", tr.breaker.State())
	}
	if sealed, _ := tr.manager.ListSealed(); len(sealed) != 1 {
		t.Errorf("one segment should remain sealed, got %v", sealed)
	}
	if tr.uploadsDLQ.Load() != 1 {
		t.Errorf("uploadsDLQ = %d, want 1", tr.uploadsDLQ.Load())
	}
}

// TestAttemptUploads_StoppedBeforeLoopMakesNoAttempt covers the early
// ctx / stopCh exits: nothing is claimed and any probe slot is released.
func TestAttemptUploads_StoppedBeforeLoopMakesNoAttempt(t *testing.T) {
	for _, mode := range []string{"ctx", "stopCh"} {
		t.Run(mode, func(t *testing.T) {
			c := &namedFake{name: "x"}
			tick := time.Now()
			tr := newTestTrack(t, trackOptions{
				conn:    c,
				breaker: BreakerOpts{FailuresToOpen: 1, HalfOpenAfter: time.Millisecond},
				now:     func() time.Time { return tick },
			})
			tr.breaker.RecordFailure()
			tick = tick.Add(time.Hour) // next Allow → half-open probe
			seedSegment(t, tr.manager.SealedDir(), 1, makeTestRecord("a", 1))

			ctx := context.Background()
			if mode == "ctx" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			} else {
				close(tr.stopCh)
			}
			tr.attemptUploads(ctx)

			if c.Calls() != 0 {
				t.Errorf("Upload calls = %d, want 0", c.Calls())
			}
			if sealed, _ := tr.manager.ListSealed(); len(sealed) != 1 {
				t.Errorf("segment should stay sealed, got %v", sealed)
			}
			if !tr.breaker.Allow() {
				t.Error("probe reservation leaked on early exit")
			}
		})
	}
}

// TestAttemptUploads_ContextCancelledMidAttemptIsNotAFailure: an attempt
// that dies with the worker's context is shutdown, not destination
// evidence — the breaker is left alone and the scan stops.
func TestAttemptUploads_ContextCancelledMidAttemptIsNotAFailure(t *testing.T) {
	c := &namedFake{
		name:           "slow",
		failWith:       &cc.Retryable{Err: errors.New("transient")},
		afterFailures:  keepFailing,
		failureBackoff: time.Second, // Upload blocks until ctx fires
	}
	tr := newTestTrack(t, trackOptions{
		conn:    c,
		breaker: BreakerOpts{FailuresToOpen: 1, HalfOpenAfter: time.Hour},
	})
	seedSegment(t, tr.manager.SealedDir(), 1, makeTestRecord("a", 1))
	seedSegment(t, tr.manager.SealedDir(), 2, makeTestRecord("b", 2))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	tr.attemptUploads(ctx)

	if c.Calls() != 1 {
		t.Errorf("Upload calls = %d, want 1 (scan stops on ctx)", c.Calls())
	}
	if tr.breaker.State() != breakerClosed {
		t.Errorf("breaker = %v, want closed — a cancelled attempt is not a destination failure", tr.breaker.State())
	}
	if uploading, _ := tr.manager.ListUploading(); len(uploading) != 1 {
		t.Errorf("the interrupted segment should stay claimed for Recover, got %v", uploading)
	}
}

// TestUploadOne_DeadletterTransitionFailureIsLogged: when the DLQ rename
// itself fails the outcome is still reported as deadlettered (the counter
// bumps, the error is logged) rather than turning into a breaker failure.
func TestUploadOne_DeadletterTransitionFailureIsLogged(t *testing.T) {
	c := &namedFake{name: "perm", failWith: &cc.Permanent{Err: errors.New("bad auth")}}
	tr := newTestTrack(t, trackOptions{conn: c})
	seedSegment(t, tr.manager.SealedDir(), 1, makeTestRecord("a", 1))
	sealed, _ := tr.manager.ListSealed()
	if err := os.RemoveAll(tr.manager.DeadletterDir()); err != nil {
		t.Fatal(err)
	}

	err := tr.uploadOne(context.Background(), sealed[0])
	if !errors.Is(err, errDeadlettered) {
		t.Fatalf("uploadOne = %v, want errDeadlettered even when the rename fails", err)
	}
	if tr.uploadsDLQ.Load() != 1 {
		t.Errorf("uploadsDLQ = %d, want 1", tr.uploadsDLQ.Load())
	}
	if uploading, _ := tr.manager.ListUploading(); len(uploading) != 1 {
		t.Errorf("segment should remain in uploading/ when the DLQ rename fails, got %v", uploading)
	}
}

// TestTrack_SealCurrentSidecarWriteFailureIsNonFatal: the sidecar is
// best-effort — losing it degrades to wall-clock partitioning, it must
// never cost the segment.
func TestTrack_SealCurrentSidecarWriteFailureIsNonFatal(t *testing.T) {
	tr := newTestTrack(t, trackOptions{conn: &namedFake{name: "x"}})
	if err := tr.writeRecord(makeTestRecord("a", 1)); err != nil {
		t.Fatal(err)
	}
	// Occupy the sidecar's path with a directory so WriteFile fails.
	tr.segMu.Lock()
	segPath := tr.segment.Path()
	tr.segMu.Unlock()
	if err := os.Mkdir(metaPath(segPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := tr.sealCurrent(); err != nil {
		t.Fatalf("sealCurrent should succeed without the sidecar: %v", err)
	}
	if sealed, _ := tr.manager.ListSealed(); len(sealed) != 1 {
		t.Errorf("segment should still seal, got %v", sealed)
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

// TestUploadOne_LostClaimRaceIsSkipNotSuccess covers the benign half of the
// Claim split: a segment a sibling worker already claimed is reported as
// errClaimRaceLost so attemptUploads skips it, rather than as nil, which
// the caller would record on the breaker as a delivery that never happened.
func TestUploadOne_LostClaimRaceIsSkipNotSuccess(t *testing.T) {
	tr := newTestTrack(t, trackOptions{conn: &namedFake{name: "x"}})

	// A sealed path that does not exist — exactly what a lost race leaves
	// behind, since the winner already renamed it into uploading/.
	missing := filepath.Join(tr.manager.root, stateSealed, "0000000000000000-deadbeef.ndjson.zst")

	err := tr.uploadOne(context.Background(), missing)
	if !errors.Is(err, errClaimRaceLost) {
		t.Fatalf("uploadOne on a vanished segment = %v, want errClaimRaceLost", err)
	}
	if tr.uploadsOK.Load() != 0 {
		t.Errorf("uploadsOK = %d, want 0 — a lost race delivered nothing", tr.uploadsOK.Load())
	}
}

// TestUploadOne_ClaimFailureIsNotSuccess covers the half that was broken: a
// Claim error that is NOT a lost race (here, a path outside sealed/, which
// assertUnder rejects) must surface as an error so the breaker sees it.
// Previously every Claim error returned nil and reset the failure counter.
func TestUploadOne_ClaimFailureIsNotSuccess(t *testing.T) {
	tr := newTestTrack(t, trackOptions{conn: &namedFake{name: "x"}})

	// Under the spool root but in the wrong state dir: assertUnder fails
	// with an error that is not os.ErrNotExist.
	wrongDir := filepath.Join(tr.manager.root, stateUploading, "0000000000000000-deadbeef.ndjson.zst")

	err := tr.uploadOne(context.Background(), wrongDir)
	if err == nil {
		t.Fatal("uploadOne returned nil for a real Claim failure — the caller would record a delivery")
	}
	if errors.Is(err, errClaimRaceLost) {
		t.Fatalf("a non-ENOENT Claim error was misreported as a lost race: %v", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("test setup wrong: expected a non-ENOENT failure, got %v", err)
	}
	if tr.uploadsOK.Load() != 0 {
		t.Errorf("uploadsOK = %d, want 0", tr.uploadsOK.Load())
	}
}

// TestAttemptUploads_ClaimFailureOpensBreaker is the end of the causal chain
// the bug broke: on a persistently unclaimable spool the breaker must open
// instead of resetting on every pass.
func TestAttemptUploads_ClaimFailureOpensBreaker(t *testing.T) {
	tr := newTestTrack(t, trackOptions{
		conn:    &namedFake{name: "x"},
		breaker: BreakerOpts{FailuresToOpen: 2, HalfOpenAfter: time.Hour},
	})

	// Two sealed entries that Claim will reject for a non-ENOENT reason.
	// ListSealed reads the directory, so create real files, then make the
	// sealed dir unwritable so the rename out of it fails.
	for _, n := range []string{"0000000000000001-aaaaaaaa", "0000000000000002-bbbbbbbb"} {
		p := filepath.Join(tr.manager.root, stateSealed, n+".ndjson.zst")
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("seed sealed segment: %v", err)
		}
	}
	sealedDir := filepath.Join(tr.manager.root, stateSealed)
	//nolint:gosec // G302 targets file modes; this is a directory, which needs
	// the execute bit to stay traversable — 0o500 is r-x precisely so ListSealed
	// can still read it while the rename out of it fails with EACCES.
	if err := os.Chmod(sealedDir, 0o500); err != nil {
		t.Fatalf("chmod sealed dir: %v", err)
	}
	//nolint:gosec // G302 targets file modes; restoring a directory to rwx so
	// t.TempDir cleanup can remove it.
	t.Cleanup(func() { _ = os.Chmod(sealedDir, 0o700) })

	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not deny rename")
	}

	// attemptUploads returns after the first failure, so one pass records
	// one failure. Two passes is what the drain loop actually does — and is
	// precisely what the bug defeated: each pass used to end in
	// RecordSuccess, resetting the consecutive-failure counter so the
	// breaker could never reach its threshold no matter how many passes ran.
	tr.attemptUploads(context.Background())
	tr.attemptUploads(context.Background())

	if got := tr.breaker.State(); got == breakerClosed {
		t.Error("breaker still closed after repeated Claim failures — " +
			"a failed claim is being recorded as a successful upload")
	}
}
