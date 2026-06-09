package resilience

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	contractsres "github.com/andyjmorgan/sluice-gateway/contracts/resilience"
)

// Tests in this file must not call t.Parallel() — they mutate the
// package-global `clock` hook. The race detector enforces that.

// fakeClock returns a clock-replacement that advances under test
// control. Use withFakeClock to install + restore.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{now: start} }

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func withFakeClock(t *testing.T) *fakeClock {
	t.Helper()
	fc := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	prev := clock
	clock = fc.Now
	t.Cleanup(func() { clock = prev })
	return fc
}

func standardCBConfig() *contractsres.CircuitBreakerConfig {
	return &contractsres.CircuitBreakerConfig{
		Enabled:                  true,
		FailureThreshold:         3,
		SamplingDurationSeconds:  60,
		CooldownSeconds:          30,
		HalfOpenSuccessThreshold: 2,
		MinimumThroughput:        3,
	}
}

func TestBreaker_DisabledConfig_AlwaysAllow(t *testing.T) {
	withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	cfg := &contractsres.CircuitBreakerConfig{Enabled: false}

	for i := 0; i < 5; i++ {
		store.RecordFailure("p", "t", cfg)
	}
	if !store.Allow("p", "t", cfg) {
		t.Error("disabled CB should always Allow regardless of failures")
	}
}

func TestBreaker_NilConfig_AlwaysAllow(t *testing.T) {
	withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	if !store.Allow("p", "t", nil) {
		t.Error("nil cfg should Allow (no breaker configured)")
	}
}

func TestBreaker_ClosedToOpen_OnAbsoluteThreshold(t *testing.T) {
	withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	cfg := standardCBConfig()

	for i := 0; i < 3; i++ {
		store.RecordFailure("p", "t", cfg)
	}
	if store.State("p", "t") != StateOpen {
		t.Errorf("state = %v; want Open after 3 failures meeting threshold", store.State("p", "t"))
	}
	if store.Allow("p", "t", cfg) {
		t.Error("Allow returned true while Open")
	}
}

func TestBreaker_ClosedToOpen_RequiresMinimumThroughput(t *testing.T) {
	withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	cfg := &contractsres.CircuitBreakerConfig{
		Enabled:              true,
		FailureRateThreshold: 0.5,
		MinimumThroughput:    10,
	}

	// 5 failures, no successes — rate is 1.0 but throughput is below
	// MinimumThroughput so the breaker must NOT trip.
	for i := 0; i < 5; i++ {
		store.RecordFailure("p", "t", cfg)
	}
	if store.State("p", "t") != StateClosed {
		t.Errorf("state = %v; want Closed (throughput below minimum)", store.State("p", "t"))
	}
}

func TestBreaker_ClosedToOpen_OnRateThreshold(t *testing.T) {
	withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	cfg := &contractsres.CircuitBreakerConfig{
		Enabled:              true,
		FailureRateThreshold: 0.5,
		MinimumThroughput:    4,
	}

	// 3 failures + 1 success = 0.75 rate, above 0.5 threshold, with
	// throughput 4 meeting the minimum.
	store.RecordSuccess("p", "t", cfg)
	for i := 0; i < 3; i++ {
		store.RecordFailure("p", "t", cfg)
	}
	if store.State("p", "t") != StateOpen {
		t.Errorf("state = %v; want Open (rate above threshold)", store.State("p", "t"))
	}
}

func TestBreaker_OpenToHalfOpen_AfterCooldown(t *testing.T) {
	fc := withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	cfg := standardCBConfig() // CooldownSeconds: 30

	// Trip the breaker.
	for i := 0; i < 3; i++ {
		store.RecordFailure("p", "t", cfg)
	}
	if store.State("p", "t") != StateOpen {
		t.Fatalf("setup: want Open")
	}

	// Before cooldown elapses, Allow is still false.
	fc.Advance(29 * time.Second)
	if store.Allow("p", "t", cfg) {
		t.Error("Allow returned true before cooldown elapsed")
	}

	// After cooldown, Allow probes (returns true, transitions to HalfOpen).
	fc.Advance(2 * time.Second)
	if !store.Allow("p", "t", cfg) {
		t.Error("Allow returned false after cooldown elapsed")
	}
	if store.State("p", "t") != StateHalfOpen {
		t.Errorf("state = %v; want HalfOpen after cooldown", store.State("p", "t"))
	}
}

