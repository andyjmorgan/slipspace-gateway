package spool

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/safego"
)

// trackStats is a per-track counter snapshot exposed via Spool.Stats.
type trackStats struct {
	Enqueued        uint64
	DroppedRing     uint64
	Written         uint64
	WriteErrors     uint64
	SegmentsSealed  uint64
	UploadsOK       uint64
	UploadsRetried  uint64
	UploadsDLQ      uint64
	BreakerState    breakerState
	PendingSegments int
}

// trackOptions configures one connector binding's runtime state.
type trackOptions struct {
	conn          connector.Connector
	queueSize     int
	rotation      RotationOpts
	retry         RetryOpts
	breaker       BreakerOpts
	uploadPoll    time.Duration
	now           func() time.Time
	logger        *slog.Logger
	uploadCtxFunc func(parent context.Context) (context.Context, context.CancelFunc)
}

// track owns one connector destination's spool state: a bounded queue,
// the active segment, the rotation/retry/breaker policy, and the
// drain + upload goroutines.
type track struct {
	name string
	opts trackOptions

	manager *Manager
	queue   chan cc.Record

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup

	uploadKick chan struct{}

	seqMu sync.Mutex
	seq   uint64

	segMu   sync.Mutex
	segment *Segment

	breaker *breaker

	// counters — all atomic so Stats is lock-free.
	enqueued       atomic.Uint64
	droppedRing    atomic.Uint64
	written        atomic.Uint64
	writeErrors    atomic.Uint64
	segmentsSealed atomic.Uint64
	uploadsOK      atomic.Uint64
	uploadsRetried atomic.Uint64
	uploadsDLQ     atomic.Uint64
}

func newTrack(name string, manager *Manager, opts trackOptions) *track {
	return &track{
		name:       name,
		opts:       opts,
		manager:    manager,
		queue:      make(chan cc.Record, opts.queueSize),
		stopCh:     make(chan struct{}),
		uploadKick: make(chan struct{}, 1),
		breaker:    newBreaker(opts.breaker, opts.now),
	}
}

// enqueue is non-blocking. If the queue is full the record is dropped
// and the drop counter increments.
func (t *track) enqueue(rec cc.Record) {
	select {
	case t.queue <- rec:
		t.enqueued.Add(1)
	default:
		t.droppedRing.Add(1)
	}
}

// start spawns the drain + uploader goroutines via safego so panics
// are recovered with a logged error rather than crashing the process.
func (t *track) start(ctx context.Context) {
	t.wg.Add(2)
	safego.Go(ctx, "spool.track.drain", t.opts.logger, nil, func() {
		defer t.wg.Done()
		t.runDrain(ctx)
	})
	safego.Go(ctx, "spool.track.uploader", t.opts.logger, nil, func() {
		defer t.wg.Done()
		t.runUploader(ctx)
	})
}

// stop signals the goroutines to drain and exit, then waits until they
// finish or timeout elapses.
func (t *track) stop(timeout time.Duration) bool {
	t.stopOnce.Do(func() { close(t.stopCh) })

	done := make(chan struct{})
	safego.Go(context.Background(), "spool.track.stop_join", t.opts.logger, nil, func() {
		t.wg.Wait()
		close(done)
	})
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// runDrain reads records from the queue and writes them to the active
// segment, rotating on size/age.
func (t *track) runDrain(ctx context.Context) {
	timer := time.NewTimer(t.opts.rotation.MaxAge)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			t.sealCurrentBestEffort()
			return
		case <-t.stopCh:
			t.drainQueue()
			t.sealCurrentBestEffort()
			return
		case rec := <-t.queue:
			t.handleRecord(rec)
			if t.shouldRotateNow() {
				t.sealCurrentBestEffort()
				resetTimer(timer, t.opts.rotation.MaxAge)
			}
		case <-timer.C:
			t.sealCurrentBestEffort()
			resetTimer(timer, t.opts.rotation.MaxAge)
		}
	}
}

func (t *track) drainQueue() {
	for {
		select {
		case rec := <-t.queue:
			t.handleRecord(rec)
		default:
			return
		}
	}
}

func (t *track) handleRecord(rec cc.Record) {
	if err := t.writeRecord(rec); err != nil {
		t.writeErrors.Add(1)
		t.opts.logger.Error("spool: write record failed",
			slog.String("track", t.name),
			slog.String("err", err.Error()),
		)
		return
	}
	t.written.Add(1)
}

