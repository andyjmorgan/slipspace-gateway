package agentroute

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock is a test-controlled clock, installed on Register.now (same-package
// access; the field is unexported by design).
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

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

func newTestRegister() (*Register, *fakeClock) {
	r := NewRegister()
	fc := newFakeClock()
	r.now = fc.Now
	return r, fc
}

const testTTL = time.Hour

func TestRegister_Observe(t *testing.T) {
	t.Run("first observe fires, second does not", func(t *testing.T) {
		r, _ := newTestRegister()

		pin, fire := r.Observe("conv-1", testTTL)
		if pin != nil || !fire {
			t.Fatalf("first Observe = (%v, %v), want (nil, true)", pin, fire)
		}
		pin, fire = r.Observe("conv-1", testTTL)
		if pin != nil || fire {
			t.Fatalf("second Observe = (%v, %v), want (nil, false)", pin, fire)
		}
	})

	t.Run("distinct keys fire independently", func(t *testing.T) {
		r, _ := newTestRegister()

		if _, fire := r.Observe("conv-a", testTTL); !fire {
			t.Fatal("conv-a first Observe should fire")
		}
		if _, fire := r.Observe("conv-b", testTTL); !fire {
			t.Fatal("conv-b first Observe should fire")
		}
	})
}

func TestRegister_Complete(t *testing.T) {
	t.Run("pin within window is applied", func(t *testing.T) {
		r, _ := newTestRegister()

		r.Observe("conv-1", testTTL) // seen = 1
		r.Complete("conv-1", &Pin{Model: "cheap-model-a", Reason: "trivial"}, 3)

		pin, fire := r.Observe("conv-1", testTTL)
		if pin == nil {
			t.Fatal("Observe after Complete returned nil pin, want applied pin")
		}
		if fire {
			t.Fatal("Observe after Complete should not fire")
		}
		if pin.Model != "cheap-model-a" || pin.Reason != "trivial" {
			t.Fatalf("pin = %+v, want {cheap-model-a trivial}", pin)
		}
	})

	t.Run("pin after window exceeded is discarded", func(t *testing.T) {
		r, _ := newTestRegister()

		window := 3
		for i := 0; i < window+1; i++ { // seen = 4 > window
			r.Observe("conv-1", testTTL)
		}
		r.Complete("conv-1", &Pin{Model: "cheap-model-a"}, window)

		pin, fire := r.Observe("conv-1", testTTL)
		if pin != nil {
			t.Fatalf("late verdict was applied: pin = %+v, want nil", pin)
		}
		if fire {
			t.Fatal("Observe after late Complete should not fire (decided)")
		}
	})

	t.Run("nil pin decides the conversation", func(t *testing.T) {
		r, _ := newTestRegister()

		r.Observe("conv-1", testTTL)
		r.Complete("conv-1", nil, 3)

		for i := 0; i < 3; i++ {
			pin, fire := r.Observe("conv-1", testTTL)
			if pin != nil || fire {
				t.Fatalf("Observe %d after continue-verdict = (%v, %v), want (nil, false)", i, pin, fire)
			}
		}
	})

	t.Run("complete on unknown key is a no-op", func(t *testing.T) {
		r, _ := newTestRegister()
		r.Complete("never-observed", &Pin{Model: "cheap-model-a"}, 3)

		pin, fire := r.Observe("never-observed", testTTL)
		if pin != nil || !fire {
			t.Fatalf("Observe = (%v, %v), want (nil, true)", pin, fire)
		}
	})
}

func TestRegister_Fail(t *testing.T) {
	t.Run("fail before decided removes the entry so observe fires again", func(t *testing.T) {
		r, _ := newTestRegister()

		r.Observe("conv-1", testTTL)
		r.Fail("conv-1")

		pin, fire := r.Observe("conv-1", testTTL)
		if pin != nil || !fire {
			t.Fatalf("Observe after Fail = (%v, %v), want (nil, true)", pin, fire)
		}
	})

	t.Run("fail after decided keeps the entry", func(t *testing.T) {
		r, _ := newTestRegister()

		r.Observe("conv-1", testTTL)
		r.Complete("conv-1", &Pin{Model: "cheap-model-a"}, 3)
		r.Fail("conv-1")

		pin, fire := r.Observe("conv-1", testTTL)
		if pin == nil || pin.Model != "cheap-model-a" {
			t.Fatalf("pin = %+v, want the decided pin to survive Fail", pin)
		}
		if fire {
			t.Fatal("Observe on a decided entry should not fire")
		}
	})

	t.Run("fail on unknown key is a no-op", func(t *testing.T) {
		r, _ := newTestRegister()
		r.Fail("never-observed") // must not panic
	})
}

func TestRegister_TTLExpiry(t *testing.T) {
	r, fc := newTestRegister()

	r.Observe("conv-1", testTTL)
	r.Complete("conv-1", &Pin{Model: "cheap-model-a"}, 3)

	// Still live just inside the TTL.
	fc.Advance(testTTL - time.Second)
	pin, fire := r.Observe("conv-1", testTTL)
	if pin == nil || fire {
		t.Fatalf("Observe inside TTL = (%v, %v), want (pin, false)", pin, fire)
	}

	// Past the TTL the entry lapses and classification fires again.
	fc.Advance(2 * time.Second)
	pin, fire = r.Observe("conv-1", testTTL)
	if pin != nil || !fire {
		t.Fatalf("Observe past TTL = (%v, %v), want (nil, true)", pin, fire)
	}
}

func TestRegister_ConcurrencySmoke(t *testing.T) {
	r, _ := newTestRegister()

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("conv-%d", i%10)
			for j := 0; j < 50; j++ {
				switch j % 4 {
				case 0, 1:
					r.Observe(key, testTTL)
				case 2:
					r.Complete(key, &Pin{Model: "cheap-model-a"}, 3)
				case 3:
					r.Fail(key)
				}
			}
		}(i)
	}
	wg.Wait()
}
