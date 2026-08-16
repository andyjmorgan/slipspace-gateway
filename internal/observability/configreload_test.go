package observability

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// countingCounter records Add calls so the test can assert on the count
// without standing up a full meter provider.
type countingCounter struct {
	metric.Int64Counter
	mu sync.Mutex
	n  int64
}

func (c *countingCounter) Add(_ context.Context, v int64, _ ...metric.AddOption) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n += v
}

func (c *countingCounter) count() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func newCountingCounter() *countingCounter {
	return &countingCounter{Int64Counter: noop.Int64Counter{}}
}

func TestConfigReloadCounter_SwallowsRegistrationCall(t *testing.T) {
	t.Parallel()
	c := newCountingCounter()
	onReload := ConfigReloadCounter(context.Background(), c)

	// The immediate call Subscribe makes at registration is not a reload.
	onReload()
	if got := c.count(); got != 0 {
		t.Fatalf("after registration call: count = %d, want 0", got)
	}

	onReload()
	if got := c.count(); got != 1 {
		t.Fatalf("after first real replace: count = %d, want 1", got)
	}

	onReload()
	onReload()
	if got := c.count(); got != 3 {
		t.Fatalf("after three real replaces: count = %d, want 3", got)
	}
}

func TestConfigReloadCounter_NilCounterIsNoOp(t *testing.T) {
	t.Parallel()
	onReload := ConfigReloadCounter(context.Background(), nil)
	// Must not panic with or without the registration call consumed.
	onReload()
	onReload()
}

func TestConfigReloadCounter_ConcurrentReplacesCountOnce(t *testing.T) {
	t.Parallel()
	c := newCountingCounter()
	onReload := ConfigReloadCounter(context.Background(), c)

	// Exactly one of these racing calls is swallowed as the registration
	// artefact; the rest must each count. Guards the atomic against a
	// plain-bool implementation that would drop or double-count under -race.
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			onReload()
		}()
	}
	wg.Wait()

	if got := c.count(); got != n-1 {
		t.Fatalf("count = %d, want %d (one call swallowed at registration)", got, n-1)
	}
}