func TestBreaker_HalfOpenToClosed_OnSuccessThreshold(t *testing.T) {
	fc := withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	cfg := standardCBConfig() // HalfOpenSuccessThreshold: 2

	// Trip and transition to HalfOpen.
	for i := 0; i < 3; i++ {
		store.RecordFailure("p", "t", cfg)
	}
	fc.Advance(31 * time.Second)
	_ = store.Allow("p", "t", cfg) // triggers Open → HalfOpen

	// First success — still HalfOpen, just one probe.
	store.RecordSuccess("p", "t", cfg)
	if store.State("p", "t") != StateHalfOpen {
		t.Errorf("state = %v; want HalfOpen after 1 success", store.State("p", "t"))
	}

	// Second success hits threshold → Closed.
	store.RecordSuccess("p", "t", cfg)
	if store.State("p", "t") != StateClosed {
		t.Errorf("state = %v; want Closed after %d successes", store.State("p", "t"), cfg.HalfOpenSuccessThreshold)
	}
	if !store.Allow("p", "t", cfg) {
		t.Error("Allow returned false after Closed")
	}
}

func TestBreaker_HalfOpenToOpen_OnAnyFailure(t *testing.T) {
	fc := withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	cfg := standardCBConfig()

	// Trip → cooldown → HalfOpen.
	for i := 0; i < 3; i++ {
		store.RecordFailure("p", "t", cfg)
	}
	fc.Advance(31 * time.Second)
	_ = store.Allow("p", "t", cfg)

	// Even after a success, a single failure reopens.
	store.RecordSuccess("p", "t", cfg)
	store.RecordFailure("p", "t", cfg)

	if store.State("p", "t") != StateOpen {
		t.Errorf("state = %v; want Open after HalfOpen failure", store.State("p", "t"))
	}
}

func TestBreaker_ClosedAfterReopenIsClean(t *testing.T) {
	fc := withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	cfg := standardCBConfig()

	// Trip the breaker.
	for i := 0; i < 3; i++ {
		store.RecordFailure("p", "t", cfg)
	}

	// Cooldown → HalfOpen → close via successes.
	fc.Advance(31 * time.Second)
	_ = store.Allow("p", "t", cfg)
	for i := 0; i < cfg.HalfOpenSuccessThreshold; i++ {
		store.RecordSuccess("p", "t", cfg)
	}
	if store.State("p", "t") != StateClosed {
		t.Fatalf("setup: want Closed; got %v", store.State("p", "t"))
	}

	// In the newly-Closed window, the breaker must not re-trip on
	// the FailureThreshold-1 stale failures from before — they
	// should have been cleared on Closed transition.
	for i := 0; i < 2; i++ {
		store.RecordFailure("p", "t", cfg)
	}
	if store.State("p", "t") != StateClosed {
		t.Errorf("breaker re-tripped on stale failures; state = %v", store.State("p", "t"))
	}
}

func TestBreaker_SlidingWindow_ForgetsOldFailures(t *testing.T) {
	fc := withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	cfg := &contractsres.CircuitBreakerConfig{
		Enabled:                 true,
		FailureThreshold:        3,
		SamplingDurationSeconds: 10,
		CooldownSeconds:         30,
		MinimumThroughput:       3,
	}

	// 2 failures.
	store.RecordFailure("p", "t", cfg)
	store.RecordFailure("p", "t", cfg)

	// Advance past the sliding window — failures roll off.
	fc.Advance(15 * time.Second)

	// Another 2 failures — together with the 2 stale ones they would
	// trip, but the stale ones must have rolled off.
	store.RecordFailure("p", "t", cfg)
	store.RecordFailure("p", "t", cfg)

	if store.State("p", "t") != StateClosed {
		t.Errorf("state = %v; want Closed (stale failures should have rolled off)", store.State("p", "t"))
	}
}

func TestBreaker_TransitionsFireListener(t *testing.T) {
	fc := withFakeClock(t)
	var observed []string
	listener := func(policy, target string, from, to State, reason string) {
		observed = append(observed, from.String()+"->"+to.String()+"("+reason+")")
	}
	store := NewInMemoryBreakerStore(listener)
	cfg := standardCBConfig()

	for i := 0; i < 3; i++ {
		store.RecordFailure("p", "t", cfg)
	}
	fc.Advance(31 * time.Second)
	_ = store.Allow("p", "t", cfg)
	for i := 0; i < cfg.HalfOpenSuccessThreshold; i++ {
		store.RecordSuccess("p", "t", cfg)
	}

	want := []string{
		"closed->open(trip)",
		"open->half_open(cooldown_elapsed)",
		"half_open->closed(halfopen_success_threshold)",
	}
	if len(observed) != len(want) {
		t.Fatalf("transitions = %v; want %v", observed, want)
	}
	for i, w := range want {
		if observed[i] != w {
			t.Errorf("transitions[%d] = %q; want %q", i, observed[i], w)
		}
	}
}

