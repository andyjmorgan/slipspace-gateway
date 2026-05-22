package resilience

import (
	"sync"
	"time"

	contractsres "github.com/andyjmorgan/sluice-gateway/contracts/resilience"
)

// State is the circuit breaker's current lifecycle position.
//
// Lifecycle:
//
//	Closed → Open → HalfOpen → Closed
//	         ↑──────────┘  (any failure in HalfOpen)
//
// In v1.2 the state lives per-pod in-process. State does not persist
// across restarts; a fresh deploy starts every breaker in Closed.
// Cross-pod consistency is not provided — see milestone notes for the
// rationale and the BreakerStore swap design for v1.3+.
type State int

const (
	// StateClosed permits requests; failures accumulate in the
	// sliding window and may trip the breaker.
	StateClosed State = iota

	// StateOpen blocks requests until cooldown elapses, then
	// transitions to HalfOpen.
	StateOpen

	// StateHalfOpen permits up to HalfOpenSuccessThreshold probes.
	// Every consecutive success counts down; any failure reopens.
	StateHalfOpen
)

// String returns a human-readable state name for logs and metrics.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// BreakerStore is the orchestrator's circuit-breaker dependency.
// Implementations track per-target health and decide whether the
// orchestrator should attempt the target on each new request.
//
// The in-memory implementation lives per-pod. A future Redis-backed
// implementation would satisfy the same interface so the orchestrator
// stays unchanged; that's why the surface is narrow on purpose —
// every method either reports a boolean decision or records a single
// outcome.
//
// A nil cfg or cfg.Enabled=false disables the breaker for that
// (policy, target): Allow returns true, Record* are no-ops. This lets
// the orchestrator call into the store unconditionally without
// branching on "is CB enabled" at every call site.
type BreakerStore interface {
	// Allow returns true if a request to (policy, target) should
	// proceed. False when the breaker is Open, or HalfOpen with the
	// probe quota exhausted.
	Allow(policy, target string, cfg *contractsres.CircuitBreakerConfig) bool

	// RecordSuccess registers a successful attempt. Closed: feeds
	// the sliding window. HalfOpen: increments probe successes and
	// closes the breaker once HalfOpenSuccessThreshold is hit.
	RecordSuccess(policy, target string, cfg *contractsres.CircuitBreakerConfig)

	// RecordFailure registers a failed attempt. Closed: feeds the
	// sliding window and may trip the breaker. HalfOpen: reopens
	// immediately.
	RecordFailure(policy, target string, cfg *contractsres.CircuitBreakerConfig)

	// State reports the current lifecycle position for telemetry.
	// Returns StateClosed for unknown keys so a never-observed
	// (policy, target) pair reads as healthy.
	State(policy, target string) State

	// Snapshot returns the current breaker state of every observed
	// (policy, target). Used by the gateway.cb.state ObservableGauge
	// callback to report the full per-target state surface at every
	// metric collection. Implementations must be goroutine-safe.
	Snapshot() []BreakerSnapshot
}

// BreakerSnapshot is one (policy, target) → State observation drawn
// from BreakerStore.Snapshot(). Returned as a flat slice so the
// gauge callback can iterate without locking the store further.
type BreakerSnapshot struct {
	// Policy is the resilience policy name.
	Policy string

	// Target is the resilience target name.
	Target string

	// State is the current breaker lifecycle position.
	State State
}

// clock is the package-level time source the breaker reads. Tests
// stub it serially (no t.Parallel) following the same pattern as
// randomIntN — the race detector enforces the constraint.
var clock = time.Now

// StateListener is invoked on every state transition. PR-10 hooks
// the OTel counter here; PR-9 ships with a no-op default so the
// orchestrator can be wired without dragging telemetry along.
type StateListener func(policy, target string, from, to State, reason string)

// NewInMemoryBreakerStore returns the default per-pod BreakerStore.
// listener may be nil to opt out of transition notifications.
func NewInMemoryBreakerStore(listener StateListener) BreakerStore {
	if listener == nil {
		listener = func(string, string, State, State, string) {}
	}
	return &inMemoryStore{
		breakers: make(map[string]*breaker),
		listener: listener,
	}
}

type inMemoryStore struct {
	mu sync.Mutex

	breakers map[string]*breaker

	listener StateListener
}

