package livefeed

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestNewRing_RejectsNonPositiveCapacity(t *testing.T) {
	t.Parallel()
	for _, capacity := range []int{0, -1, -100} {
		if _, err := NewRing(capacity); !errors.Is(err, ErrCapacityNonPositive) {
			t.Fatalf("capacity=%d: want ErrCapacityNonPositive, got %v", capacity, err)
		}
	}
}

func TestRing_AppendAndRecent_OrdersOldestFirst(t *testing.T) {
	t.Parallel()
	r, err := NewRing(4)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		r.Append(Entry{EventID: fmt.Sprintf("e%d", i)})
	}
	got := r.Recent(0)
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	for i, e := range got {
		want := fmt.Sprintf("e%d", i)
		if e.EventID != want {
			t.Fatalf("idx %d: got %q want %q", i, e.EventID, want)
		}
	}
}

func TestRing_Recent_LimitClamps(t *testing.T) {
	t.Parallel()
	r, _ := NewRing(8)
	for i := 0; i < 5; i++ {
		r.Append(Entry{EventID: fmt.Sprintf("e%d", i)})
	}
	got := r.Recent(2)
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	if got[0].EventID != "e3" || got[1].EventID != "e4" {
		t.Fatalf("got %v, want [e3 e4]", got)
	}
	// limit > len returns full ring
	got = r.Recent(100)
	if len(got) != 5 {
		t.Fatalf("oversized limit: len=%d want 5", len(got))
	}
}

func TestRing_Recent_EmptyReturnsNil(t *testing.T) {
	t.Parallel()
	r, _ := NewRing(4)
	if got := r.Recent(10); got != nil {
		t.Fatalf("empty ring: got %v want nil", got)
	}
}

func TestRing_Append_EvictsOldestWhenFull(t *testing.T) {
	t.Parallel()
	r, _ := NewRing(3)
	for i := 0; i < 6; i++ {
		r.Append(Entry{EventID: fmt.Sprintf("e%d", i)})
	}
	got := r.Recent(0)
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	for i, e := range got {
		want := fmt.Sprintf("e%d", i+3)
		if e.EventID != want {
			t.Fatalf("idx %d: got %q want %q", i, e.EventID, want)
		}
	}
}

func TestRing_Recent_ReturnsCopy(t *testing.T) {
	t.Parallel()
	r, _ := NewRing(4)
	r.Append(Entry{EventID: "a", Model: "claude"})
	got := r.Recent(0)
	got[0].Model = "mutated"
	again := r.Recent(0)
	if again[0].Model != "claude" {
		t.Fatalf("mutation of returned slice leaked into ring: %v", again[0])
	}
}

func TestRing_Subscribe_DeliversAppendedEntries(t *testing.T) {
	t.Parallel()
	r, _ := NewRing(4)
	sub := r.Subscribe(8)
	defer sub.Close()

	go func() {
		for i := 0; i < 3; i++ {
			r.Append(Entry{EventID: fmt.Sprintf("e%d", i)})
		}
	}()

	deadline := time.After(2 * time.Second)
	for i := 0; i < 3; i++ {
		select {
		case e := <-sub.C():
			want := fmt.Sprintf("e%d", i)
			if e.EventID != want {
				t.Fatalf("idx %d: got %q want %q", i, e.EventID, want)
			}
		case <-deadline:
			t.Fatalf("timeout waiting for entry %d", i)
		}
	}
}

func TestRing_Subscribe_DropsOnFullBuffer(t *testing.T) {
	t.Parallel()
	r, _ := NewRing(100)
	sub := r.Subscribe(1) // tiny buffer
	defer sub.Close()
	// Append many entries without reading from the channel; only one
	// can sit in the buffer, the rest must drop on the per-subscriber
	// counter rather than blocking Append.
	const N = 50
	for i := 0; i < N; i++ {
		r.Append(Entry{EventID: fmt.Sprintf("e%d", i)})
	}
	if sub.Dropped() == 0 {
		t.Fatalf("expected drops > 0, got 0")
	}
	// First entry should still be sitting in the buffer.
	select {
	case e := <-sub.C():
		if e.EventID != "e0" {
			t.Fatalf("first entry: got %q want e0", e.EventID)
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout reading first entry")
	}
}

func TestRing_Subscribe_CloseIsIdempotent(t *testing.T) {
	t.Parallel()
	r, _ := NewRing(4)
	sub := r.Subscribe(2)
	sub.Close()
	sub.Close() // must not panic
	if got := r.SubscriberCount(); got != 0 {
		t.Fatalf("subscriber count after close: got %d want 0", got)
	}
	// Channel should be drained + closed.
	if _, ok := <-sub.C(); ok {
		t.Fatalf("channel should be closed after Close()")
	}
}

func TestRing_Subscribe_AppendAfterCloseDoesNotPanic(t *testing.T) {
	t.Parallel()
	r, _ := NewRing(4)
	sub := r.Subscribe(2)
	sub.Close()
	// Must not panic — closed flag short-circuits delivery.
	r.Append(Entry{EventID: "e0"})
}

func TestRing_AppendAndSubscribeAreConcurrencySafe(t *testing.T) {
	t.Parallel()
	r, _ := NewRing(64)
	var wg sync.WaitGroup
	const writers = 4
	const perWriter = 200
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				r.Append(Entry{EventID: fmt.Sprintf("w%d-e%d", w, i)})
			}
		}(w)
	}

	// Concurrent subscriber that just drains.
	sub := r.Subscribe(1024)
	defer sub.Close()
	done := make(chan struct{})
	go func() {
		for range sub.C() {
		}
		close(done)
	}()

	wg.Wait()
	sub.Close()
	<-done

	got := r.Recent(0)
	if len(got) != 64 {
		t.Fatalf("len=%d, want capacity=64", len(got))
	}
}

func TestRing_Capacity(t *testing.T) {
	t.Parallel()
	r, _ := NewRing(7)
	if got := r.Capacity(); got != 7 {
		t.Fatalf("Capacity()=%d, want 7", got)
	}
}