func (t *track) writeRecord(rec cc.Record) error {
	t.segMu.Lock()
	defer t.segMu.Unlock()
	if t.segment == nil {
		seg, err := OpenSegment(t.manager.ActiveDir(), t.nextSeq(), t.opts.now())
		if err != nil {
			return fmt.Errorf("open segment: %w", err)
		}
		t.segment = seg
	}
	return t.segment.Write(rec)
}

func (t *track) shouldRotateNow() bool {
	t.segMu.Lock()
	defer t.segMu.Unlock()
	if t.segment == nil {
		return false
	}
	return t.segment.ShouldRotate(t.opts.rotation.MaxBytes, t.opts.rotation.MaxAge, t.opts.now())
}

func (t *track) sealCurrentBestEffort() {
	if err := t.sealCurrent(); err != nil {
		t.opts.logger.Error("spool: seal failed",
			slog.String("track", t.name),
			slog.String("err", err.Error()),
		)
	}
}

func (t *track) sealCurrent() error {
	t.segMu.Lock()
	seg := t.segment
	t.segment = nil
	t.segMu.Unlock()
	if seg == nil {
		return nil
	}
	stats := seg.Stats()
	if err := seg.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if stats.Records == 0 {
		// Empty segment — discard the zero-byte file instead of moving
		// to sealed/.
		_ = os.Remove(seg.Path())
		return nil
	}
	// Persist the counters next to the file before the rename so Seal
	// carries both into sealed/ together. The in-memory Segment is gone
	// by the time uploadOne runs — possibly in a later process — and the
	// sidecar is how SealedSegment.TsMinNs/Records/... survive that gap.
	// Best-effort: a segment without a sidecar still ships, partitioned
	// by upload clock, which is the pre-sidecar behaviour.
	if err := writeSegmentMeta(seg.Path(), stats); err != nil {
		t.opts.logger.Warn("spool: segment meta not written; upload will fall back to wall clock",
			slog.String("track", t.name),
			slog.String("path", seg.Path()),
			slog.String("err", err.Error()))
	}
	if _, err := t.manager.Seal(seg.Path()); err != nil {
		return fmt.Errorf("seal rename: %w", err)
	}
	t.segmentsSealed.Add(1)
	// Kick the uploader so it doesn't have to wait for the poll timer.
	select {
	case t.uploadKick <- struct{}{}:
	default:
	}
	return nil
}

func (t *track) nextSeq() uint64 {
	t.seqMu.Lock()
	defer t.seqMu.Unlock()
	t.seq++
	return t.seq
}

// runUploader is the upload worker. Pulled out so it can be tested via
// direct invocation. Wakes on uploadKick and on a periodic poll so a
// missed kick (full chan, race) doesn't strand sealed segments forever.
func (t *track) runUploader(ctx context.Context) {
	poll := t.opts.uploadPoll
	if poll <= 0 {
		poll = 5 * time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stopCh:
			return
		case <-t.uploadKick:
			t.attemptUploads(ctx)
		case <-ticker.C:
			t.attemptUploads(ctx)
		}
	}
}

// attemptUploads iterates sealed segments and ships each via the
// connector. Honours the per-destination circuit breaker: Allow gates
// every segment, so once uploadOne's per-attempt accounting has opened
// the breaker the scan stops and the remaining backlog waits for the
// half-open probe.
//
// Outcomes from uploadOne:
//
//   - nil — delivered; the breaker already saw the success.
//   - errClaimRaceLost — a sibling owns the segment; skip. Nothing was
//     attempted, so any half-open reservation Allow handed out is
//     released rather than left dangling.
//   - errDeadlettered — the segment is quarantined; the failed attempts
//     that got it there were recorded one by one, so the transition
//     itself is not a further failure and the scan continues to the next
//     sealed segment (#412).
//   - errShutdown / a context error — the worker is stopping; the
//     segment stays claimed for the next boot's Recover. Neither a
//     delivery nor a failure (#553).
//   - anything else — a local failure (Claim) that never reached the
//     destination; counted against the breaker and the scan stops.
func (t *track) attemptUploads(ctx context.Context) {
	if !t.breaker.Allow() {
		return
	}
	sealed, err := t.manager.ListSealed()
	if err != nil {
		t.breaker.Release()
		t.opts.logger.Error("spool: list sealed", slog.String("track", t.name), slog.String("err", err.Error()))
		return
	}
	if len(sealed) == 0 {
		// Nothing to probe with; hand back any half-open reservation so
		// the next wake can take it.
		t.breaker.Release()
		return
	}
	for i, path := range sealed {
		// The first Allow (above) covers the first segment; re-checking
		// it here would be refused in half-open by our own reservation.
		if i > 0 && !t.breaker.Allow() {
			return
		}
		select {
		case <-ctx.Done():
			t.breaker.Release()
			return
		case <-t.stopCh:
			t.breaker.Release()
			return
		default:
		}
		err := t.uploadOne(ctx, path)
		switch {
		case err == nil, errors.Is(err, errDeadlettered):
			continue
		case errors.Is(err, errClaimRaceLost):
			t.breaker.Release()
			continue
		case errors.Is(err, errShutdown), ctx.Err() != nil:
			return
		default:
			t.breaker.RecordFailure()
			return
		}
	}
}

