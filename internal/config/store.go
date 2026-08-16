package config

import (
	"sync"
	"sync/atomic"
)

// Store owns the live ResolvedConfig and brokers updates to it. It is the
// single point of truth for the running gateway's configuration; every
// consumer that previously held a *ResolvedConfig now holds a *Store and
// reads through Snapshot.
//
// The pointer is swapped atomically. Readers never block, never lock, and
// never see a torn ResolvedConfig — Replace publishes the new snapshot
// with a single atomic write and fires subscribers afterwards. Writers
// (Phase 2: admin write endpoints) clone the current snapshot, mutate the
// clone, Validate it, and then call Replace.
//
// Subscribers receive a callback for every successful Replace and for the
// snapshot in effect at the moment they Subscribe — that's how consumers
// with pre-derived state (the routing.Router compiles its pattern set
// once, not per request) keep their derived caches synchronised with the
// active snapshot without a separate init path.
type Store struct {
	// current carries the live snapshot. Readers use atomic load; the
	// pointer changes only inside Replace.
	current atomic.Pointer[ResolvedConfig]

	// mu guards subs and serialises Replace calls. The read path
	// (Snapshot) never touches it.
	mu sync.Mutex

	// subs is the list of callbacks fired on every Replace and once
	// immediately on Subscribe. The slice is copied under mu before
	// dispatch so subscribers may Subscribe new callbacks from inside
	// their own callback without deadlocking.
	subs []func(*ResolvedConfig)
}

// NewStore returns a Store seeded with initial. initial must be non-nil
// — there is no "config not yet loaded" state at runtime; the gateway
// either has a validated ResolvedConfig or refuses to start.
func NewStore(initial *ResolvedConfig) *Store {
	if initial == nil {
		panic("config: NewStore: initial snapshot is nil")
	}
	s := &Store{}
	s.current.Store(initial)
	return s
}

// Snapshot returns the live ResolvedConfig pointer. The returned pointer
// is stable for the lifetime of the snapshot — even after a Replace, the
// previous pointer remains a valid read of the configuration that was
// active when the request started, so callers may keep it across multiple
// lookups inside a single request without risk of mid-request shear.
func (s *Store) Snapshot() *ResolvedConfig {
	return s.current.Load()
}

// Subscribe registers fn to fire on every successful Replace and invokes
// fn immediately with the current snapshot. The immediate call lets
// subscribers (e.g. routing.Router) initialise their derived state in
// one place rather than splitting between construction and Subscribe.
//
// Callbacks are dispatched synchronously from Replace. Subscribers must
// not block — heavy work goes on its own goroutine.
func (s *Store) Subscribe(fn func(*ResolvedConfig)) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	s.subs = append(s.subs, fn)
	cur := s.current.Load()
	s.mu.Unlock()
	fn(cur)
}

// Replace publishes next as the live snapshot and dispatches it to every
// registered subscriber. next must be non-nil; callers are responsible
// for cloning + validating before they call here (the admin write path
// does Clone → mutate → Validate → persist → Replace, in that order —
// CLAUDE.md invariant 9; see internal/admin.commitClone).
//
// Replace serialises with other Replace calls under mu but does not block
// readers. The subscribe-list is snapshotted under mu and dispatched
// outside the critical section so a slow subscriber cannot stall an
// unrelated Replace.
func (s *Store) Replace(next *ResolvedConfig) {
	if next == nil {
		panic("config: Store.Replace: next is nil")
	}
	s.mu.Lock()
	s.current.Store(next)
	subs := make([]func(*ResolvedConfig), len(s.subs))
	copy(subs, s.subs)
	s.mu.Unlock()
	for _, fn := range subs {
		fn(next)
	}
}