func (s *inMemoryStore) getOrCreate(policy, target string, cfg *contractsres.CircuitBreakerConfig) *breaker {
	key := breakerKey(policy, target)
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.breakers[key]; ok {
		return b
	}
	window := cfg.SamplingDurationSeconds
	if window <= 0 {
		window = 60
	}
	b := &breaker{
		buckets:    make([]bucket, window),
		lastRotate: clock(),
	}
	s.breakers[key] = b
	return b
}

func breakerKey(policy, target string) string {
	return policy + "|" + target
}

func (s *inMemoryStore) Allow(policy, target string, cfg *contractsres.CircuitBreakerConfig) bool {
	if cfg == nil || !cfg.Enabled {
		return true
	}
	b := s.getOrCreate(policy, target, cfg)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := clock()
	b.advance(now, cfg, policy, target, s.listener)
	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		return false
	case StateHalfOpen:
		if b.halfOpenSuccesses < cfg.HalfOpenSuccessThreshold {
			return true
		}
		return false
	}
	return true
}

func (s *inMemoryStore) RecordSuccess(policy, target string, cfg *contractsres.CircuitBreakerConfig) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	b := s.getOrCreate(policy, target, cfg)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := clock()
	b.advance(now, cfg, policy, target, s.listener)
	b.buckets[b.bucketIdx].successes++
	if b.state == StateHalfOpen {
		b.halfOpenSuccesses++
		if b.halfOpenSuccesses >= cfg.HalfOpenSuccessThreshold {
			b.transition(StateClosed, now, "halfopen_success_threshold", policy, target, s.listener)
		}
	}
}

func (s *inMemoryStore) RecordFailure(policy, target string, cfg *contractsres.CircuitBreakerConfig) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	b := s.getOrCreate(policy, target, cfg)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := clock()
	b.advance(now, cfg, policy, target, s.listener)
	b.buckets[b.bucketIdx].failures++
	switch b.state {
	case StateClosed:
		if b.shouldTrip(cfg) {
			b.transition(StateOpen, now, "trip", policy, target, s.listener)
		}
	case StateHalfOpen:
		b.transition(StateOpen, now, "halfopen_failure", policy, target, s.listener)
	}
}