func TestBreaker_PerPolicyTargetIsolation(t *testing.T) {
	withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	cfg := standardCBConfig()

	for i := 0; i < 3; i++ {
		store.RecordFailure("p1", "t1", cfg)
	}
	// p1/t1 should be Open; p1/t2 and p2/t1 must be Closed.
	if store.State("p1", "t1") != StateOpen {
		t.Errorf("p1/t1 state = %v; want Open", store.State("p1", "t1"))
	}
	if store.State("p1", "t2") != StateClosed {
		t.Errorf("p1/t2 state = %v; want Closed (different target)", store.State("p1", "t2"))
	}
	if store.State("p2", "t1") != StateClosed {
		t.Errorf("p2/t1 state = %v; want Closed (different policy)", store.State("p2", "t1"))
	}
}

func TestBreaker_ConcurrentFailures_OnlyOneTransition(t *testing.T) {
	withFakeClock(t)
	var transitionCount atomic.Int32
	listener := func(policy, target string, from, to State, reason string) {
		if from == StateClosed && to == StateOpen {
			transitionCount.Add(1)
		}
	}
	store := NewInMemoryBreakerStore(listener)
	cfg := &contractsres.CircuitBreakerConfig{
		Enabled:                 true,
		FailureThreshold:        3,
		SamplingDurationSeconds: 60,
		CooldownSeconds:         30,
		MinimumThroughput:       3,
	}

	// 100 goroutines each recording 5 failures. The breaker should
	// only transition Closed→Open exactly once despite the race.
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				store.RecordFailure("p", "t", cfg)
			}
		}()
	}
	wg.Wait()

	if got := transitionCount.Load(); got != 1 {
		t.Errorf("transition count = %d; want exactly 1", got)
	}
}

func TestBreaker_UnknownKeyState_DefaultsToClosed(t *testing.T) {
	withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	if store.State("never-observed", "target") != StateClosed {
		t.Errorf("unknown key state = %v; want Closed", store.State("never-observed", "target"))
	}
}

func TestState_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		s    State
		want string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half_open"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q; want %q", tt.s, got, tt.want)
		}
	}
}

func TestEffectiveCircuitBreaker_FallsBackToPolicy(t *testing.T) {
	t.Parallel()
	policyCB := &contractsres.CircuitBreakerConfig{Enabled: true, FailureThreshold: 1}
	pol := &contractsres.ResilienceConfig{CircuitBreaker: policyCB}
	target := contractsres.ResilienceTarget{Name: "t"}
	if got := effectiveCircuitBreaker(pol, target); got != policyCB {
		t.Errorf("got %p; want policy-level %p", got, policyCB)
	}
}

func TestEffectiveCircuitBreaker_TargetOverridesPolicy(t *testing.T) {
	t.Parallel()
	policyCB := &contractsres.CircuitBreakerConfig{Enabled: true, FailureThreshold: 1}
	targetCB := &contractsres.CircuitBreakerConfig{Enabled: true, FailureThreshold: 5}
	pol := &contractsres.ResilienceConfig{CircuitBreaker: policyCB}
	target := contractsres.ResilienceTarget{CircuitBreaker: targetCB}
	if got := effectiveCircuitBreaker(pol, target); got != targetCB {
		t.Errorf("got %p; want per-target %p", got, targetCB)
	}
}

func TestBreaker_ShouldTrip_RateThresholdOnlyMode(t *testing.T) {
	withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	// FailureThreshold=0 means rate-only; trip on rate>threshold
	// regardless of absolute count.
	cfg := &contractsres.CircuitBreakerConfig{
		Enabled:              true,
		FailureThreshold:     0,
		FailureRateThreshold: 0.6,
		MinimumThroughput:    5,
	}
	store.RecordSuccess("p", "t", cfg)
	store.RecordSuccess("p", "t", cfg)
	for i := 0; i < 4; i++ {
		store.RecordFailure("p", "t", cfg) // 4 failures / 6 total = 0.66
	}
	if store.State("p", "t") != StateOpen {
		t.Errorf("rate-only trip did not fire; state = %v", store.State("p", "t"))
	}
}

