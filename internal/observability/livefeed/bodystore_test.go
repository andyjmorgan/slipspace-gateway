package livefeed

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestNewBodyStore_RejectsNonPositive(t *testing.T) {
	t.Parallel()
	for _, c := range []int{0, -1, -1024} {
		if _, err := NewBodyStore(c); !errors.Is(err, ErrBodyCapacityNonPositive) {
			t.Fatalf("capacity=%d: want ErrBodyCapacityNonPositive, got %v", c, err)
		}
	}
}

func TestBodyStore_PutGet(t *testing.T) {
	t.Parallel()
	s, _ := NewBodyStore(1024)
	env := BodyEnvelope{Request: []byte("hello"), Response: []byte("world")}
	s.Put("e1", env)
	got, ok := s.Get("e1")
	if !ok {
		t.Fatal("Get returned !ok")
	}
	if string(got.Request) != "hello" || string(got.Response) != "world" {
		t.Errorf("got %+v", got)
	}
}

func TestBodyStore_Get_MissingReturnsZero(t *testing.T) {
	t.Parallel()
	s, _ := NewBodyStore(1024)
	if _, ok := s.Get("nope"); ok {
		t.Fatal("Get on missing returned ok=true")
	}
}

func TestBodyStore_BytesAccounting(t *testing.T) {
	t.Parallel()
	s, _ := NewBodyStore(1024)
	s.Put("a", BodyEnvelope{Request: []byte("abcd")})
	s.Put("b", BodyEnvelope{Request: []byte("efghij")})
	if got := s.Bytes(); got != 10 {
		t.Errorf("bytes=%d want 10", got)
	}
	if got := s.Len(); got != 2 {
		t.Errorf("len=%d want 2", got)
	}
}

func TestBodyStore_LRUEviction(t *testing.T) {
	t.Parallel()
	// Capacity 30 bytes; insert three 10-byte envelopes then a fourth,
	// expect the oldest to be evicted.
	s, _ := NewBodyStore(30)
	body := func(n int) []byte { return []byte(strings.Repeat("x", n)) }
	s.Put("a", BodyEnvelope{Request: body(10)})
	s.Put("b", BodyEnvelope{Request: body(10)})
	s.Put("c", BodyEnvelope{Request: body(10)})
	if got := s.Len(); got != 3 {
		t.Fatalf("setup: len=%d want 3", got)
	}
	s.Put("d", BodyEnvelope{Request: body(10)})
	if _, ok := s.Get("a"); ok {
		t.Fatalf("expected oldest 'a' to be evicted, but it is still present")
	}
	for _, k := range []string{"b", "c", "d"} {
		if _, ok := s.Get(k); !ok {
			t.Errorf("expected %q to remain", k)
		}
	}
}

func TestBodyStore_GetBumpsRecency(t *testing.T) {
	t.Parallel()
	s, _ := NewBodyStore(30)
	body := func(n int) []byte { return []byte(strings.Repeat("x", n)) }
	s.Put("a", BodyEnvelope{Request: body(10)})
	s.Put("b", BodyEnvelope{Request: body(10)})
	s.Put("c", BodyEnvelope{Request: body(10)})
	// Touch 'a' so it becomes most-recently-used; 'b' is now the oldest.
	if _, ok := s.Get("a"); !ok {
		t.Fatal("setup: Get('a') !ok")
	}
	s.Put("d", BodyEnvelope{Request: body(10)})
	if _, ok := s.Get("b"); ok {
		t.Fatalf("expected 'b' to be evicted, but it remains")
	}
	for _, k := range []string{"a", "c", "d"} {
		if _, ok := s.Get(k); !ok {
			t.Errorf("expected %q to remain", k)
		}
	}
}

func TestBodyStore_PutReplaceSameKey(t *testing.T) {
	t.Parallel()
	s, _ := NewBodyStore(1024)
	s.Put("a", BodyEnvelope{Request: []byte("first")})
	s.Put("a", BodyEnvelope{Request: []byte("second")})
	got, ok := s.Get("a")
	if !ok {
		t.Fatal("Get !ok")
	}
	if string(got.Request) != "second" {
		t.Errorf("Request=%q, want second", got.Request)
	}
	if got := s.Len(); got != 1 {
		t.Errorf("len=%d want 1 after replacing same key", got)
	}
}

func TestBodyStore_PutOverBudget_Dropped(t *testing.T) {
	t.Parallel()
	s, _ := NewBodyStore(8)
	// Single envelope larger than capacity → dropped silently.
	s.Put("big", BodyEnvelope{Request: []byte("0123456789abcdef")})
	if _, ok := s.Get("big"); ok {
		t.Fatal("envelope larger than capacity should not be stored")
	}
	if s.Len() != 0 || s.Bytes() != 0 {
		t.Errorf("expected empty store; len=%d bytes=%d", s.Len(), s.Bytes())
	}
}

func TestBodyStore_Delete(t *testing.T) {
	t.Parallel()
	s, _ := NewBodyStore(1024)
	s.Put("a", BodyEnvelope{Request: []byte("hello")})
	s.Delete("a")
	if _, ok := s.Get("a"); ok {
		t.Fatal("Get returned ok after Delete")
	}
	if s.Bytes() != 0 || s.Len() != 0 {
		t.Errorf("not empty after delete: bytes=%d len=%d", s.Bytes(), s.Len())
	}
	// Delete on missing key is a no-op.
	s.Delete("missing")
}

func TestBodyStore_ConcurrentPutGet(t *testing.T) {
	t.Parallel()
	s, _ := NewBodyStore(64 * 1024)
	var wg sync.WaitGroup
	const writers = 8
	const perWriter = 100
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				key := fmt.Sprintf("w%d-e%d", w, i)
				s.Put(key, BodyEnvelope{Request: []byte(key)})
				if _, ok := s.Get(key); !ok {
					t.Errorf("missing right after Put: %s", key)
				}
			}
		}(w)
	}
	wg.Wait()
}

func TestBodyEnvelope_BytesAccountsAllFields(t *testing.T) {
	t.Parallel()
	env := BodyEnvelope{
		Request:           []byte("req"),
		Response:          []byte("resp-bytes"),
		ResponseAssembled: `{"choices":[]}`,
	}
	want := len("req") + len("resp-bytes") + len(`{"choices":[]}`)
	if got := env.Bytes(); got != want {
		t.Fatalf("Bytes=%d want %d", got, want)
	}
}