// errClaimRaceLost reports that a sealed segment was claimed by a sibling
// upload worker between ListSealed and Claim. It is a benign skip, not a
// delivery and not a failure, so attemptUploads moves to the next segment
// without touching the circuit breaker.
var errClaimRaceLost = errors.New("spool: sealed segment claimed by another worker")

// errShutdown reports that uploadOne was interrupted by track.stop while
// waiting out a retry backoff. The segment was not delivered — it stays
// in uploading/ for the next boot's Recover to return to sealed/ — so it
// must never be reported as nil: pre-#553 the stop arm returned nil and
// the caller recorded a spurious breaker success for an undelivered
// segment.
var errShutdown = errors.New("spool: upload interrupted by shutdown")

// errDeadlettered reports that uploadOne moved the segment to
// deadletter/ — after a *cc.Permanent error or an exhausted retry
// budget — and wraps the upload error that caused it. It is a handled
// terminal outcome: the segment is quarantined and the destination
// failures were already recorded against the breaker per attempt, so
// attemptUploads neither records it again nor stops the sealed scan.
var errDeadlettered = errors.New("spool: segment deadlettered")

// uploadOne claims one sealed segment, calls Connector.Upload with the
// retry schedule, and either Completes, deadletters, or leaves the
// segment in uploading/ (shutdown) depending on the outcome.
//
// Breaker accounting is per Upload attempt, because that is the unit
// that actually reaches the destination: each failed attempt is a
// RecordFailure, a delivery is a RecordSuccess. The retry loop keeps
// going after the breaker opens — the segment's own budget decides when
// it deadletters — while attemptUploads' Allow check stops new segments
// being claimed until the half-open probe.
//
// Returns nil on delivery, errClaimRaceLost when a sibling worker won the
// claim, errDeadlettered (wrapping the cause) after a DLQ transition,
// errShutdown or ctx.Err() when interrupted, and any other error for a
// local failure that never reached the destination.
func (t *track) uploadOne(ctx context.Context, sealedPath string) error {
	uploading, err := t.manager.Claim(sealedPath)
	if err != nil {
		// Only ENOENT means a sibling worker won the rename race. Every
		// other error — EACCES, EIO, EXDEV, a read-only filesystem, an
		// assertUnder violation — is a local failure, and returning nil
		// here reported the segment as delivered: the breaker recorded a
		// success, its consecutive-failure counter reset every pass, and
		// segments piled up in sealed/ with no error log and no signal.
		if errors.Is(err, os.ErrNotExist) {
			return errClaimRaceLost
		}
		t.opts.logger.Error("spool: claim sealed segment",
			slog.String("track", t.name),
			slog.String("path", sealedPath),
			slog.String("err", err.Error()))
		return fmt.Errorf("spool: claim %q: %w", sealedPath, err)
	}
	seg := t.describeSealed(uploading)

	backoff := t.opts.retry.BaseBackoff
	for attempt := 1; attempt <= t.opts.retry.MaxAttempts; attempt++ {
		uploadCtx := ctx
		var cancel context.CancelFunc
		if t.opts.uploadCtxFunc != nil {
			uploadCtx, cancel = t.opts.uploadCtxFunc(ctx)
		}
		err := t.opts.conn.Upload(uploadCtx, seg)
		if cancel != nil {
			cancel()
		}
		if err == nil {
			t.breaker.RecordSuccess()
			if cerr := t.manager.Complete(uploading); cerr != nil {
				t.opts.logger.Error("spool: complete after upload", slog.String("err", cerr.Error()))
			}
			t.uploadsOK.Add(1)
			return nil
		}
		if ctx.Err() != nil {
			// The attempt died with the worker's context, not the
			// destination: neither a failure nor a success for the
			// breaker, and the segment stays claimed for Recover.
			t.breaker.Release()
			return ctx.Err()
		}
		t.breaker.RecordFailure()
		if cc.IsPermanent(err) {
			return t.deadletter(uploading, err)
		}
		// Retryable.
		t.uploadsRetried.Add(1)
		if attempt >= t.opts.retry.MaxAttempts {
			return t.deadletter(uploading, err)
		}
		sleep := fullJitter(backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.stopCh:
			return errShutdown
		case <-time.After(sleep):
		}
		backoff = nextBackoff(backoff, t.opts.retry.Multiplier, t.opts.retry.MaxBackoff)
	}
	// Unreachable: the final iteration always returns from the
	// attempt-cap branch above. Kept so the compiler sees a return.
	return errShutdown
}

