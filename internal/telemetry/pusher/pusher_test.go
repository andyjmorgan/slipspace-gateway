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

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/config"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/registry"
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
		Endpoint:      "chat",
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
	drain(t, p)
}