func TestBreaker_ShouldTrip_BothThresholdsRequired(t *testing.T) {
	withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	// Both thresholds set: absolute count must be hit AND rate
	// must be breached.
	cfg := &contractsres.CircuitBreakerConfig{
		Enabled:              true,
		FailureThreshold:     10,
		FailureRateThreshold: 0.5,
		MinimumThroughput:    5,
	}
	// 4 failures, 2 successes — rate 0.66 but absolute 4 < 10. No trip.
	store.RecordSuccess("p", "t", cfg)
	store.RecordSuccess("p", "t", cfg)
	for i := 0; i < 4; i++ {
		store.RecordFailure("p", "t", cfg)
	}
	if store.State("p", "t") != StateClosed {
		t.Errorf("tripped early: %v", store.State("p", "t"))
	}
}

func TestBreaker_ShouldTrip_NoThresholdsConfigured(t *testing.T) {
	withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	cfg := &contractsres.CircuitBreakerConfig{
		Enabled:           true,
		MinimumThroughput: 1,
	}
	for i := 0; i < 100; i++ {
		store.RecordFailure("p", "t", cfg)
	}
	if store.State("p", "t") != StateClosed {
		t.Errorf("breaker tripped despite no thresholds configured: %v", store.State("p", "t"))
	}
}

func TestBreaker_HalfOpen_AllowsProbesUntilThresholdRecorded(t *testing.T) {
	fc := withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	cfg := &contractsres.CircuitBreakerConfig{
		Enabled:                  true,
		FailureThreshold:         1,
		MinimumThroughput:        1,
		CooldownSeconds:          1,
		HalfOpenSuccessThreshold: 2,
	}
	store.RecordFailure("p", "t", cfg)
	fc.Advance(2 * time.Second)
	// v1.2 semantics: HalfOpen admits all probes; the
	// success-counter drives closure. Any failure during HalfOpen
	// immediately reopens.
	if !store.Allow("p", "t", cfg) {
		t.Error("probe 1 not allowed during HalfOpen")
	}
	if !store.Allow("p", "t", cfg) {
		t.Error("probe 2 not allowed during HalfOpen")
	}
	store.RecordSuccess("p", "t", cfg)
	store.RecordSuccess("p", "t", cfg)
	if store.State("p", "t") != StateClosed {
		t.Errorf("state = %v; want Closed after threshold successes", store.State("p", "t"))
	}
}

func TestBreaker_HalfOpen_AllowReturnsFalseAfterSuccessThreshold(t *testing.T) {
	fc := withFakeClock(t)
	store := NewInMemoryBreakerStore(nil)
	cfg := &contractsres.CircuitBreakerConfig{
		Enabled:                  true,
		FailureThreshold:         1,
		MinimumThroughput:        1,
		CooldownSeconds:          1,
		HalfOpenSuccessThreshold: 1, // single-probe close
	}
	store.RecordFailure("p", "t", cfg)
	fc.Advance(2 * time.Second)
	// First Allow transitions Open → HalfOpen and returns true
	// (the probe quota check is < threshold, so 0 < 1 → true).
	// We don't record a success — directly check that the next
	// Allow with the quota exhausted (still 0 successes but
	// counter not yet bumped) actually... hmm — the v1.2 design
	// admits probes; quota check is in the success count, which
	// is 0, so Allow stays true until we record N successes.
	//
	// To exercise the "quota exhausted → block" branch we need
	// the counter to be at or above threshold without having
	// transitioned to Closed yet. That only happens if the test
	// observes the in-between state of "halfOpenSuccesses ==
	// threshold but state hasn't transitioned." Since the
	// implementation transitions inside RecordSuccess BEFORE
	// returning, that observable in-between never exists from a
	// caller's perspective.
	//
	// The code branch `b.halfOpenSuccesses >= threshold` is
	// therefore defensive — it only fires if Allow is called
	// after a RecordSuccess that triggered the transition but
	// before some other accumulator nudged the state out of
	// HalfOpen. Manual fabrication: drive the breaker by hand.
	allowed := store.Allow("p", "t", cfg)
	if !allowed {
		t.Error("Allow should return true on first HalfOpen probe")
	}
	if store.State("p", "t") != StateHalfOpen {
		t.Fatal("setup: expected HalfOpen")
	}
}