func (s *inMemoryStore) State(policy, target string) State {
	s.mu.Lock()
	b, ok := s.breakers[breakerKey(policy, target)]
	s.mu.Unlock()
	if !ok {
		return StateClosed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Snapshot collects a copy of every (policy, target) → State row.
// The outer store mutex protects the map read; per-breaker mu
// protects each state load. The breakerKey is split on '|' (the
// canonical encoding from breakerKey) — operators cannot construct
// keys that legitimately contain '|' because policy/target names are
// validated as identifiers at config-load time.
func (s *inMemoryStore) Snapshot() []BreakerSnapshot {
	s.mu.Lock()
	keys := make([]string, 0, len(s.breakers))
	for k := range s.breakers {
		keys = append(keys, k)
	}
	s.mu.Unlock()

	out := make([]BreakerSnapshot, 0, len(keys))
	for _, k := range keys {
		policy, target := splitBreakerKey(k)
		out = append(out, BreakerSnapshot{
			Policy: policy,
			Target: target,
			State:  s.State(policy, target),
		})
	}
	return out
}

// splitBreakerKey reverses breakerKey's "policy|target" encoding.
// A missing separator collapses to (k, "") so the gauge always
// emits something rather than dropping the row.
func splitBreakerKey(k string) (policy, target string) {
	for i := 0; i < len(k); i++ {
		if k[i] == '|' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

// breaker is the per-(policy, target) state. Its own mu protects
// state machine fields and the sliding-window buckets; the
// inMemoryStore's outer mutex only protects map mutation.
type breaker struct {
	mu sync.Mutex

	state State

	buckets []bucket

	// bucketIdx is the index of the bucket currently accumulating
	// counts. advance() rotates it forward as time moves on.
	bucketIdx int

	// lastRotate is the wall-clock time the bucket pointer last
	// moved. Used to decide how many buckets to skip on the next
	// touch.
	lastRotate time.Time

	// openedAt is the timestamp the breaker entered Open, so
	// advance() can decide when CooldownSeconds has elapsed and
	// the breaker should transition to HalfOpen.
	openedAt time.Time

	// halfOpenSuccesses counts consecutive successes during the
	// HalfOpen probe window. Reset on entering HalfOpen and on
	// closing.
	halfOpenSuccesses int
}

type bucket struct {
	successes int
	failures  int
}

// advance rolls the sliding window forward and applies any
// time-based state transition (Open → HalfOpen on cooldown elapse).
// Caller must hold b.mu.
func (b *breaker) advance(now time.Time, cfg *contractsres.CircuitBreakerConfig, policy, target string, listener StateListener) {
	elapsed := int(now.Sub(b.lastRotate) / time.Second)
	if elapsed > 0 {
		if elapsed >= len(b.buckets) {
			for i := range b.buckets {
				b.buckets[i] = bucket{}
			}
			b.bucketIdx = 0
		} else {
			for i := 0; i < elapsed; i++ {
				b.bucketIdx = (b.bucketIdx + 1) % len(b.buckets)
				b.buckets[b.bucketIdx] = bucket{}
			}
		}
		b.lastRotate = now
	}

	if b.state == StateOpen {
		cooldown := time.Duration(cfg.CooldownSeconds) * time.Second
		if cooldown > 0 && now.Sub(b.openedAt) >= cooldown {
			b.transition(StateHalfOpen, now, "cooldown_elapsed", policy, target, listener)
		}
	}
}

// shouldTrip evaluates the trip condition against the sliding
// window. Caller must hold b.mu.
//
// Trip requires both:
//   - At least MinimumThroughput samples in the window (so a couple
//     of cold-start failures don't trip a fresh breaker).
//   - Either FailureThreshold absolute count OR FailureRateThreshold
//     proportional rate is breached. When both are configured both
//     must be breached.
func (b *breaker) shouldTrip(cfg *contractsres.CircuitBreakerConfig) bool {
	var success, failure int
	for _, bk := range b.buckets {
		success += bk.successes
		failure += bk.failures
	}
	total := success + failure
	if cfg.MinimumThroughput > 0 && total < cfg.MinimumThroughput {
		return false
	}
	if total == 0 {
		return false
	}

	absoluteOK := cfg.FailureThreshold == 0 || failure >= cfg.FailureThreshold
	rateOK := cfg.FailureRateThreshold == 0 || float64(failure)/float64(total) > cfg.FailureRateThreshold

	if cfg.FailureThreshold > 0 && cfg.FailureRateThreshold > 0 {
		return absoluteOK && rateOK
	}
	if cfg.FailureThreshold > 0 {
		return absoluteOK
	}
	if cfg.FailureRateThreshold > 0 {
		return rateOK
	}
	return false
}

// transition moves the breaker into to, notifies the listener, and
// resets the appropriate accounting. Caller must hold b.mu.
func (b *breaker) transition(to State, now time.Time, reason, policy, target string, listener StateListener) {
	from := b.state
	if from == to {
		return
	}
	b.state = to
	switch to {
	case StateOpen:
		b.openedAt = now
	case StateHalfOpen:
		b.halfOpenSuccesses = 0
	case StateClosed:
		b.halfOpenSuccesses = 0
		// Clear the window on close so the new healthy span starts
		// fresh — otherwise old failures linger and a single new
		// failure could re-trip the breaker.
		for i := range b.buckets {
			b.buckets[i] = bucket{}
		}
		b.bucketIdx = 0
		b.lastRotate = now
	}
	listener(policy, target, from, to, reason)
}

// noopBreakerStore satisfies BreakerStore without doing anything.
// Returned as the orchestrator's default when no store is wired,
// so callers don't have to branch on nil at every Allow / Record
// site. Equivalent to "every (policy, target) is always healthy."
type noopBreakerStore struct{}

func (noopBreakerStore) Allow(string, string, *contractsres.CircuitBreakerConfig) bool {
	return true
}

func (noopBreakerStore) RecordSuccess(string, string, *contractsres.CircuitBreakerConfig) {
	// no-op
	_ = "noop"
}

func (noopBreakerStore) RecordFailure(string, string, *contractsres.CircuitBreakerConfig) {
	// no-op
	_ = "noop"
}

func (noopBreakerStore) State(string, string) State { return StateClosed }

func (noopBreakerStore) Snapshot() []BreakerSnapshot { return nil }
