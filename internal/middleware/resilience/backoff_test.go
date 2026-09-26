package resilience

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/proxy"
)

// Tests in this file must not call t.Parallel(): they stub the package-global
// sleepWithContext and randomIntN hooks, the same serial discipline the clock
// stub follows. No real sleeping happens — the stub records the requested
// delays and returns immediately.

// fakeSleeper replaces sleepWithContext, capturing every requested delay.
// cancelAfter, when > 0, cancels the supplied cancel func on the nth call so a
// test can simulate the client disconnecting mid-backoff.
type fakeSleeper struct {
	delays      []time.Duration
	cancelAfter int
	cancel      context.CancelFunc
}

func (f *fakeSleeper) sleep(ctx context.Context, d time.Duration) error {
	f.delays = append(f.delays, d)
	if f.cancelAfter > 0 && len(f.delays) == f.cancelAfter && f.cancel != nil {
		f.cancel()
		return ctx.Err()
	}
	return nil
}

func withFakeSleeper(t *testing.T) *fakeSleeper {
	t.Helper()
	fs := &fakeSleeper{}
	prev := sleepWithContext
	sleepWithContext = fs.sleep
	t.Cleanup(func() { sleepWithContext = prev })
	return fs
}

func withFixedRandom(t *testing.T, value int) {
	t.Helper()
	prev := randomIntN
	randomIntN = func(int) int { return value }
	t.Cleanup(func() { randomIntN = prev })
}

func enabledRetry(bt contractsres.BackoffType, delayMs, maxDelayMs, maxAttempts int) *contractsres.RetryConfig {
	return &contractsres.RetryConfig{
		Enabled:           true,
		BackoffType:       bt,
		DelayMilliseconds: delayMs,
		MaxDelayMs:        maxDelayMs,
		MaxAttempts:       maxAttempts,
	}
}

