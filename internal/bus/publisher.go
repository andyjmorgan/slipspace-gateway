package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/vmihailenco/msgpack/v5"
)

// Defaults for the Publisher Options.
const (
	defaultQueueSize      = 10000
	defaultWorkers        = 2
	defaultStashThreshold = 768 * 1024
	subjectPrefix         = "gateway."
)

// JetStreamPublisher is the narrow subset of jetstream.JetStream the bus
// needs at runtime. Concrete jetstream.JetStream values satisfy it.
type JetStreamPublisher interface {
	Publish(ctx context.Context, subject string, payload []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

// ObjectPutter is the narrow subset of jetstream.ObjectStore the bus needs
// to stash large payloads. The real jetstream.ObjectStore satisfies it.
type ObjectPutter interface {
	PutBytes(ctx context.Context, name string, data []byte) (*jetstream.ObjectInfo, error)
}

// Stats is a point-in-time snapshot of publisher counters.
type Stats struct {
	Published uint64

	Dropped uint64

	Stashed uint64
}

// Options configures a Publisher. JS is required; ObjectStore is optional
// — when nil, oversized payloads are published inline and a warning is
// logged.
type Options struct {
	JS JetStreamPublisher

	ObjectStore ObjectPutter

	// StashBucket is recorded on the ObjectRef for stashed payloads.
	// Defaults to DefaultStreamConfig().StashBucket when empty.
	StashBucket string

	QueueSize int

	Workers int

	StashThreshold int

	Logger *slog.Logger
}

// Publisher is the non-blocking event publisher. Publish enqueues or drops;
// worker goroutines drain the queue and ship envelopes to JetStream.
type Publisher struct {
	js          JetStreamPublisher
	store       ObjectPutter
	stashBucket string

	queue chan Envelope

	threshold int
	workers   int

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup

	dropCnt    atomic.Uint64
	publishCnt atomic.Uint64
	stashCnt   atomic.Uint64

	logger *slog.Logger
}

// New constructs a Publisher. It does not spawn workers — call Start.
func New(opts Options) *Publisher {
	queueSize := opts.QueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	threshold := opts.StashThreshold
	if threshold <= 0 {
		threshold = defaultStashThreshold
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	bucket := opts.StashBucket
	if bucket == "" {
		bucket = DefaultObjectStoreBucket
	}

	return &Publisher{
		js:          opts.JS,
		store:       opts.ObjectStore,
		stashBucket: bucket,
		queue:       make(chan Envelope, queueSize),
		threshold:   threshold,
		workers:     workers,
		stopCh:      make(chan struct{}),
		logger:      logger,
	}
}

// Start spawns worker goroutines that drain the queue until ctx is
// cancelled or Stop is called.
func (p *Publisher) Start(ctx context.Context) {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.run(ctx)
	}
}

// Stop signals workers to exit and waits up to timeout for them to finish
// draining the queue. Returns true if all workers exited cleanly.
//
// Stop closes the internal queue, so Publish must not be called after
// Stop returns.
func (p *Publisher) Stop(timeout time.Duration) bool {
	p.stopOnce.Do(func() { close(p.stopCh) })

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Publish enqueues env for asynchronous dispatch. If the queue is full it
// drops the event and increments the drop counter. It never blocks.
func (p *Publisher) Publish(env Envelope) {
	if env.SchemaVersion == 0 {
		env.SchemaVersion = SchemaVersion
	}
	select {
	case p.queue <- env:
	default:
		p.dropCnt.Add(1)
	}
}

// Stats returns a snapshot of the publisher's counters.
func (p *Publisher) Stats() Stats {
	return Stats{
		Published: p.publishCnt.Load(),
		Dropped:   p.dropCnt.Load(),
		Stashed:   p.stashCnt.Load(),
	}
}

// QueueLen reports the current number of envelopes waiting to be
// dispatched. Exposed for diagnostics and tests.
func (p *Publisher) QueueLen() int {
	return len(p.queue)
}

func (p *Publisher) run(ctx context.Context) {
	defer p.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			p.drainRemaining(ctx)
			return
		case env, ok := <-p.queue:
			if !ok {
				return
			}
			p.handle(ctx, env)
		}
	}
}

func (p *Publisher) drainRemaining(ctx context.Context) {
	for {
		select {
		case env, ok := <-p.queue:
			if !ok {
				return
			}
			p.handle(ctx, env)
		default:
			return
		}
	}
}

func (p *Publisher) handle(ctx context.Context, env Envelope) {
	if err := p.dispatch(ctx, env); err != nil {
		p.dropCnt.Add(1)
		p.logger.WarnContext(ctx, "bus dispatch failed",
			slog.String("event_id", env.EventID),
			slog.String("event_type", env.EventType),
			slog.String("error", err.Error()),
		)
		return
	}
	p.publishCnt.Add(1)
}

func (p *Publisher) dispatch(ctx context.Context, env Envelope) error {
	if len(env.InlinePayload) > p.threshold {
		switch {
		case p.store != nil:
			ref, err := p.stash(ctx, env)
			if err != nil {
				return fmt.Errorf("bus: stash: %w", err)
			}
			env.Mode = PayloadStashed
			env.ObjectRef = ref
			env.InlinePayload = nil
			p.stashCnt.Add(1)
		default:
			p.logger.WarnContext(ctx, "bus oversized payload published inline (no object store configured)",
				slog.String("event_id", env.EventID),
				slog.Int("size", len(env.InlinePayload)),
				slog.Int("threshold", p.threshold),
			)
		}
	}

	data, err := msgpack.Marshal(env)
	if err != nil {
		return fmt.Errorf("bus: msgpack marshal: %w", err)
	}

	subject := subjectPrefix + env.EventType
	if _, err := p.js.Publish(ctx, subject, data); err != nil {
		return fmt.Errorf("bus: publish subject=%s: %w", subject, err)
	}
	return nil
}

func (p *Publisher) stash(ctx context.Context, env Envelope) (*ObjectRef, error) {
	if env.EventID == "" {
		return nil, errors.New("envelope missing event id")
	}
	size := int64(len(env.InlinePayload))
	info, err := p.store.PutBytes(ctx, env.EventID, env.InlinePayload)
	if err != nil {
		return nil, fmt.Errorf("put bytes: %w", err)
	}
	// Clamp at math.MaxInt64 — the wire field is int64 and the store
	// reports the put as uint64. A >8 EiB payload is unreachable in
	// practice but gosec rightly objects to the unchecked cast.
	if info != nil && info.Size > 0 && info.Size <= uint64(math.MaxInt64) {
		size = int64(info.Size)
	}
	return &ObjectRef{
		Bucket: p.stashBucket,
		Key:    env.EventID,
		Size:   size,
	}, nil
}
