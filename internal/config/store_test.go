package config

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewStore_PanicsOnNil(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewStore(nil) did not panic")
		}
	}()
	_ = NewStore(nil)
}

func TestStore_SnapshotReturnsInitial(t *testing.T) {
	t.Parallel()
	initial := &ResolvedConfigV2{}
	s := NewStore(initial)
	if got := s.Snapshot(); got != initial {
		t.Fatalf("Snapshot()=%p, want %p", got, initial)
	}
}

func TestStore_ReplaceSwapsSnapshot(t *testing.T) {
	t.Parallel()
	first := &ResolvedConfigV2{}
	second := &ResolvedConfigV2{}
	s := NewStore(first)
	s.Replace(second)
	if got := s.Snapshot(); got != second {
		t.Fatalf("Snapshot()=%p, want %p (second)", got, second)
	}
}

func TestStore_ReplacePanicsOnNil(t *testing.T) {
	t.Parallel()
	s := NewStore(&ResolvedConfigV2{})
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Replace(nil) did not panic")
		}
	}()
	s.Replace(nil)
}

func TestStore_SubscribeFiresImmediately(t *testing.T) {
	t.Parallel()
	initial := &ResolvedConfigV2{}
	s := NewStore(initial)

	var got *ResolvedConfigV2
	s.Subscribe(func(r *ResolvedConfigV2) { got = r })

	if got != initial {
		t.Fatalf("subscriber received %p on Subscribe, want initial %p", got, initial)
	}
}

func TestStore_SubscribeFiresOnReplace(t *testing.T) {
	t.Parallel()
	first := &ResolvedConfigV2{}
	second := &ResolvedConfigV2{}
	s := NewStore(first)

	var calls []*ResolvedConfigV2
	s.Subscribe(func(r *ResolvedConfigV2) { calls = append(calls, r) })

	s.Replace(second)

	if len(calls) != 2 {
		t.Fatalf("subscriber fired %d times, want 2 (Subscribe + Replace)", len(calls))
	}
	if calls[0] != first {
		t.Fatalf("calls[0]=%p, want first %p", calls[0], first)
	}
	if calls[1] != second {
		t.Fatalf("calls[1]=%p, want second %p", calls[1], second)
	}
}

func TestStore_MultipleSubscribersAllFire(t *testing.T) {
	t.Parallel()
	first := &ResolvedConfigV2{}
	second := &ResolvedConfigV2{}
	s := NewStore(first)

	var a, b int
	s.Subscribe(func(*ResolvedConfigV2) { a++ })
	s.Subscribe(func(*ResolvedConfigV2) { b++ })

	s.Replace(second)

	if a != 2 || b != 2 {
		t.Fatalf("subscriber counts a=%d b=%d, want 2,2", a, b)
	}
}

func TestStore_SubscribeNilIsNoop(t *testing.T) {
	t.Parallel()
	s := NewStore(&ResolvedConfigV2{})
	s.Subscribe(nil)
	s.Replace(&ResolvedConfigV2{})
}

func TestStore_ConcurrentReadDuringReplace(t *testing.T) {
	t.Parallel()
	const readers = 16
	const swaps = 200

	configs := make([]*ResolvedConfigV2, swaps+1)
	for i := range configs {
		configs[i] = &ResolvedConfigV2{}
	}
	s := NewStore(configs[0])

	var wg sync.WaitGroup
	stop := make(chan struct{})

	var nilReads atomic.Uint64
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if s.Snapshot() == nil {
					nilReads.Add(1)
				}
			}
		}()
	}

	for i := 1; i <= swaps; i++ {
		s.Replace(configs[i])
	}
	close(stop)
	wg.Wait()

	if got := nilReads.Load(); got != 0 {
		t.Fatalf("observed %d nil snapshots during concurrent Replace", got)
	}
	if got := s.Snapshot(); got != configs[swaps] {
		t.Fatalf("final snapshot=%p, want last config %p", got, configs[swaps])
	}
}
