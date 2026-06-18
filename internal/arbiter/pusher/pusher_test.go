package pusher

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/registry"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func sampleRecord() cc.Record {
	return cc.Record{
		V:             1,
		ID:            "r1",
		CorrelationID: "c1",
		InstanceID:    "i1",
		Seq:           1,
		TsNs:          99,
		Provider:      "openai",
		Protocol:      "chat",
		Configuration: "prod",
		SchemaVersion: cc.SchemaVersion,
		Request:       cc.RequestPart{Method: "POST", Body: json.RawMessage(`{"k":"v"}`)},
	}
}

// drain closes the pusher with a generous deadline.
func drain(t *testing.T, p *Pusher) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Close(ctx)
}

// recordReceiver is a minimal stand-in for the telemetry record-ingest
// endpoint: it verifies the HMAC against a registry (the real trust check)
// and decodes the bare cc.Record body. It proves the pusher's signing +
// wire shape without coupling the pusher to the ingest package's storage.
type recordReceiver struct {
	reg  *registry.Registry
	mu   sync.Mutex
	last cc.Record
	n    int
}

func (rr *recordReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if err := rr.reg.Verify(r.Header.Get(headerGatewayID), body, r.Header.Get(headerSignature)); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var rec cc.Record
	if err := json.Unmarshal(body, &rec); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	rr.mu.Lock()
	rr.last = rec
	rr.n++
	rr.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

// TestPusher_EndToEnd proves the gateway pusher's signing matches registry
// verification and the bare-Record wire shape, end to end over httptest.
func TestPusher_EndToEnd(t *testing.T) {
	const gwID, secret = "gw-a", "shhh"
	rr := &recordReceiver{reg: registry.New([]config.Gateway{{ID: gwID, HMACSecret: secret}})}
	srv := httptest.NewServer(rr)
	defer srv.Close()

	p := New(Options{
		Endpoint:  srv.URL,
		GatewayID: gwID,
		Secret:    secret,
		Workers:   1,
		Logger:    discard(),
	})
	if !p.Enqueue(sampleRecord()) {
		t.Fatal("Enqueue should accept")
	}
	drain(t, p)

	if p.Sent() != 1 {
		t.Fatalf("sent = %d, want 1 (failed=%d)", p.Sent(), p.Failed())
	}
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rr.n != 1 || rr.last.CorrelationID != "c1" || rr.last.Provider != "openai" {
		t.Fatalf("received = %+v (n=%d)", rr.last, rr.n)
	}
}

// TestPusher_RejectedSignature confirms a wrong secret is rejected by the
// receiver and counted as failed, never as sent.
func TestPusher_RejectedSignature(t *testing.T) {
	rr := &recordReceiver{reg: registry.New([]config.Gateway{{ID: "gw-a", HMACSecret: "right"}})}
	srv := httptest.NewServer(rr)
	defer srv.Close()

	p := New(Options{Endpoint: srv.URL, GatewayID: "gw-a", Secret: "wrong", Workers: 1, Logger: discard()})
	p.Enqueue(sampleRecord())
	drain(t, p)

	if p.Sent() != 0 || p.Failed() != 1 {
		t.Fatalf("sent=%d failed=%d, want 0/1", p.Sent(), p.Failed())
	}
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rr.n != 0 {
		t.Fatal("nothing should be received on bad signature")
	}
}

// blockingDoer blocks until released, so the queue fills deterministically.
type blockingDoer struct {
	release chan struct{}
	calls   atomic.Int64
}

func (b *blockingDoer) Do(req *http.Request) (*http.Response, error) {
	b.calls.Add(1)
	<-b.release
	return &http.Response{StatusCode: 200, Body: io.NopCloser(http.NoBody)}, nil
}

func TestPusher_DropsWhenFull(t *testing.T) {
	doer := &blockingDoer{release: make(chan struct{})}
	// One worker, buffer 1: worker grabs one (blocks in Do), buffer holds one,
	// the rest drop.
	p := New(Options{Endpoint: "http://x", Secret: "s", Workers: 1, Buffer: 1, Client: doer, Logger: discard()})

	// Wait until the single worker is parked inside Do so the channel slot is
	// the only free capacity.
	deadline := time.After(2 * time.Second)
	for doer.calls.Load() == 0 {
		p.Enqueue(sampleRecord())
		select {
		case <-deadline:
			t.Fatal("worker never entered Do")
		default:
		}
		time.Sleep(time.Millisecond)
	}
	// Worker is blocked; fill the 1-slot buffer then overflow.
	p.Enqueue(sampleRecord()) // fills buffer (best effort)
	got := 0
	for range 50 {
		if !p.Enqueue(sampleRecord()) {
			got++
		}
	}
	if got == 0 {
		t.Fatal("expected some drops when the queue is full")
	}
	if p.Dropped() == 0 {
		t.Fatal("dropped counter should be > 0")
	}
	close(doer.release)
	drain(t, p)
}

func TestPusher_CloseTimeout(t *testing.T) {
	doer := &blockingDoer{release: make(chan struct{})}
	p := New(Options{Endpoint: "http://x", Secret: "s", Workers: 1, Buffer: 4, Client: doer, Logger: discard()})
	p.Enqueue(sampleRecord())
	// Worker is stuck in Do; Close with a tiny deadline must return (timeout
	// path), not hang.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	p.Close(ctx)
	close(doer.release)
}

// errDoer always fails the transport.
type errDoer struct{}

func (errDoer) Do(*http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF }

// TestSend_ErrorBranches drives send() directly to cover the failure paths.
func TestSend_ErrorBranches(t *testing.T) {
	// transport error
	p := New(Options{Endpoint: "http://x", Secret: "s", Client: errDoer{}, Logger: discard()})
	p.send(sampleRecord())
	if p.Failed() != 1 {
		t.Errorf("transport: failed = %d, want 1", p.Failed())
	}
	drain(t, p)

	// marshal error: an invalid json.RawMessage body makes json.Marshal fail.
	p2 := New(Options{Endpoint: "http://x", Secret: "s", Client: errDoer{}, Logger: discard()})
	p2.send(cc.Record{CorrelationID: "c", Request: cc.RequestPart{Body: json.RawMessage("{not json")}})
	if p2.Failed() != 1 {
		t.Errorf("marshal: failed = %d, want 1", p2.Failed())
	}
	drain(t, p2)

	// request-build error: a control char in the URL makes http.NewRequest fail.
	p3 := New(Options{Endpoint: "http://\x7f\n", Secret: "s", Client: errDoer{}, Logger: discard()})
	p3.send(sampleRecord())
	if p3.Failed() != 1 {
		t.Errorf("build: failed = %d, want 1", p3.Failed())
	}
	drain(t, p3)
}

func TestPusher_Defaults(t *testing.T) {
	p := New(Options{Endpoint: "http://x", Secret: "s"})
	if cap(p.ch) != defaultBuffer {
		t.Errorf("buffer = %d, want %d", cap(p.ch), defaultBuffer)
	}
	if p.maxAttempts != defaultMaxAttempts {
		t.Errorf("maxAttempts = %d, want %d", p.maxAttempts, defaultMaxAttempts)
	}
	if p.backoffBase != defaultBackoffBase || p.backoffMax != defaultBackoffMax {
		t.Errorf("backoff = %v/%v, want %v/%v", p.backoffBase, p.backoffMax, defaultBackoffBase, defaultBackoffMax)
	}
	drain(t, p)
}

// scriptedResp is one canned outcome for scriptedDoer.
type scriptedResp struct {
	status int
	err    error
}

// scriptedDoer plays back a fixed sequence of responses, repeating the last
// entry once the script runs out, so retry behaviour is deterministic.
type scriptedDoer struct {
	mu    sync.Mutex
	seq   []scriptedResp
	calls int
}

func (s *scriptedDoer) Do(*http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.calls
	s.calls++
	if i >= len(s.seq) {
		i = len(s.seq) - 1
	}
	r := s.seq[i]
	if r.err != nil {
		return nil, r.err
	}
	return &http.Response{StatusCode: r.status, Body: io.NopCloser(http.NoBody)}, nil
}

func (s *scriptedDoer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// hookLog collects OnDropped / OnFailure invocations. Worker goroutines call
// the hooks concurrently, hence the mutex.
type hookLog struct {
	mu    sync.Mutex
	drops []string
	fails []string
}

func (h *hookLog) onDropped(reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.drops = append(h.drops, reason)
}

func (h *hookLog) onFailure(kind string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fails = append(h.fails, kind)
}

func (h *hookLog) snapshot() (drops, fails []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.drops...), append([]string(nil), h.fails...)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestPusher_RetryBackoffGiveUp drives one record through the retry loop per
// case: transient failures (network, 408/429/5xx) retry up to MaxAttempts;
// permanent rejections (other 4xx) and exhausted schedules drop the record
// with the matching reason. The incident this pins: a transient 502 from the
// Arbiter permanently lost records because no retry existed.
func TestPusher_RetryBackoffGiveUp(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		seq         []scriptedResp
		maxAttempts int
		wantCalls   int
		wantSent    int64
		wantFailed  int64
		wantLost    int64
		wantDrops   []string
		wantFails   []string
	}{
		{
			name:      "transient 502 then success",
			seq:       []scriptedResp{{status: 502}, {status: 200}},
			wantCalls: 2, wantSent: 1, wantFailed: 1,
			wantFails: []string{FailureStatus},
		},
		{
			name:      "network error then success",
			seq:       []scriptedResp{{err: io.ErrUnexpectedEOF}, {status: 200}},
			wantCalls: 2, wantSent: 1, wantFailed: 1,
			wantFails: []string{FailureNetwork},
		},
		{
			name:      "throttle then outage then success",
			seq:       []scriptedResp{{status: 429}, {status: 503}, {status: 200}},
			wantCalls: 3, wantSent: 1, wantFailed: 2,
			wantFails: []string{FailureStatus, FailureStatus},
		},
		{
			name:      "request timeout 408 retried",
			seq:       []scriptedResp{{status: 408}, {status: 202}},
			wantCalls: 2, wantSent: 1, wantFailed: 1,
			wantFails: []string{FailureStatus},
		},
		{
			name:      "non-retryable 400 gives up immediately",
			seq:       []scriptedResp{{status: 400}},
			wantCalls: 1, wantFailed: 1, wantLost: 1,
			wantDrops: []string{DropRejected},
			wantFails: []string{FailureStatus},
		},
		{
			name:      "bad HMAC 401 never retried",
			seq:       []scriptedResp{{status: 401}},
			wantCalls: 1, wantFailed: 1, wantLost: 1,
			wantDrops: []string{DropRejected},
			wantFails: []string{FailureStatus},
		},
		{
			name:        "persistent 502 exhausts attempts",
			seq:         []scriptedResp{{status: 502}},
			maxAttempts: 3,
			wantCalls:   3, wantFailed: 3, wantLost: 1,
			wantDrops: []string{DropExhausted},
			wantFails: []string{FailureStatus, FailureStatus, FailureStatus},
		},
		{
			name:        "persistent network error exhausts attempts",
			seq:         []scriptedResp{{err: io.ErrUnexpectedEOF}},
			maxAttempts: 2,
			wantCalls:   2, wantFailed: 2, wantLost: 1,
			wantDrops: []string{DropExhausted},
			wantFails: []string{FailureNetwork, FailureNetwork},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doer := &scriptedDoer{seq: tc.seq}
			hooks := &hookLog{}
			p := New(Options{
				Endpoint:    "http://x",
				Secret:      "s",
				Workers:     1,
				Buffer:      4,
				Client:      doer,
				Logger:      discard(),
				MaxAttempts: tc.maxAttempts,
				BackoffBase: time.Millisecond,
				BackoffMax:  4 * time.Millisecond,
				OnDropped:   hooks.onDropped,
				OnFailure:   hooks.onFailure,
			})
			if !p.Enqueue(sampleRecord()) {
				t.Fatal("Enqueue should accept")
			}
			drain(t, p)

			if got := doer.callCount(); got != tc.wantCalls {
				t.Errorf("attempts = %d, want %d", got, tc.wantCalls)
			}
			if p.Sent() != tc.wantSent || p.Failed() != tc.wantFailed || p.Lost() != tc.wantLost {
				t.Errorf("sent/failed/lost = %d/%d/%d, want %d/%d/%d",
					p.Sent(), p.Failed(), p.Lost(), tc.wantSent, tc.wantFailed, tc.wantLost)
			}
			drops, fails := hooks.snapshot()
			if !equalStrings(drops, tc.wantDrops) {
				t.Errorf("drop reasons = %v, want %v", drops, tc.wantDrops)
			}
			if !equalStrings(fails, tc.wantFails) {
				t.Errorf("failure kinds = %v, want %v", fails, tc.wantFails)
			}
		})
	}
}

