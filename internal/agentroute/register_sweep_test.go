package agentroute

import (
	"fmt"
	"testing"
	"time"
)

// fillEntries inserts n entries directly into the register map (same-package
// access) — 100k Observe calls would dominate the test run without exercising
// anything new. expired selects which entries lapse before the fake clock.
func fillEntries(r *Register, n int, expiresFor func(i int) time.Time) {
	for i := 0; i < n; i++ {
		r.entries[fmt.Sprintf("conv-fill-%d", i)] = &entry{seen: 1, expires: expiresFor(i)}
	}
}

func TestRegister_SweepOnOverflow(t *testing.T) {
	t.Run("expired entries are dropped first", func(t *testing.T) {
		r, fc := newTestRegister()
		now := fc.Now()

		// Fill to capacity: even indexes expired, odd indexes live.
		fillEntries(r, maxEntries, func(i int) time.Time {
			if i%2 == 0 {
				return now.Add(-time.Minute)
			}
			return now.Add(time.Hour)
		})

		pin, fire := r.Observe("conv-sweep-new", testTTL)
		if pin != nil || !fire {
			t.Fatalf("Observe on full register = (%v, %v), want (nil, true)", pin, fire)
		}

		if _, ok := r.entries["conv-sweep-new"]; !ok {
			t.Fatal("new entry missing after sweep")
		}
		// Dropping the expired half creates ample headroom, so no arbitrary
		// eviction happens: every live entry survives.
		want := maxEntries/2 + 1
		if got := len(r.entries); got != want {
			t.Fatalf("len(entries) = %d, want %d (expired half swept + new entry)", got, want)
		}
		if _, ok := r.entries["conv-fill-0"]; ok {
			t.Error("expired conv-fill-0 survived the sweep")
		}
		if _, ok := r.entries["conv-fill-1"]; !ok {
			t.Error("live conv-fill-1 was dropped despite expired headroom")
		}
	})

	t.Run("all-live register evicts arbitrarily to 1% headroom", func(t *testing.T) {
		r, fc := newTestRegister()
		now := fc.Now()

		fillEntries(r, maxEntries, func(int) time.Time { return now.Add(time.Hour) })

		pin, fire := r.Observe("conv-sweep-new", testTTL)
		if pin != nil || !fire {
			t.Fatalf("Observe on full register = (%v, %v), want (nil, true)", pin, fire)
		}

		if _, ok := r.entries["conv-sweep-new"]; !ok {
			t.Fatal("new entry missing after sweep")
		}
		// Nothing was expired, so the sweep evicts arbitrary entries down to
		// 1% headroom, then Observe adds the new entry.
		want := maxEntries - maxEntries/100 + 1
		if got := len(r.entries); got != want {
			t.Fatalf("len(entries) = %d, want %d (1%% headroom carved + new entry)", got, want)
		}
	})
}
