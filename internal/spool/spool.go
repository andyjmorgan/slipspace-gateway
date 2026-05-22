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

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/connector"
)

// defaultQueueSize is the per-track ring depth before drops kick in.
const defaultQueueSize = 10000

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

// Stats is a point-in-time snapshot of one Spool's per-track counters.
type Stats struct {
	// Tracks maps connector.Name() to its trackStats.
	Tracks map[string]trackStats
}

// Spool is the disk-backed buffer that sits between the data plane's
// body-capture middleware and the upload workers shipping records to
// connector destinations. Construct with New, register destinations
// with RegisterTrack, then Start. Enqueue is non-blocking and drops
// at full per-track-queue capacity — the request path must never
// stall on reporting backpressure.
type Spool struct {
	root   string
	logger *slog.Logger
	now    func() time.Time

	mu     sync.RWMutex
	tracks map[string]*track

	started bool
	ctx     context.Context
}

// New constructs a Spool. It does not start any goroutines.
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
		root:   opts.Root,
		logger: logger,
		now:    now,
		tracks: make(map[string]*track),
	}, nil
}

// RegisterTrack adds a destination. Must be called before Start.
// Names must be unique within a Spool — re-registering an existing
// name returns an error.
func (s *Spool) RegisterTrack(opts RegisterTrackOptions) error {
	if opts.Connector == nil {
		return errors.New("spool: RegisterTrackOptions.Connector is required")
	}
	name := opts.Connector.Name()
	if name == "" {
		return errors.New("spool: connector Name() is empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errors.New("spool: RegisterTrack after Start")
	}
	if _, exists := s.tracks[name]; exists {
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

	s.tracks[name] = newTrack(name, manager, tOpts)
	return nil
}

// Start spawns the drain + uploader goroutines for every registered
// track. Returns an error if Start is called twice or with zero tracks.
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
	if len(s.tracks) == 0 {
		return errors.New("spool: no tracks registered")
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

// Enqueue routes rec to every named track that's registered. Names
// that aren't registered are silently ignored — the caller decided to
// not bind them, so dropping is the correct behaviour. The send is
// non-blocking; full track queues drop the record and increment that
// track's droppedRing counter.
func (s *Spool) Enqueue(rec cc.Record, connectors ...string) {
	if len(connectors) == 0 {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, name := range connectors {
		if t, ok := s.tracks[name]; ok {
			t.enqueue(rec)
		}
	}
}

// Stats returns a snapshot of per-track counters. Safe to call
// concurrently with Enqueue.
func (s *Spool) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]trackStats, len(s.tracks))
	for name, t := range s.tracks {
		out[name] = t.stats()
	}
	return Stats{Tracks: out}
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