// TestBackoffFor pins the capped exponential schedule.
func TestBackoffFor(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Endpoint: "http://x", Secret: "s", Logger: discard(),
		BackoffBase: 100 * time.Millisecond, BackoffMax: time.Second,
	})
	defer drain(t, p)

	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 800 * time.Millisecond},
		{5, time.Second},  // doubled past the cap
		{10, time.Second}, // stays pinned at the cap
	}
	for _, tc := range cases {
		if got := p.backoffFor(tc.attempt); got != tc.want {
			t.Errorf("backoffFor(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}

	// A base above the cap is clamped from the first retry.
	p2 := New(Options{
		Endpoint: "http://x", Secret: "s", Logger: discard(),
		BackoffBase: 2 * time.Second, BackoffMax: time.Second,
	})
	defer drain(t, p2)
	if got := p2.backoffFor(1); got != time.Second {
		t.Errorf("clamped backoffFor(1) = %v, want 1s", got)
	}
}

// TestPusher_QueueFullDropHook proves the Enqueue-side cap reports queue_full
// through the dropped hook — the metric feed for invariant-#2-style loss.
func TestPusher_QueueFullDropHook(t *testing.T) {
	doer := &blockingDoer{release: make(chan struct{})}
	hooks := &hookLog{}
	p := New(Options{
		Endpoint: "http://x", Secret: "s", Workers: 1, Buffer: 1,
		Client: doer, Logger: discard(), OnDropped: hooks.onDropped,
	})

	// Park the single worker inside Do so the channel slot is the only
	// free capacity, then overflow it.
	deadline := time.After(2 * time.Second)
	for doer.calls.Load() == 0 {
		p.Enqueue(sampleRecord())
		select {
		case <-deadline:
			t.Fatal("worker never entered Do")
		default:
		}
		time.Sleep(time.Millisecond)
	}
	p.Enqueue(sampleRecord()) // fills the buffer (best effort)
	for range 20 {
		p.Enqueue(sampleRecord())
	}

	drops, _ := hooks.snapshot()
	if int64(len(drops)) != p.Dropped() {
		t.Fatalf("hook fired %d times, Dropped() = %d", len(drops), p.Dropped())
	}
	if p.Dropped() == 0 {
		t.Fatal("expected queue-full drops")
	}
	for _, r := range drops {
		if r != DropQueueFull {
			t.Fatalf("drop reason = %q, want %q", r, DropQueueFull)
		}
	}
	close(doer.release)
	drain(t, p)
}

// TestPusher_CloseSkipsBackoff proves a shutdown drain does not sleep out the
// backoff schedule: remaining retries run back-to-back so Close stays bounded
// by HTTP timeouts, not by minutes of accumulated waits.
func TestPusher_CloseSkipsBackoff(t *testing.T) {
	t.Parallel()

	doer := &scriptedDoer{seq: []scriptedResp{{status: 502}}}
	p := New(Options{
		Endpoint: "http://x", Secret: "s", Workers: 1, Buffer: 4,
		Client: doer, Logger: discard(),
		MaxAttempts: 3,
		BackoffBase: 30 * time.Second, // would dwarf the Close deadline if honoured
		BackoffMax:  time.Minute,
	})
	if !p.Enqueue(sampleRecord()) {
		t.Fatal("Enqueue should accept")
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Close(ctx)

	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Close took %v; draining must skip backoff waits", elapsed)
	}
	if got := doer.callCount(); got != 3 {
		t.Errorf("attempts = %d, want all 3 during drain", got)
	}
	if p.Lost() != 1 {
		t.Errorf("lost = %d, want 1 (exhausted during drain)", p.Lost())
	}
}
