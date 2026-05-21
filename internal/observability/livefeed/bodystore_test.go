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

// probeSlotSize Puts a representative envelope into a throwaway store
// and reports the compressed footprint the store ended up holding.
// Lets the LRU tests compute realistic capacity multiples without
// hard-coding zstd output sizes.
func probeSlotSize(t *testing.T, payload []byte) int {
	t.Helper()
	probe, err := NewBodyStore(1 << 20)
	if err != nil {
		t.Fatalf("probe: NewBodyStore: %v", err)
	}
	probe.Put("probe", BodyEnvelope{Request: payload})
	return probe.Bytes()
}

func TestBodyStore_BytesAccounting(t *testing.T) {
	t.Parallel()
	// Realistic chat-completion-style payloads, large enough that zstd
	// actually shrinks them and small enough that the test stays fast.
	payloadA := []byte(strings.Repeat(`{"role":"user","content":"hello"}`, 32))
	payloadB := []byte(strings.Repeat(`{"role":"assistant","content":"hi there"}`, 32))

	s, _ := NewBodyStore(1 << 20)
	s.Put("a", BodyEnvelope{Request: payloadA})
	sizeA := s.Bytes()
	s.Put("b", BodyEnvelope{Request: payloadB})
	sizeAB := s.Bytes()

	if sizeA <= 0 {
		t.Errorf("first put produced no accounted bytes")
	}
	if sizeAB <= sizeA {
		t.Errorf("second put did not increase bytes (sizeA=%d sizeAB=%d)", sizeA, sizeAB)
	}
	if got := s.Len(); got != 2 {
		t.Errorf("len=%d want 2", got)
	}
	// Compression should leave the stored footprint well under the
	// uncompressed source.
	uncompressed := len(payloadA) + len(payloadB)
	if sizeAB >= uncompressed {
		t.Errorf("compression did not shrink: stored %d bytes vs raw %d", sizeAB, uncompressed)
	}
}

func TestBodyStore_LRUEviction(t *testing.T) {
	t.Parallel()
	// Three envelopes fit; the fourth must evict the oldest.
	payload := []byte(strings.Repeat(`{"k":"v"}`, 128))
	slot := probeSlotSize(t, payload)
	s, _ := NewBodyStore(slot * 3)
	s.Put("a", BodyEnvelope{Request: payload})
	s.Put("b", BodyEnvelope{Request: payload})
	s.Put("c", BodyEnvelope{Request: payload})
	if got := s.Len(); got != 3 {
		t.Fatalf("setup: len=%d want 3", got)
	}
	s.Put("d", BodyEnvelope{Request: payload})
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
	payload := []byte(strings.Repeat(`{"k":"v"}`, 128))
	slot := probeSlotSize(t, payload)
	s, _ := NewBodyStore(slot * 3)
	s.Put("a", BodyEnvelope{Request: payload})
	s.Put("b", BodyEnvelope{Request: payload})
	s.Put("c", BodyEnvelope{Request: payload})
	// Touch 'a' so it becomes most-recently-used; 'b' is now the oldest.
	if _, ok := s.Get("a"); !ok {
		t.Fatal("setup: Get('a') !ok")
	}
	s.Put("d", BodyEnvelope{Request: payload})
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

func TestBodyStore_RoundTripsThroughCompression(t *testing.T) {
	t.Parallel()
	s, _ := NewBodyStore(1 << 20)
	in := BodyEnvelope{
		Request:            []byte(`{"prompt":"the quick brown fox jumps over the lazy dog"}`),
		RequestTotalBytes:  56,
		RequestTruncated:   true,
		Response:           []byte(`{"choices":[{"message":{"role":"assistant","content":"woof"}}]}`),
		ResponseTotalBytes: 64,
		ResponseTruncated:  false,
		ResponseAssembled:  `{"id":"chatcmpl-1","object":"chat.completion","choices":[]}`,
		AssemblyPartial:    true,
		RequestHeaders:     map[string][]string{"Authorization": {"[REDACTED]"}, "Content-Type": {"application/json"}},
		ResponseHeaders:    map[string][]string{"Content-Type": {"application/json"}},
	}
	s.Put("evt-1", in)
	out, ok := s.Get("evt-1")
	if !ok {
		t.Fatal("Get !ok after Put")
	}
	if string(out.Request) != string(in.Request) {
		t.Errorf("Request mismatch:\n  in: %s\n out: %s", in.Request, out.Request)
	}
	if out.RequestTotalBytes != in.RequestTotalBytes || out.RequestTruncated != in.RequestTruncated {
		t.Errorf("request metadata lost: total=%d truncated=%v", out.RequestTotalBytes, out.RequestTruncated)
	}
	if string(out.Response) != string(in.Response) {
		t.Errorf("Response mismatch:\n  in: %s\n out: %s", in.Response, out.Response)
	}
	if out.ResponseAssembled != in.ResponseAssembled {
		t.Errorf("ResponseAssembled mismatch:\n  in: %s\n out: %s", in.ResponseAssembled, out.ResponseAssembled)
	}
	if out.AssemblyPartial != in.AssemblyPartial {
		t.Errorf("AssemblyPartial lost: %v", out.AssemblyPartial)
	}
	if got, want := out.RequestHeaders["Authorization"][0], "[REDACTED]"; got != want {
		t.Errorf("request header lost: %q", got)
	}
	if got, want := out.ResponseHeaders["Content-Type"][0], "application/json"; got != want {
		t.Errorf("response header lost: %q", got)
	}
}

func TestBodyStore_CompressesRealisticPayloads(t *testing.T) {
	t.Parallel()
	// Realistic chat-completion JSON has lots of repetition (role,
	// content, finish_reason, etc.). Even at modest sizes zstd should
	// produce a 3x+ reduction.
	body := []byte(strings.Repeat(`{"role":"assistant","content":"thank you for the question","finish_reason":"stop"}`, 64))
	s, _ := NewBodyStore(1 << 20)
	s.Put("e", BodyEnvelope{Request: body})
	stored := s.Bytes()
	if stored >= len(body) {
		t.Errorf("compression did not shrink: stored %d vs raw %d", stored, len(body))
	}
	if ratio := float64(len(body)) / float64(stored); ratio < 3 {
		t.Errorf("compression ratio %.1fx is suspiciously low for repetitive JSON", ratio)
	}
}
