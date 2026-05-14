package proxy

import (
	"reflect"
	"sync"
	"testing"
)

func TestTransportCacheReuse(t *testing.T) {
	f := New(Options{})

	t1 := f.getOrCreateTransport("https://api.openai.com")
	t2 := f.getOrCreateTransport("https://api.openai.com")
	t3 := f.getOrCreateTransport("https://api.anthropic.com")

	if t1 != t2 {
		t.Fatalf("expected same transport for identical baseURL, got %p and %p", t1, t2)
	}
	if t1 == t3 {
		t.Fatalf("expected distinct transports for distinct baseURLs")
	}
}

func TestTransportCacheConcurrent(t *testing.T) {
	f := New(Options{})

	const goroutines = 100
	results := make([]uintptr, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			tr := f.getOrCreateTransport("https://api.openai.com")
			results[idx] = reflect.ValueOf(tr).Pointer()
		}(i)
	}
	close(start)
	wg.Wait()

	first := results[0]
	for i, p := range results {
		if p != first {
			t.Fatalf("goroutine %d got a different transport pointer", i)
		}
	}
}

func TestTransportDefaults(t *testing.T) {
	tr := newTransport()
	if tr.MaxIdleConns != 100 {
		t.Fatalf("MaxIdleConns: want 100 got %d", tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != 50 {
		t.Fatalf("MaxIdleConnsPerHost: want 50 got %d", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout.Seconds() != 90 {
		t.Fatalf("IdleConnTimeout: want 90s got %s", tr.IdleConnTimeout)
	}
	if tr.TLSHandshakeTimeout.Seconds() != 10 {
		t.Fatalf("TLSHandshakeTimeout: want 10s got %s", tr.TLSHandshakeTimeout)
	}
	if tr.ResponseHeaderTimeout.Seconds() != 30 {
		t.Fatalf("ResponseHeaderTimeout: want 30s got %s", tr.ResponseHeaderTimeout)
	}
	if tr.ExpectContinueTimeout.Seconds() != 1 {
		t.Fatalf("ExpectContinueTimeout: want 1s got %s", tr.ExpectContinueTimeout)
	}
	if !tr.ForceAttemptHTTP2 {
		t.Fatalf("ForceAttemptHTTP2: want true")
	}
	if tr.DialContext == nil {
		t.Fatalf("DialContext: want non-nil")
	}
}
