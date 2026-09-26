package spool

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
	"time"

	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/connector"
)

// defaultQueueSize is the per-track ring depth before drops kick in.
const defaultQueueSize = 10000

// ErrUnknownTrack is returned by UnregisterTrack when no track of that
// name is registered.
var ErrUnknownTrack = errors.New("spool: unknown track")

// ErrTrackStopTimeout is returned by UnregisterTrack when the track's
// goroutines did not exit within the caller's deadline even after the
// hard abort. The track is already unrouted (Enqueue no longer reaches
// it) and its on-disk segments are intact; only the goroutine join is
// outstanding.
var ErrTrackStopTimeout = errors.New("spool: track did not stop before deadline")

// Options configures a Spool.
type Options struct {
	// Root is the on-disk directory under which every track's
	// records/<name>/{active,sealed,uploading,deadletter,quarantine}
	// dirs live. Required.
	Root string

	// Logger is the structured logger used for spool-internal errors
	// (write/seal/upload failures). Defaults to slog.Default.
	Logger *slog.Logger

	// Now is the clock used for segment open timestamps + rotation
	// age + breaker timing. Defaults to time.Now. Tests pin this.
	Now func() time.Time
}

// RegisterTrackOptions configures one connector destination at
// RegisterTrack time.
type RegisterTrackOptions struct {
	// Connector is the destination implementation. Its Name() is the
	// key used by Enqueue to route records.
	Connector connector.Connector

	// QueueSize is the per-track bounded ring depth. Zero defaults
	// to defaultQueueSize.
	QueueSize int

	// Rotation policy for this track's active segment.
	Rotation RotationOpts

	// Retry policy applied to Connector.Upload failures.
	Retry RetryOpts

	// Breaker policy gating Upload attempts under sustained failure.
	Breaker BreakerOpts

	// UploadPollInterval is how often the uploader scans sealed/
	// outside of seal-kick events. Defaults to 5s.
	UploadPollInterval time.Duration

	// UploadAttemptTimeout, when non-zero, derives a per-attempt
	// context with this timeout via context.WithTimeout. Useful for
	// HTTP-backed connectors that should not run forever.
	UploadAttemptTimeout time.Duration
}

// Stats is a point-in-time snapshot of one Spool's counters.
type Stats struct {
	// Tracks maps connector.Name() to its TrackStats for every
	// registered track.
	Tracks map[string]TrackStats

	// Unrouted counts records Enqueue was asked to route to a connector
	// name with no registered track, keyed by that name. Each is a
	// record lost before any ring existed to drop it from — a binding
	// that references a connector whose track never registered (build
	// failure, or a live-added connector before its track came up).
	Unrouted map[string]uint64
}

// Spool is the disk-backed buffer that sits between the data plane's
// body-capture middleware and the upload workers shipping records to
// connector destinations. Construct with New, register destinations
// with RegisterTrack, then Start. Tracks may also be registered and
// unregistered after Start — that is how the gateway applies connector
// edits made through the admin write API without a restart. Enqueue is
// non-blocking and drops at full per-track-queue capacity — the request
// path must never stall on reporting backpressure.
type Spool struct {
	root   string
	logger *slog.Logger
	now    func() time.Time

	mu     sync.RWMutex
	tracks map[string]*track

	started bool
	ctx     context.Context

	// unroutedMu guards unrouted. Separate from mu because Enqueue holds
	// mu's read side while it counts a miss, and the miss path is rare
	// enough that a second lock beats promoting every Enqueue to a write
	// lock.
	unroutedMu sync.Mutex
	unrouted   map[string]uint64
}

// New constructs a Spool. It does not start any goroutines and touches
// no disk — the per-track directories are created by RegisterTrack.
func New(opts Options) (*Spool, error) {
	if opts.Root == "" {
		return nil, errors.New("spool: Options.Root is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Spool{
		root:     opts.Root,
		logger:   logger,
		now:      now,
		tracks:   make(map[string]*track),
		unrouted: make(map[string]uint64),
	}, nil
}

// RegisterTrack adds a destination. Names must be unique within a
// Spool — re-registering an existing name returns an error; unregister
// it first (UnregisterTrack) to replace a track's settings.
//
// Before Start the track is merely recorded; Start recovers its
// directories and launches its goroutines with every other track. After
// Start the track is recovered and started here, synchronously, so a
// record enqueued once RegisterTrack returns is routed. Recovery and
// construction run outside the spool lock — a large sealed/ backlog must
// not stall concurrent Enqueue calls on the request path.
func (s *Spool) RegisterTrack(opts RegisterTrackOptions) error {
	if opts.Connector == nil {
		return errors.New("spool: RegisterTrackOptions.Connector is required")
	}
	name := opts.Connector.Name()
	if name == "" {
		return errors.New("spool: connector Name() is empty")
	}

	s.mu.RLock()
	_, exists := s.tracks[name]
	started, ctx := s.started, s.ctx
	s.mu.RUnlock()
	if exists {
		return fmt.Errorf("spool: track %q already registered", name)
	}

	manager, err := NewManager(filepath.Join(s.root, "records", name))
	if err != nil {
		return fmt.Errorf("spool: track %q manager: %w", name, err)
	}

	queueSize := opts.QueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	tOpts := trackOptions{
		conn:       opts.Connector,
		queueSize:  queueSize,
		rotation:   opts.Rotation.withDefaults(),
		retry:      opts.Retry.withDefaults(),
		breaker:    opts.Breaker.withDefaults(),
		uploadPoll: opts.UploadPollInterval,
		now:        s.now,
		logger:     s.logger.With(slog.String("track", name)),
	}
	if opts.UploadAttemptTimeout > 0 {
		timeout := opts.UploadAttemptTimeout
		tOpts.uploadCtxFunc = func(parent context.Context) (context.Context, context.CancelFunc) {
			return context.WithTimeout(parent, timeout)
		}
	}
	t := newTrack(name, manager, tOpts)

	if started {
		// Live registration: the directory reconcile that Start would
		// have run for this track runs now, before the goroutines see
		// it. Same fail-closed rule — an unrecoverable directory is an
		// error, not a silently stranded backlog.
		if err := s.recoverTrack(ctx, t); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tracks[name]; exists {
		return fmt.Errorf("spool: track %q already registered", name)
	}
	s.tracks[name] = t
	if s.started {
		t.start(s.ctx)
	}
	return nil
}

// UnregisterTrack removes the named track: Enqueue stops routing to it
// immediately, then its drain goroutine flushes the ring into the active
// segment, seals it, and both goroutines exit. Waits up to timeout for
// the graceful stop; if that elapses the track's context is cancelled so
// an in-flight Upload aborts, and the join is retried for a further
// timeout before ErrTrackStopTimeout is returned.
//
// Nothing on disk is deleted. Sealed segments (and any segment left in
// uploading/ by an aborted upload) stay under records/<name>/ and are
// picked up by Recover the next time a track of that name registers —
// on a later RegisterTrack or on the next process start. This is the
// mechanism behind a live connector edit: unregister the old settings,
// register the new, and the backlog follows the name.
func (s *Spool) UnregisterTrack(name string, timeout time.Duration) error {
	s.mu.Lock()
	t, ok := s.tracks[name]
	if ok {
		delete(s.tracks, name)
	}
	started := s.started
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownTrack, name)
	}
	if !started {
		// Never started, so no goroutines to join.
		return nil
	}
	if t.stop(timeout) {
		return nil
	}
	t.abort()
	if t.stop(timeout) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrTrackStopTimeout, name)
}

