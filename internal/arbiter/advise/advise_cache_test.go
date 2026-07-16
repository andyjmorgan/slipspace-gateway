package advise

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	contractsadvise "github.com/andyjmorgan/slipspace-gateway/contracts/advise"
)

// fillCache inserts n entries directly into the handler cache (same-package
// access) — 10k judge round-trips would dominate the test run without
// exercising anything new.
func fillCache(h *Handler, n int, expiresFor func(i int) time.Time) {
	for i := 0; i < n; i++ {
		h.cache[fmt.Sprintf("key-fill-%d", i)] = cacheEntry{
			verdict: contractsadvise.Verdict{Reason: "old"},
			expires: expiresFor(i),
		}
	}
}

func TestStore_OverflowSweep(t *testing.T) {
	t.Run("expired entries swept first", func(t *testing.T) {
		h := NewHandler(&stubVerifier{}, &stubJudge{}, time.Minute, discardLogger())
		current := time.Unix(1_700_000_000, 0)
		h.now = func() time.Time { return current }

		// Fill to capacity: even indexes expired, odd indexes live.
		fillCache(h, maxCacheEntries, func(i int) time.Time {
			if i%2 == 0 {
				return current.Add(-time.Second)
			}
			return current.Add(time.Hour)
		})

		want := contractsadvise.Verdict{Switch: true, Model: "cheap-candidate-a", Reason: "trivial"}
		h.store("key-new", want)

		// Dropping the expired half creates ample headroom, so no arbitrary
		// eviction happens: every live entry survives.
		if got, wantLen := len(h.cache), maxCacheEntries/2+1; got != wantLen {
			t.Fatalf("len(cache) = %d, want %d (expired half swept + new entry)", got, wantLen)
		}
		if _, ok := h.cache["key-fill-0"]; ok {
			t.Error("expired key-fill-0 survived the sweep")
		}
		if _, ok := h.cache["key-fill-1"]; !ok {
			t.Error("live key-fill-1 was dropped despite expired headroom")
		}
		if got, ok := h.cached("key-new"); !ok || got != want {
			t.Errorf("cached(key-new) = (%+v, %t), want (%+v, true)", got, ok, want)
		}
	})

	t.Run("all-live cache evicts arbitrarily", func(t *testing.T) {
		h := NewHandler(&stubVerifier{}, &stubJudge{}, time.Minute, discardLogger())
		current := time.Unix(1_700_000_000, 0)
		h.now = func() time.Time { return current }

		fillCache(h, maxCacheEntries, func(int) time.Time { return current.Add(time.Hour) })

		h.store("key-new", contractsadvise.Verdict{Switch: false, Reason: "complex"})

		// Nothing was expired, so an arbitrary entry is evicted to make room
		// and the cache stays at capacity.
		if got := len(h.cache); got != maxCacheEntries {
			t.Fatalf("len(cache) = %d, want %d (at capacity after arbitrary eviction)", got, maxCacheEntries)
		}
		if _, ok := h.cache["key-new"]; !ok {
			t.Fatal("new entry missing after arbitrary eviction")
		}
	})
}

func TestServeHTTP_RequestTooLarge(t *testing.T) {
	j := &stubJudge{}
	h := NewHandler(&stubVerifier{}, j, time.Minute, discardLogger())

	rr := post(t, h, trustedHeaders(), strings.Repeat("a", maxRequestBytes+1))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusRequestEntityTooLarge)
	}
	if j.calls != 0 {
		t.Errorf("judge called %d times, want 0", j.calls)
	}
}
