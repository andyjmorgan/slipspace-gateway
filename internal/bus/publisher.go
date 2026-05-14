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
	// Published counts envelopes successfully shipped to JetStream.
	Published uint64

	// Dropped counts envelopes refused on enqueue (queue full) or that
	// failed dispatch after enqueue (NATS unreachable, marshal error).
	// The two cases share a counter because either way the customer
	// observes a missing event.
	Dropped uint64

	// Stashed counts envelopes whose payload exceeded the threshold and
	// was uploaded to the Object Store instead of carried inline.
	Stashed uint64
}

// Options configures a Publisher. JS is required; ObjectStore is
// optional — when nil, oversized payloads are published inline and a
// warning is logged.
type Options struct {
	// JS is the JetStream publish target. Required.
	JS JetStreamPublisher

	// ObjectStore is the bucket used to stash oversized payloads. When
	// nil the publisher logs a warning and falls back to inline publish —
	// a deliberate degradation that keeps the request path unaffected
	// even in a misconfigured environment.
	ObjectStore ObjectPutter

	// StashBucket is recorded on the ObjectRef for stashed payloads.
	// Defaults to DefaultObjectStoreBucket when empty.
	StashBucket string

	// QueueSize is the capacity of the internal channel that buffers
	// envelopes between Publish and the worker goroutines. Defaults to
	// defaultQueueSize when zero.
	QueueSize int

	// Workers is the number of goroutines draining the queue. Defaults
	// to defaultWorkers when zero.
	Workers int

	// StashThreshold is the byte length above which an inline payload is
	// uploaded to the Object Store instead. Defaults to
	// defaultStashThreshold (768 KiB) when zero.
	StashThreshold int

	Logger *slog.Logger
}

// Publisher is the non-blocking event publisher. Publish enqueues or
// drops; worker goroutines drain the queue and ship envelopes to
// JetStream.
//
// The drop-on-full semantics are load-bearing: the request path must
// never block on reporting backpressure. See the "NATS Reporting" design
// note and CLAUDE.md's load-bearing invariants section.
type Publisher struct {
	// js is the JetStream publisher. Non-nil for the lifetime of the
	// Publisher.
	js JetStreamPublisher

	// store is the Object Store used for stashed payloads. Nil when the
	// caller did not configure one — dispatch logs and falls back to
	// inline publish for oversized envelopes.
	store ObjectPutter

	// stashBucket is the bucket name recorded on stashed envelopes.
	stashBucket string

	// queue is the bounded channel between producers (Publish) and
	// worker goroutines. Capacity is set at construction; full-queue
	// pressure manifests as drops, never as blocked callers.
	queue chan Envelope

	// threshold is the inline-vs-stashed cutoff in bytes.
	threshold int

	// workers is the number of run() goroutines spawned by Start.
	workers int

	// stopOnce guards stopCh so Stop is idempotent.
	stopOnce sync.Once

	// stopCh signals workers to drain remaining envelopes and exit.
	stopCh chan struct{}

	// wg tracks worker goroutines for Stop to await.
	wg sync.WaitGroup

	// dropCnt, publishCnt, stashCnt are lock-free counters exposed via
	// Stats. Always update via atomic ops.
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

// Publish enqueues env for asynchronous dispatch. It never blocks.
//
// If the queue is full, the envelope is dropped and Stats.Dropped is
// incremented — there is no retry, no in-memory replay buffer, no backoff.
// This non-blocking guarantee is a load-bearing invariant of the gateway:
// the request path must never wait on reporting backpressure. The next
// event is always more valuable than this one.
//
// SchemaVersion is stamped to SchemaVersion when callers leave it zero, so
// the on-wire envelope always carries a version.
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