// recoverTrack runs the startup directory reconcile for one track and
// logs the report when it did anything. See docs/spool.md "Recovery on
// startup".
func (s *Spool) recoverTrack(ctx context.Context, t *track) error {
	rep, err := Recover(t.manager)
	if err != nil {
		return fmt.Errorf("spool: recover track %q: %w", t.name, err)
	}
	if rep != (RecoveryReport{}) {
		s.logger.LogAttrs(ctx, slog.LevelInfo, "spool: track recovered after restart",
			slog.String("track", t.name),
			slog.Int("uploading_to_sealed", rep.RecoveredFromUploading),
			slog.Int("active_sealed", rep.SealedFromActive),
			slog.Int("active_quarantined", rep.QuarantinedFromActive),
		)
	}
	return nil
}

// Start spawns the drain + uploader goroutines for every registered
// track. Returns an error if Start is called twice. Starting with zero
// tracks is allowed: the spool then idles until a track is registered
// live, which is the state of a gateway booted with no spool-backed
// connector that later gains one through the admin write API.
//
// ctx is the parent context for all goroutines; cancellation triggers a
// best-effort drain and exit. Use Stop to wait on graceful shutdown
// with a deadline.
func (s *Spool) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errors.New("spool: already started")
	}
	// Reconcile each track's on-disk state before any goroutine runs:
	// fish uploading/ orphans back to sealed/, seal cleanly-decoding
	// active/ leftovers so they upload, quarantine torn ones. Synchronous
	// and fail-closed — a spool that can't reconcile its directories must
	// not start, because silently stranding audit/billing records is worse
	// than refusing to boot. See docs/spool.md "Recovery on startup".
	for _, t := range s.tracks {
		if err := s.recoverTrack(ctx, t); err != nil {
			return err
		}
	}
	s.started = true
	s.ctx = ctx
	for _, t := range s.tracks {
		t.start(ctx)
	}
	return nil
}

// Stop signals all tracks to drain their queues, seal active segments,
// and exit. Waits up to timeout for goroutines to finish. Returns
// true iff every track stopped within the deadline.
func (s *Spool) Stop(timeout time.Duration) bool {
	s.mu.RLock()
	tracks := make([]*track, 0, len(s.tracks))
	for _, t := range s.tracks {
		tracks = append(tracks, t)
	}
	s.mu.RUnlock()

	deadline := time.Now().Add(timeout)
	allOK := true
	for _, t := range tracks {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			remaining = time.Millisecond
		}
		if !t.stop(remaining) {
			allOK = false
		}
	}
	return allOK
}

// Enqueue routes rec to every named track that's registered. A name
// with no registered track drops the record and bumps that name's
// Stats.Unrouted counter — the loss is counted, never silent, because
// the caller only names connectors its configuration is bound to. The
// send is non-blocking; full track queues drop the record and increment
// that track's DroppedRing counter.
func (s *Spool) Enqueue(rec cc.Record, connectors ...string) {
	if len(connectors) == 0 {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, name := range connectors {
		if t, ok := s.tracks[name]; ok {
			t.enqueue(rec)
			continue
		}
		s.unroutedMu.Lock()
		s.unrouted[name]++
		s.unroutedMu.Unlock()
	}
}

// Stats returns a snapshot of per-track counters plus the per-name
// unrouted-drop counts. Safe to call concurrently with Enqueue.
func (s *Spool) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]TrackStats, len(s.tracks))
	for name, t := range s.tracks {
		out[name] = t.stats()
	}
	s.unroutedMu.Lock()
	unrouted := make(map[string]uint64, len(s.unrouted))
	for name, n := range s.unrouted {
		unrouted[name] = n
	}
	s.unroutedMu.Unlock()
	return Stats{Tracks: out, Unrouted: unrouted}
}

// TrackNames returns the registered track names in lexical order.
// Useful for tests that need deterministic iteration.
func (s *Spool) TrackNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.tracks))
	for n := range s.tracks {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