// deadletter moves a claimed segment to deadletter/, bumps the DLQ
// counter, and returns errDeadlettered wrapping cause so the caller can
// distinguish a handled quarantine from a transport failure.
func (t *track) deadletter(uploading string, cause error) error {
	if _, dErr := t.manager.Deadletter(uploading); dErr != nil {
		t.opts.logger.Error("spool: deadletter", slog.String("err", dErr.Error()))
	}
	t.uploadsDLQ.Add(1)
	return fmt.Errorf("%w: %w", errDeadlettered, cause)
}

// describeSealed builds the SealedSegment handed to Connector.Upload for
// the claimed file at uploading. The per-delivery stats come from the
// sidecar written at seal time (records, uncompressed bytes, ts range)
// plus os.Stat for the compressed size; a segment without a sidecar — one
// sealed by a pre-sidecar binary, or an active/ leftover Recover sealed —
// leaves them zero, and the connectors fall back to their upload clock
// for the date=/hour= partition exactly as before (#440).
func (t *track) describeSealed(uploading string) cc.SealedSegment {
	seg := cc.SealedSegment{
		Path:       uploading,
		DeliveryID: deliveryIDFromFilename(filepath.Base(uploading)),
		Connector:  t.name,
	}
	if info, err := os.Stat(uploading); err == nil {
		seg.Bytes = info.Size()
	}
	meta, ok, err := readSegmentMeta(uploading)
	if err != nil {
		t.opts.logger.Warn("spool: segment meta unreadable; partitioning by upload clock",
			slog.String("track", t.name),
			slog.String("path", uploading),
			slog.String("err", err.Error()))
		return seg
	}
	if ok {
		seg.Records = meta.Records
		seg.BytesUncompressed = meta.BytesUncompressed
		seg.TsMinNs = meta.TsMinNs
		seg.TsMaxNs = meta.TsMaxNs
	}
	return seg
}

func (t *track) stats() trackStats {
	sealed, _ := t.manager.ListSealed()
	return trackStats{
		Enqueued:        t.enqueued.Load(),
		DroppedRing:     t.droppedRing.Load(),
		Written:         t.written.Load(),
		WriteErrors:     t.writeErrors.Load(),
		SegmentsSealed:  t.segmentsSealed.Load(),
		UploadsOK:       t.uploadsOK.Load(),
		UploadsRetried:  t.uploadsRetried.Load(),
		UploadsDLQ:      t.uploadsDLQ.Load(),
		BreakerState:    t.breaker.State(),
		PendingSegments: len(sealed),
	}
}

// resetTimer safely resets time.Timer per the stdlib doc's
// drain-then-Reset pattern.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		// Drain the channel if a tick was already queued.
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

// deliveryIDFromFilename extracts the <unix_ns>-<seq> prefix from a
// segment filename and returns it as the DeliveryID. The prefix is
// already monotonic per-instance and stable across retries, which is
// what we need.
func deliveryIDFromFilename(name string) string {
	const ext = ".ndjson.zst"
	if len(name) > len(ext) && name[len(name)-len(ext):] == ext {
		return name[:len(name)-len(ext)]
	}
	return name
}
