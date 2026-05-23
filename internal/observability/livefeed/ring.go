package livefeed

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrCapacityNonPositive is returned by NewRing when the caller asks for
// a ring of capacity <= 0. Callers should gate Ring construction on the
// env-var-derived capacity being positive and skip wiring otherwise.
var ErrCapacityNonPositive = errors.New("livefeed: capacity must be > 0")

// Ring is a bounded in-memory store of completed-request entries plus a
// fan-out broadcaster for SSE subscribers.
//
// Append is non-blocking: it takes the write lock briefly to insert into
// the ring and to copy the subscriber set, then releases the lock before
// sending to each subscriber. Per-subscriber sends are non-blocking — a
// slow consumer increments its own drop counter rather than back-pressuring
// the writer.
type Ring struct {
	capacity int

	mu      sync.RWMutex
	entries []Entry              // oldest-first; len <= capacity
	subs    map[*subscriber]bool // active SSE subscribers
}

// NewRing constructs a Ring of the given capacity. Capacity must be > 0.
func NewRing(capacity int) (*Ring, error) {
	if capacity <= 0 {
		return nil, ErrCapacityNonPositive
	}
	return &Ring{
		capacity: capacity,
		entries:  make([]Entry, 0, capacity),
		subs:     map[*subscriber]bool{},
	}, nil
}

// Capacity returns the configured ring size.
func (r *Ring) Capacity() int { return r.capacity }

// Append inserts entry at the tail, evicting the oldest when full, and
// broadcasts to every active subscriber. Safe to call concurrently from
// the request path.
func (r *Ring) Append(entry Entry) {
	r.mu.Lock()
	if len(r.entries) < r.capacity {
		r.entries = append(r.entries, entry)
	} else {
		copy(r.entries, r.entries[1:])
		r.entries[len(r.entries)-1] = entry
	}
	// Snapshot the subscriber set so we can release the write lock
	// before doing the (still non-blocking) sends. This keeps Append
	// off the hot path of any subscriber's goroutine.
	subs := make([]*subscriber, 0, len(r.subs))
	for s := range r.subs {
		subs = append(subs, s)
	}
	r.mu.Unlock()

	for _, s := range subs {
		s.deliver(entry)
	}
}

// Recent returns the up-to-limit most recent entries, oldest-first. When
// limit <= 0 or exceeds the current ring length, the full ring is
// returned. The returned slice is freshly allocated; callers may mutate
// it without affecting the Ring.
func (r *Ring) Recent(limit int) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := len(r.entries)
	if limit <= 0 || limit > n {
		limit = n
	}
	if limit == 0 {
		return nil
	}
	out := make([]Entry, limit)
	copy(out, r.entries[n-limit:])
	return out
}

// Subscription is the handle returned by Subscribe. C delivers Entries as
// they are appended; Close detaches the subscriber and is idempotent.
// Dropped returns the count of entries the subscriber missed because its
// channel was full when Append fired.
type Subscription struct {
	sub *subscriber
}

// C returns the receive channel. Closed when the subscription is
// detached (via Close or via ctx cancellation if the subscriber was
// created with SubscribeCtx).
func (s *Subscription) C() <-chan Entry { return s.sub.ch }

// Dropped returns the cumulative count of entries the subscriber missed
// because its delivery channel was full at Append time.
func (s *Subscription) Dropped() uint64 { return s.sub.dropped.Load() }

// Close detaches the subscriber from the ring. Safe to call multiple
// times.
func (s *Subscription) Close() { s.sub.ring.detach(s.sub) }

// Subscribe registers a new SSE subscriber and returns its Subscription.
// bufSize bounds the per-subscriber delivery channel; a value of 0
// defaults to 32 (a few seconds of headroom at any realistic gateway
// throughput before the slow-client drop semantics kick in).
func (r *Ring) Subscribe(bufSize int) *Subscription {
	if bufSize <= 0 {
		bufSize = 32
	}
	s := &subscriber{
		ring: r,
		ch:   make(chan Entry, bufSize),
	}
	r.mu.Lock()
	r.subs[s] = true
	r.mu.Unlock()
	return &Subscription{sub: s}
}

// subscriber is the per-Subscription state owned by the Ring.
//
// mu serialises deliver and detach so the channel send in deliver cannot
// race with the close in detach. Without it, an atomic `closed` flag
// only narrows the TOCTOU window — the load can return false, detach can
// then close the channel, and the subsequent send panics with "send on
// closed channel". The race detector flagged this between concurrent
// Append and HTTP-handler teardown; under production load the panic
// would crash the gateway process.
type subscriber struct {
	ring    *Ring
	ch      chan Entry
	dropped atomic.Uint64

	mu     sync.Mutex
	closed bool
}

// deliver does a non-blocking send onto the subscriber's channel,
// incrementing the per-subscriber drop counter if the buffer is full.
// mu is held only for the duration of the non-blocking select, so
// contention against a concurrent detach is bounded by the select
// itself — no I/O happens under the lock.
func (s *subscriber) deliver(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- e:
	default:
		s.dropped.Add(1)
	}
}

// detach is called by Subscription.Close. Idempotent. Takes the
// subscriber mutex around close(s.ch) so any concurrent deliver
// observes s.closed=true (under the same mutex) and returns without
// touching the channel.
func (r *Ring) detach(s *subscriber) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.ch)
	s.mu.Unlock()

	r.mu.Lock()
	delete(r.subs, s)
	r.mu.Unlock()
}

// SubscriberCount returns the number of active subscribers. Useful for
// tests and for surfacing a debug counter to the admin pane.
func (r *Ring) SubscriberCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.subs)
}
