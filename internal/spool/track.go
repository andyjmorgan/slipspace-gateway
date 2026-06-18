package spool

import (
	"context"
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
	UploadInFlight  bool
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
// connector. Honours the per-destination circuit breaker.
func (t *track) attemptUploads(ctx context.Context) {
	if !t.breaker.Allow() {
		return
	}
	sealed, err := t.manager.ListSealed()
	if err != nil {
		t.opts.logger.Error("spool: list sealed", slog.String("track", t.name), slog.String("err", err.Error()))
		return
	}
	for _, path := range sealed {
		if !t.breaker.Allow() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.stopCh:
			return
		default:
		}
		if err := t.uploadOne(ctx, path); err != nil {
			t.breaker.RecordFailure()
			return
		}
		t.breaker.RecordSuccess()
	}
}

// uploadOne claims one sealed segment, calls Connector.Upload with the
// retry schedule, and either Completes, deadletters, or leaves the
// segment in sealed/ depending on the outcome.
func (t *track) uploadOne(ctx context.Context, sealedPath string) error {
	uploading, err := t.manager.Claim(sealedPath)
	if err != nil {
		// Most likely a sibling worker claimed it first; benign.
		return nil
	}
	seg := cc.SealedSegment{
		Path:       uploading,
		DeliveryID: deliveryIDFromFilename(filepath.Base(uploading)),
		Connector:  t.name,
	}

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
			if cerr := t.manager.Complete(uploading); cerr != nil {
				t.opts.logger.Error("spool: complete after upload", slog.String("err", cerr.Error()))
			}
			t.uploadsOK.Add(1)
			return nil
		}
		if cc.IsPermanent(err) {
			if _, dErr := t.manager.Deadletter(uploading); dErr != nil {
				t.opts.logger.Error("spool: deadletter", slog.String("err", dErr.Error()))
			}
			t.uploadsDLQ.Add(1)
			return err
		}
		// Retryable.
		t.uploadsRetried.Add(1)
		if attempt >= t.opts.retry.MaxAttempts {
			if _, dErr := t.manager.Deadletter(uploading); dErr != nil {
				t.opts.logger.Error("spool: deadletter", slog.String("err", dErr.Error()))
			}
			t.uploadsDLQ.Add(1)
			return err
		}
		sleep := fullJitter(backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.stopCh:
			return nil
		case <-time.After(sleep):
		}
		backoff = nextBackoff(backoff, t.opts.retry.Multiplier, t.opts.retry.MaxBackoff)
	}
	return nil
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