func TestRetryDelay_Curves(t *testing.T) {
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	cases := []struct {
		name      string
		retry     *contractsres.RetryConfig
		completed int
		want      time.Duration
	}{
		{"nil retry", nil, 1, 0},
		{"disabled retry", &contractsres.RetryConfig{Enabled: false, DelayMilliseconds: 100}, 1, 0},
		{"zero base delay", enabledRetry(contractsres.BackoffConstant, 0, 0, 0), 1, 0},
		{"nothing completed yet", enabledRetry(contractsres.BackoffConstant, 100, 0, 0), 0, 0},
		{"constant n=1", enabledRetry(contractsres.BackoffConstant, 100, 0, 0), 1, ms(100)},
		{"constant n=4", enabledRetry(contractsres.BackoffConstant, 100, 0, 0), 4, ms(100)},
		{"unset type is constant", enabledRetry("", 100, 0, 0), 3, ms(100)},
		{"linear n=1", enabledRetry(contractsres.BackoffLinear, 100, 0, 0), 1, ms(100)},
		{"linear n=3", enabledRetry(contractsres.BackoffLinear, 100, 0, 0), 3, ms(300)},
		{"exponential n=1", enabledRetry(contractsres.BackoffExponential, 100, 0, 0), 1, ms(100)},
		{"exponential n=2", enabledRetry(contractsres.BackoffExponential, 100, 0, 0), 2, ms(200)},
		{"exponential n=4", enabledRetry(contractsres.BackoffExponential, 100, 0, 0), 4, ms(800)},
		{"exponential capped", enabledRetry(contractsres.BackoffExponential, 100, 250, 0), 4, ms(250)},
		{"linear capped", enabledRetry(contractsres.BackoffLinear, 100, 150, 0), 5, ms(150)},
		{"constant under cap untouched", enabledRetry(contractsres.BackoffConstant, 100, 500, 0), 9, ms(100)},
		{"exponential huge n does not overflow", enabledRetry(contractsres.BackoffExponential, 1000, 0, 0), 500, time.Duration(1<<62) + (time.Duration(1<<62) - 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := retryDelay(tc.retry, tc.completed)
			if tc.name == "exponential huge n does not overflow" {
				// The exact clamp value is an implementation detail; the
				// property is "positive and not wrapped negative".
				if got <= 0 {
					t.Fatalf("retryDelay overflowed to %d", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("retryDelay(%+v, %d) = %s; want %s", tc.retry, tc.completed, got, tc.want)
			}
		})
	}
}

func TestRetryDelay_Jitter_DrawsFromUpperHalf(t *testing.T) {
	retry := enabledRetry(contractsres.BackoffConstant, 200, 0, 0)
	retry.UseJitter = true

	withFixedRandom(t, 0)
	if got := retryDelay(retry, 1); got != 100*time.Millisecond {
		t.Errorf("jitter floor = %s; want delay/2 = 100ms", got)
	}

	// randomIntN(half+1) may return half itself, landing on the full delay.
	withFixedRandom(t, int(100*time.Millisecond))
	if got := retryDelay(retry, 1); got != 200*time.Millisecond {
		t.Errorf("jitter ceiling = %s; want the full 200ms", got)
	}
}

func TestRetryBudgetExhausted(t *testing.T) {
	cases := []struct {
		name      string
		retry     *contractsres.RetryConfig
		completed int
		want      bool
	}{
		{"nil never exhausts", nil, 99, false},
		{"disabled never exhausts", &contractsres.RetryConfig{MaxAttempts: 1}, 5, false},
		{"zero max never exhausts", enabledRetry("", 0, 0, 0), 5, false},
		{"under budget", enabledRetry("", 0, 0, 3), 2, false},
		{"at budget", enabledRetry("", 0, 0, 3), 3, true},
		{"over budget", enabledRetry("", 0, 0, 3), 4, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryBudgetExhausted(tc.retry, tc.completed); got != tc.want {
				t.Errorf("retryBudgetExhausted = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestSleepWithContext_Default(t *testing.T) {
	// The real hook: a zero delay returns nil without consulting ctx, a live
	// ctx sleeps the (tiny) duration, and a cancelled ctx returns its error
	// rather than waiting out a long delay.
	if err := sleepWithContext(context.Background(), 0); err != nil {
		t.Errorf("zero delay err = %v; want nil", err)
	}
	if err := sleepWithContext(context.Background(), time.Millisecond); err != nil {
		t.Errorf("1ms sleep err = %v; want nil", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err := sleepWithContext(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled sleep err = %v; want context.Canceled", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("cancelled sleep took %s; must return promptly", time.Since(start))
	}
}

// queuedNext is the minimal downstream for the orchestrator-level backoff
// tests: each attempt pops the next canned status (0 = transport error).
type queuedNext struct {
	statuses []int
	seen     int
}

func (q *queuedNext) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	idx := q.seen
	q.seen++
	if idx >= len(q.statuses) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if q.statuses[idx] == 0 {
		if buf, ok := w.(*proxy.BufferingResponseWriter); ok {
			buf.SetTransportError(errors.New("connection refused"))
		}
		return
	}
	w.WriteHeader(q.statuses[idx])
}

func switchTo(p string) []contractsrules.Action {
	return []contractsrules.Action{&contractsrules.ChangeProviderAction{NewProvider: p}}
}

func threeTargetPolicy(mode contractsres.ResilienceMode, retry *contractsres.RetryConfig) *contractsres.ResilienceConfig {
	return &contractsres.ResilienceConfig{
		Name:  "paced",
		Mode:  mode,
		Retry: retry,
		Targets: []contractsres.ResilienceTarget{
			{Name: "a", Provider: "a", Order: 1, Weight: 1, Actions: switchTo("a")},
			{Name: "b", Provider: "b", Order: 2, Weight: 1, Actions: switchTo("b")},
			{Name: "c", Provider: "c", Order: 3, Weight: 1, Actions: switchTo("c")},
		},
	}
}

func serveWithPolicy(pol *contractsres.ResilienceConfig, next http.Handler, ctx context.Context) *httptest.ResponseRecorder {
	h := HTTPHandler(nil, nil, nil, next)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx = WithResilienceConfig(rules.WithMutableState(ctx, &rules.MutableState{}), pol)
	h.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestFailover_RetryBackoff_SleepsBetweenAttemptsOnly(t *testing.T) {
	fs := withFakeSleeper(t)
	pol := threeTargetPolicy(contractsres.ModeFailover, enabledRetry(contractsres.BackoffLinear, 100, 0, 0))
	next := &queuedNext{statuses: []int{503, 0, 200}}

	rec := serveWithPolicy(pol, next, context.Background())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 from the third target", rec.Code)
	}
	if next.seen != 3 {
		t.Errorf("attempts = %d; want 3", next.seen)
	}
	// No sleep before the first attempt; linear 100ms × completed before the
	// second and third.
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	if len(fs.delays) != len(want) {
		t.Fatalf("sleeps = %v; want %v", fs.delays, want)
	}
	for i := range want {
		if fs.delays[i] != want[i] {
			t.Errorf("sleep[%d] = %s; want %s", i, fs.delays[i], want[i])
		}
	}
}

func TestFailover_RetryDisabled_NoSleep(t *testing.T) {
	fs := withFakeSleeper(t)
	pol := threeTargetPolicy(contractsres.ModeFailover, &contractsres.RetryConfig{Enabled: false, DelayMilliseconds: 100, MaxAttempts: 1})
	next := &queuedNext{statuses: []int{503, 503, 200}}

	rec := serveWithPolicy(pol, next, context.Background())

	if rec.Code != http.StatusOK || next.seen != 3 {
		t.Fatalf("status/attempts = %d/%d; disabled retry must neither pace nor cap", rec.Code, next.seen)
	}
	if len(fs.delays) != 0 {
		t.Errorf("sleeps = %v; want none", fs.delays)
	}
}

func TestFailover_NilRetry_NoSleep(t *testing.T) {
	fs := withFakeSleeper(t)
	pol := threeTargetPolicy(contractsres.ModeFailover, nil)
	next := &queuedNext{statuses: []int{503, 200}}

	rec := serveWithPolicy(pol, next, context.Background())

	if rec.Code != http.StatusOK || next.seen != 2 {
		t.Fatalf("status/attempts = %d/%d", rec.Code, next.seen)
	}
	if len(fs.delays) != 0 {
		t.Errorf("sleeps = %v; nil retry must be the historical no-delay walk", fs.delays)
	}
}

func TestFailover_MaxAttempts_StopsWalkWithTargetsRemaining(t *testing.T) {
	fs := withFakeSleeper(t)
	pol := threeTargetPolicy(contractsres.ModeFailover, enabledRetry(contractsres.BackoffConstant, 50, 0, 2))
	next := &queuedNext{statuses: []int{503, 502, 200}}

	rec := serveWithPolicy(pol, next, context.Background())

	if next.seen != 2 {
		t.Errorf("attempts = %d; want 2 (max_attempts budget)", next.seen)
	}
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d; want the last attempt's 502 via the exhausted path", rec.Code)
	}
	if len(fs.delays) != 1 {
		t.Errorf("sleeps = %v; want exactly one (before attempt 2)", fs.delays)
	}
}

func TestFailover_MaxAttemptsOne_NoFailover(t *testing.T) {
	withFakeSleeper(t)
	pol := threeTargetPolicy(contractsres.ModeFailover, enabledRetry(contractsres.BackoffConstant, 50, 0, 1))
	next := &queuedNext{statuses: []int{503, 200}}

	rec := serveWithPolicy(pol, next, context.Background())

	if next.seen != 1 || rec.Code != http.StatusServiceUnavailable {
		t.Errorf("attempts/status = %d/%d; max_attempts=1 means the first outcome is final", next.seen, rec.Code)
	}
}

func TestFailover_ClientCancelDuringBackoff_EndsWalk(t *testing.T) {
	fs := withFakeSleeper(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fs.cancelAfter = 1
	fs.cancel = cancel
	pol := threeTargetPolicy(contractsres.ModeFailover, enabledRetry(contractsres.BackoffConstant, 50, 0, 0))
	next := &queuedNext{statuses: []int{503, 200, 200}}

	rec := serveWithPolicy(pol, next, ctx)

	if next.seen != 1 {
		t.Errorf("attempts = %d; want 1 — a client that left mid-backoff must not trigger more upstream calls", next.seen)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want the last attempt's 503 via the exhausted path", rec.Code)
	}
}

func TestFailover_CBBlockedTarget_SkippedWithoutSleep(t *testing.T) {
	fs := withFakeSleeper(t)
	pol := threeTargetPolicy(contractsres.ModeFailover, enabledRetry(contractsres.BackoffConstant, 50, 0, 0))
	blockB := blockingStore{blocked: "b"}
	next := &queuedNext{statuses: []int{503, 200}}

	h := HTTPHandler(nil, blockB, nil, next)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := WithResilienceConfig(rules.WithMutableState(req.Context(), &rules.MutableState{}), pol)
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK || next.seen != 2 {
		t.Fatalf("status/attempts = %d/%d; want 200 after a→(skip b)→c", rec.Code, next.seen)
	}
	// One sleep: before c. The CB-blocked b never counted as an attempt and
	// never paid a delay.
	if len(fs.delays) != 1 {
		t.Errorf("sleeps = %v; want exactly one", fs.delays)
	}
}

// blockingStore is a BreakerStore whose Allow rejects one named target and
// admits everything else; Record* are no-ops.
type blockingStore struct{ blocked string }

func (b blockingStore) Allow(_, target string, _ *contractsres.CircuitBreakerConfig) bool {
	return target != b.blocked
}
func (blockingStore) RecordSuccess(string, string, *contractsres.CircuitBreakerConfig) {}
func (blockingStore) RecordFailure(string, string, *contractsres.CircuitBreakerConfig) {}
func (blockingStore) State(string, string) State                                       { return StateClosed }
func (blockingStore) Snapshot() []BreakerSnapshot                                      { return nil }

func TestLoadBalance_RetryBackoff_SleepsBeforeReroll(t *testing.T) {
	fs := withFakeSleeper(t)
	pol := threeTargetPolicy(contractsres.ModeLoadBalance, enabledRetry(contractsres.BackoffExponential, 100, 0, 0))
	next := &queuedNext{statuses: []int{0, 0, 200}}

	rec := serveWithPolicy(pol, next, context.Background())

	if rec.Code != http.StatusOK || next.seen != 3 {
		t.Fatalf("status/attempts = %d/%d; want 200 on the third roll", rec.Code, next.seen)
	}
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	if len(fs.delays) != 2 || fs.delays[0] != want[0] || fs.delays[1] != want[1] {
		t.Errorf("sleeps = %v; want %v", fs.delays, want)
	}
}

func TestLoadBalance_MaxAttempts_CapsRerolls(t *testing.T) {
	withFakeSleeper(t)
	pol := threeTargetPolicy(contractsres.ModeLoadBalance, enabledRetry(contractsres.BackoffConstant, 10, 0, 2))
	next := &queuedNext{statuses: []int{503, 503, 200}}

	rec := serveWithPolicy(pol, next, context.Background())

	if next.seen != 2 {
		t.Errorf("attempts = %d; want 2 (budget)", next.seen)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 from the exhausted path", rec.Code)
	}
}

func TestLoadBalance_ClientCancelDuringBackoff_EndsWalk(t *testing.T) {
	fs := withFakeSleeper(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fs.cancelAfter = 1
	fs.cancel = cancel
	pol := threeTargetPolicy(contractsres.ModeLoadBalance, enabledRetry(contractsres.BackoffConstant, 10, 0, 0))
	next := &queuedNext{statuses: []int{503, 200, 200}}

	_ = serveWithPolicy(pol, next, ctx)

	if next.seen != 1 {
		t.Errorf("attempts = %d; want 1", next.seen)
	}
}

func TestInMemoryStore_GetOrCreate_ResizesWindowOnConfigChange(t *testing.T) {
	fc := withFakeClock(t)
	store := NewInMemoryBreakerStore(nil).(*inMemoryStore)

	cfg := &contractsres.CircuitBreakerConfig{Enabled: true, FailureThreshold: 3, SamplingDurationSeconds: 60, CooldownSeconds: 30}
	store.RecordFailure("pol", "tgt", cfg)
	store.RecordFailure("pol", "tgt", cfg)

	b := store.getOrCreate("pol", "tgt", cfg)
	if len(b.buckets) != 60 {
		t.Fatalf("initial ring = %d buckets; want 60", len(b.buckets))
	}

	// Operator widens the window live: the same key must now evaluate over
	// 300 buckets. In-window history is discarded on resize (documented), so
	// the two earlier failures no longer count toward the threshold.
	fc.Advance(time.Second)
	wider := &contractsres.CircuitBreakerConfig{Enabled: true, FailureThreshold: 3, SamplingDurationSeconds: 300, CooldownSeconds: 30}
	b2 := store.getOrCreate("pol", "tgt", wider)
	if b2 != b {
		t.Fatalf("resize must rebuild the ring in place, not replace the breaker")
	}
	if len(b.buckets) != 300 {
		t.Fatalf("ring after resize = %d buckets; want 300", len(b.buckets))
	}
	if b.bucketIdx != 0 || !b.lastRotate.Equal(fc.Now()) {
		t.Errorf("resize must reset bucketIdx (%d) and lastRotate (%s vs %s)", b.bucketIdx, b.lastRotate, fc.Now())
	}
	store.RecordFailure("pol", "tgt", wider)
	store.RecordFailure("pol", "tgt", wider)
	if store.State("pol", "tgt") != StateClosed {
		t.Errorf("two failures after resize tripped the breaker; pre-resize history should have been dropped")
	}
	store.RecordFailure("pol", "tgt", wider)
	if store.State("pol", "tgt") != StateOpen {
		t.Errorf("third failure in the new window should trip (threshold 3)")
	}

	// Shrinking keeps the lifecycle state: Open survives, only the ring
	// changes.
	narrower := &contractsres.CircuitBreakerConfig{Enabled: true, FailureThreshold: 3, SamplingDurationSeconds: 10, CooldownSeconds: 30}
	if store.Allow("pol", "tgt", narrower) {
		t.Errorf("Open breaker must stay Open across a window resize")
	}
	if got := len(store.getOrCreate("pol", "tgt", narrower).buckets); got != 10 {
		t.Errorf("ring after shrink = %d; want 10", got)
	}
}

func TestInMemoryStore_GetOrCreate_SameWindowKeepsHistory(t *testing.T) {
	withFakeClock(t)
	store := NewInMemoryBreakerStore(nil).(*inMemoryStore)
	cfg := &contractsres.CircuitBreakerConfig{Enabled: true, FailureThreshold: 3, SamplingDurationSeconds: 60, CooldownSeconds: 30}
	store.RecordFailure("pol", "tgt", cfg)
	store.RecordFailure("pol", "tgt", cfg)

	// A config with the same window but different thresholds is not a
	// resize; the counted failures must survive.
	same := &contractsres.CircuitBreakerConfig{Enabled: true, FailureThreshold: 5, SamplingDurationSeconds: 60, CooldownSeconds: 30}
	store.RecordFailure("pol", "tgt", same)
	var failures int
	for _, bk := range store.getOrCreate("pol", "tgt", same).buckets {
		failures += bk.failures
	}
	if failures != 3 {
		t.Errorf("failures in window = %d; want 3 (no resize, no reset)", failures)
	}
}

func TestEffectiveWindow(t *testing.T) {
	if got := effectiveWindow(&contractsres.CircuitBreakerConfig{}); got != 60 {
		t.Errorf("unset window = %d; want 60", got)
	}
	if got := effectiveWindow(&contractsres.CircuitBreakerConfig{SamplingDurationSeconds: -4}); got != 60 {
		t.Errorf("negative window = %d; want 60", got)
	}
	if got := effectiveWindow(&contractsres.CircuitBreakerConfig{SamplingDurationSeconds: 7}); got != 7 {
		t.Errorf("window = %d; want 7", got)
	}
}

func TestEffectiveAttemptTimeout(t *testing.T) {
	pol := &contractsres.ResilienceConfig{TimeoutSeconds: 5}
	if got := effectiveAttemptTimeout(pol, contractsres.ResilienceTarget{}); got != 5*time.Second {
		t.Errorf("policy fallback = %s; want 5s", got)
	}
	if got := effectiveAttemptTimeout(pol, contractsres.ResilienceTarget{TimeoutSeconds: 2}); got != 2*time.Second {
		t.Errorf("target override = %s; want 2s", got)
	}
	if got := effectiveAttemptTimeout(&contractsres.ResilienceConfig{}, contractsres.ResilienceTarget{}); got != 0 {
		t.Errorf("neither set = %s; want 0", got)
	}
}

func TestMarkAttemptTimeout(t *testing.T) {
	parent := context.Background()
	expired, cancel := context.WithTimeout(parent, time.Nanosecond)
	defer cancel()
	<-expired.Done()

	// Zero timeout: never marks, even with an expired attempt ctx.
	buf := proxy.NewBufferingResponseWriter(httptest.NewRecorder(), nil)
	markAttemptTimeout(parent, expired, buf, 0)
	if buf.TransportError() != nil {
		t.Errorf("zero timeout must not mark")
	}

	// Expired attempt ctx, live parent, nothing recorded: marks.
	buf = proxy.NewBufferingResponseWriter(httptest.NewRecorder(), nil)
	markAttemptTimeout(parent, expired, buf, time.Second)
	if err := buf.TransportError(); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded-wrapping transport error, got %v", err)
	}

	// Already-recorded transport error is left alone.
	buf = proxy.NewBufferingResponseWriter(httptest.NewRecorder(), nil)
	sentinel := errors.New("upstream said no")
	buf.SetTransportError(sentinel)
	markAttemptTimeout(parent, expired, buf, time.Second)
	if !errors.Is(buf.TransportError(), sentinel) {
		t.Errorf("existing transport error overwritten: %v", buf.TransportError())
	}

	// Committed buffer: nothing to retry, no mark.
	buf = proxy.NewBufferingResponseWriter(httptest.NewRecorder(), nil)
	buf.WriteHeader(http.StatusOK)
	markAttemptTimeout(parent, expired, buf, time.Second)
	if buf.TransportError() != nil {
		t.Errorf("committed attempt must not be marked")
	}

	// Parent cancelled too: the client left; no mark.
	gone, cancelParent := context.WithCancel(parent)
	cancelParent()
	buf = proxy.NewBufferingResponseWriter(httptest.NewRecorder(), nil)
	markAttemptTimeout(gone, gone, buf, time.Second)
	if buf.TransportError() != nil {
		t.Errorf("departed client must not be recorded as an attempt timeout")
	}
}
