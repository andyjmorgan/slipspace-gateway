package proxy

import (
	"reflect"
	"sync"
	"testing"
	"time"
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
	tr := newTransport(DefaultResponseHeaderTimeout)
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
	if tr.ResponseHeaderTimeout.Seconds() != 120 {
		t.Fatalf("ResponseHeaderTimeout: want 120s got %s", tr.ResponseHeaderTimeout)
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

func TestNewTransport_ResponseHeaderTimeout(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"explicit", 200 * time.Second, 200 * time.Second},
		{"zero falls back to default", 0, DefaultResponseHeaderTimeout},
		{"negative falls back to default", -5 * time.Second, DefaultResponseHeaderTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTransport(tc.in)
			if tr.ResponseHeaderTimeout != tc.want {
				t.Fatalf("ResponseHeaderTimeout: want %s got %s", tc.want, tr.ResponseHeaderTimeout)
			}
		})
	}
}

func TestNew_ResponseHeaderTimeoutThreadedToTransport(t *testing.T) {
	f := New(Options{ResponseHeaderTimeout: 200 * time.Second})
	tr := f.getOrCreateTransport("https://api.openai.com")
	if tr.ResponseHeaderTimeout != 200*time.Second {
		t.Fatalf("transport ResponseHeaderTimeout: want 200s got %s", tr.ResponseHeaderTimeout)
	}

	def := New(Options{})
	trDef := def.getOrCreateTransport("https://api.openai.com")
	if trDef.ResponseHeaderTimeout != DefaultResponseHeaderTimeout {
		t.Fatalf("default transport ResponseHeaderTimeout: want %s got %s", DefaultResponseHeaderTimeout, trDef.ResponseHeaderTimeout)
	}
}