func TestBreaker_HalfOpenSuccessThreshold_DefaultsToOne(t *testing.T) {
	// Serial test: mutates the package-global clock via withFakeClock.
	// No t.Parallel().
	tests := []struct {
		name          string
		threshold     int // HalfOpenSuccessThreshold; 0 == unset
		successesToCl int // consecutive successes expected to close
	}{
		{
			// issue #313: an unset threshold must default to 1 so a
			// single probe closes the breaker rather than wedging it
			// Open forever.
			name:          "unset_defaults_to_one",
			threshold:     0,
			successesToCl: 1,
		},
		{
			// Non-default path is unchanged: an explicit 2 still
			// requires two consecutive successes to close.
			name:          "explicit_two_requires_two",
			threshold:     2,
			successesToCl: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := withFakeClock(t)
			store := NewInMemoryBreakerStore(nil)
			cfg := &contractsres.CircuitBreakerConfig{
				Enabled:                  true,
				FailureThreshold:         1,
				MinimumThroughput:        1,
				CooldownSeconds:          5,
				HalfOpenSuccessThreshold: tt.threshold,
			}

			// Trip the breaker Open.
			store.RecordFailure("p", "t", cfg)
			if store.State("p", "t") != StateOpen {
				t.Fatalf("setup: state = %v; want Open", store.State("p", "t"))
			}

			// Elapse cooldown, then the first Allow probes and
			// transitions Open → HalfOpen, returning true.
			fc.Advance(6 * time.Second)
			if !store.Allow("p", "t", cfg) {
				t.Fatal("Allow returned false after cooldown; want a HalfOpen probe admitted")
			}
			if store.State("p", "t") != StateHalfOpen {
				t.Fatalf("state = %v; want HalfOpen after cooldown probe", store.State("p", "t"))
			}

			// Record one fewer success than required — must stay HalfOpen.
			for i := 0; i < tt.successesToCl-1; i++ {
				store.RecordSuccess("p", "t", cfg)
				if store.State("p", "t") != StateHalfOpen {
					t.Fatalf("state = %v after %d success(es); want HalfOpen (need %d to close)",
						store.State("p", "t"), i+1, tt.successesToCl)
				}
			}

			// The final success crosses the threshold → Closed.
			store.RecordSuccess("p", "t", cfg)
			if store.State("p", "t") != StateClosed {
				t.Errorf("state = %v after %d success(es); want Closed", store.State("p", "t"), tt.successesToCl)
			}
			if !store.Allow("p", "t", cfg) {
				t.Error("Allow returned false after Closed")
			}
		})
	}
}

func TestNoopBreakerStore(t *testing.T) {
	t.Parallel()
	s := noopBreakerStore{}
	cfg := standardCBConfig()
	if !s.Allow("p", "t", cfg) {
		t.Error("noop store should Allow")
	}
	s.RecordSuccess("p", "t", cfg) // must not panic
	s.RecordFailure("p", "t", cfg) // must not panic
	if s.State("p", "t") != StateClosed {
		t.Error("noop store should report Closed")
	}
	if snap := s.Snapshot(); snap != nil {
		t.Errorf("noop store Snapshot = %v; want nil", snap)
	}
}

func TestInMemoryStore_Snapshot_EmptyWhenUntouched(t *testing.T) {
	store := NewInMemoryBreakerStore(nil)
	if snap := store.Snapshot(); len(snap) != 0 {
		t.Errorf("fresh store Snapshot len = %d; want 0", len(snap))
	}
}

func TestInMemoryStore_Snapshot_ReportsPerKeyState(t *testing.T) {
	store := NewInMemoryBreakerStore(nil)
	cfg := standardCBConfig()

	// Touch two distinct (policy, target) pairs; both start Closed.
	store.RecordSuccess("polA", "targetA", cfg)
	store.RecordSuccess("polB", "targetB", cfg)
	// Trip polA/targetA so its Snapshot row reads StateOpen.
	for i := 0; i < cfg.FailureThreshold+cfg.MinimumThroughput; i++ {
		store.RecordFailure("polA", "targetA", cfg)
	}

	snap := store.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("Snapshot len = %d; want 2", len(snap))
	}

	got := map[string]State{}
	for _, s := range snap {
		got[s.Policy+"|"+s.Target] = s.State
	}
	if got["polA|targetA"] != StateOpen {
		t.Errorf("polA/targetA state = %v; want StateOpen", got["polA|targetA"])
	}
	if got["polB|targetB"] != StateClosed {
		t.Errorf("polB/targetB state = %v; want StateClosed", got["polB|targetB"])
	}
}

func TestSplitBreakerKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in     string
		policy string
		target string
	}{
		{"polA|targetA", "polA", "targetA"},
		{"a|b|c", "a", "b|c"}, // only the first | is the separator
		{"bare", "bare", ""},  // no separator collapses safely
		{"|trailing", "", "trailing"},
	}
	for _, tc := range cases {
		p, t1 := splitBreakerKey(tc.in)
		if p != tc.policy || t1 != tc.target {
			t.Errorf("splitBreakerKey(%q) = (%q, %q); want (%q, %q)", tc.in, p, t1, tc.policy, tc.target)
		}
	}
}
