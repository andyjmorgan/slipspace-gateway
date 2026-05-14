package bus_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/andyjmorgan/sluice-gateway/internal/bus"
)

// stubJS is a hand-rolled bus.JetStreamPublisher that records every
// published subject + payload. publishFn lets a test inject errors or
// blocking behavior.
type stubJS struct {
	mu       sync.Mutex
	messages []stubMsg

	publishFn func(ctx context.Context, subject string, data []byte) (*jetstream.PubAck, error)
}

type stubMsg struct {
	Subject string
	Data    []byte
}

func (s *stubJS) Publish(ctx context.Context, subject string, data []byte, _ ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	if s.publishFn != nil {
		ack, err := s.publishFn(ctx, subject, data)
		if err == nil {
			s.record(subject, data)
		}
		return ack, err
	}
	s.record(subject, data)
	return &jetstream.PubAck{Stream: "GATEWAY_EVENTS"}, nil
}

func (s *stubJS) record(subject string, data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)
	s.mu.Lock()
	s.messages = append(s.messages, stubMsg{Subject: subject, Data: cp})
	s.mu.Unlock()
}

func (s *stubJS) snapshot() []stubMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]stubMsg, len(s.messages))
	copy(out, s.messages)
	return out
}

// stubStore is a hand-rolled bus.ObjectPutter. It records every Put and
// can be made to fail.
type stubStore struct {
	mu      sync.Mutex
	puts    []stubPut
	failErr error
}

type stubPut struct {
	Name string
	Size int
}

func (s *stubStore) PutBytes(_ context.Context, name string, data []byte) (*jetstream.ObjectInfo, error) {
	if s.failErr != nil {
		return nil, s.failErr
	}
	s.mu.Lock()
	s.puts = append(s.puts, stubPut{Name: name, Size: len(data)})
	s.mu.Unlock()
	return &jetstream.ObjectInfo{
		Bucket: bus.DefaultObjectStoreBucket,
		Size:   uint64(len(data)),
	}, nil
}

func (s *stubStore) snapshot() []stubPut {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]stubPut, len(s.puts))
	copy(out, s.puts)
	return out
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// waitTimeout is the default polling deadline for waitForStats. Long enough
// to absorb CI scheduling jitter, short enough that a real bug fails fast.
const waitTimeout = time.Second

func waitForStats(t *testing.T, p *bus.Publisher, want bus.Stats) bus.Stats {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	var got bus.Stats
	for time.Now().Before(deadline) {
		got = p.Stats()
		if got.Published >= want.Published && got.Dropped >= want.Dropped && got.Stashed >= want.Stashed {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("stats never reached want=%+v, got=%+v", want, got)
	return got
}

func newEnv(id string, payload []byte) bus.Envelope {
	return bus.Envelope{
		SchemaVersion: bus.SchemaVersion,
		EventID:       id,
		EventType:     "request",
		Timestamp:     time.Now().UTC(),
		Mode:          bus.PayloadInline,
		InlinePayload: payload,
	}
}

func TestPublisher_PublishEnqueuesAndDrains(t *testing.T) {
	t.Parallel()

	js := &stubJS{}
	p := bus.New(bus.Options{
		JS:        js,
		QueueSize: 8,
		Workers:   2,
		Logger:    discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop(time.Second)

	for i := 0; i < 5; i++ {
		p.Publish(newEnv("e", []byte(`{"i":1}`)))
	}

	got := waitForStats(t, p, bus.Stats{Published: 5})
	if got.Dropped != 0 {
		t.Fatalf("Dropped = %d, want 0", got.Dropped)
	}
	if got.Stashed != 0 {
		t.Fatalf("Stashed = %d, want 0", got.Stashed)
	}

	msgs := js.snapshot()
	if len(msgs) != 5 {
		t.Fatalf("recorded %d messages, want 5", len(msgs))
	}
	for _, m := range msgs {
		if m.Subject != "gateway.request" {
			t.Errorf("subject = %q, want gateway.request", m.Subject)
		}
		var env bus.Envelope
		if err := msgpack.Unmarshal(m.Data, &env); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if env.Mode != bus.PayloadInline {
			t.Errorf("Mode = %d, want PayloadInline", env.Mode)
		}
	}
}

func TestPublisher_QueueFullDrops(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})

	js := &stubJS{
		publishFn: func(_ context.Context, _ string, _ []byte) (*jetstream.PubAck, error) {
			<-block
			return &jetstream.PubAck{}, nil
		},
	}

	p := bus.New(bus.Options{
		JS:        js,
		QueueSize: 1,
		Workers:   1,
		Logger:    discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)

	// Each Publish must return immediately. With a 1-deep queue and a worker
	// hung on publishFn, every send after the first two (one in worker, one
	// in queue) must be dropped — never block the caller.
	const tries = 200
	doneCh := make(chan struct{})
	go func() {
		for i := 0; i < tries; i++ {
			p.Publish(newEnv("flood", []byte(`f`)))
		}
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked instead of dropping")
	}

	stats := p.Stats()
	if stats.Dropped == 0 {
		t.Fatalf("expected drops with queue capacity 1 and hung worker; got %+v", stats)
	}

	close(block)
	p.Stop(2 * time.Second)
}

func TestPublisher_DispatchErrorDrops(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("publish failed")
	js := &stubJS{
		publishFn: func(_ context.Context, _ string, _ []byte) (*jetstream.PubAck, error) {
			return nil, sentinel
		},
	}
	p := bus.New(bus.Options{
		JS:        js,
		QueueSize: 8,
		Workers:   1,
		Logger:    discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop(time.Second)

	for i := 0; i < 3; i++ {
		p.Publish(newEnv("x", []byte(`x`)))
	}

	got := waitForStats(t, p, bus.Stats{Dropped: 3})
	if got.Published != 0 {
		t.Fatalf("Published = %d, want 0", got.Published)
	}
	if len(js.snapshot()) != 0 {
		t.Fatalf("expected no successful publishes, got %d", len(js.snapshot()))
	}
}

func TestPublisher_LargePayloadStashed(t *testing.T) {
	t.Parallel()

	js := &stubJS{}
	store := &stubStore{}
	threshold := 1024
	p := bus.New(bus.Options{
		JS:             js,
		ObjectStore:    store,
		QueueSize:      4,
		Workers:        1,
		StashThreshold: threshold,
		Logger:         discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop(time.Second)

	big := make([]byte, threshold+1)
	for i := range big {
		big[i] = 'A'
	}
	env := newEnv("big-1", big)
	p.Publish(env)

	got := waitForStats(t, p, bus.Stats{Published: 1, Stashed: 1})
	if got.Dropped != 0 {
		t.Fatalf("Dropped = %d, want 0", got.Dropped)
	}

	puts := store.snapshot()
	if len(puts) != 1 {
		t.Fatalf("Put calls = %d, want 1", len(puts))
	}
	if puts[0].Name != "big-1" {
		t.Errorf("Put name = %q, want big-1", puts[0].Name)
	}

	msgs := js.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("publish count = %d, want 1", len(msgs))
	}
	var pub bus.Envelope
	if err := msgpack.Unmarshal(msgs[0].Data, &pub); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pub.Mode != bus.PayloadStashed {
		t.Errorf("Mode = %d, want PayloadStashed", pub.Mode)
	}
	if pub.InlinePayload != nil {
		t.Errorf("InlinePayload non-nil after stash: %q", pub.InlinePayload)
	}
	if pub.ObjectRef == nil {
		t.Fatalf("ObjectRef nil after stash")
	}
	if pub.ObjectRef.Key != "big-1" {
		t.Errorf("ObjectRef.Key = %q, want big-1", pub.ObjectRef.Key)
	}
	if pub.ObjectRef.Size != int64(len(big)) {
		t.Errorf("ObjectRef.Size = %d, want %d", pub.ObjectRef.Size, len(big))
	}
}

func TestPublisher_LargePayloadNoStoreInlines(t *testing.T) {
	t.Parallel()

	js := &stubJS{}
	threshold := 256
	p := bus.New(bus.Options{
		JS:             js,
		QueueSize:      2,
		Workers:        1,
		StashThreshold: threshold,
		Logger:         discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop(time.Second)

	big := make([]byte, threshold+1)
	p.Publish(newEnv("big-noobj", big))

	got := waitForStats(t, p, bus.Stats{Published: 1})
	if got.Stashed != 0 {
		t.Fatalf("Stashed = %d, want 0", got.Stashed)
	}

	msgs := js.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("publish count = %d, want 1", len(msgs))
	}
	var pub bus.Envelope
	if err := msgpack.Unmarshal(msgs[0].Data, &pub); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pub.Mode != bus.PayloadInline {
		t.Errorf("Mode = %d, want PayloadInline (no object store should not switch)", pub.Mode)
	}
	if len(pub.InlinePayload) != len(big) {
		t.Errorf("InlinePayload size = %d, want %d", len(pub.InlinePayload), len(big))
	}
}

func TestPublisher_StashErrorDrops(t *testing.T) {
	t.Parallel()

	js := &stubJS{}
	store := &stubStore{failErr: errors.New("stash exploded")}
	p := bus.New(bus.Options{
		JS:             js,
		ObjectStore:    store,
		QueueSize:      4,
		Workers:        1,
		StashThreshold: 16,
		Logger:         discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop(time.Second)

	big := make([]byte, 64)
	p.Publish(newEnv("big-err", big))

	got := waitForStats(t, p, bus.Stats{Dropped: 1})
	if got.Published != 0 {
		t.Fatalf("Published = %d, want 0 on stash failure", got.Published)
	}
	if len(js.snapshot()) != 0 {
		t.Fatalf("expected no jetstream publish on stash failure")
	}
}

func TestPublisher_StopDrainsPending(t *testing.T) {
	t.Parallel()

	js := &stubJS{}
	p := bus.New(bus.Options{
		JS:        js,
		QueueSize: 32,
		Workers:   2,
		Logger:    discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)

	for i := 0; i < 10; i++ {
		p.Publish(newEnv("drain", []byte(`d`)))
	}

	if !p.Stop(2 * time.Second) {
		t.Fatal("Stop timed out")
	}
	stats := p.Stats()
	if stats.Published+stats.Dropped != 10 {
		t.Fatalf("Published+Dropped = %d, want 10", stats.Published+stats.Dropped)
	}
}

func TestPublisher_StopIdempotent(t *testing.T) {
	t.Parallel()

	js := &stubJS{}
	p := bus.New(bus.Options{
		JS:        js,
		QueueSize: 4,
		Workers:   1,
		Logger:    discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)

	if !p.Stop(time.Second) {
		t.Fatal("first stop timed out")
	}
	if !p.Stop(time.Second) {
		t.Fatal("second stop should be a no-op")
	}
}

func TestPublisher_CtxCancelStops(t *testing.T) {
	t.Parallel()

	js := &stubJS{}
	p := bus.New(bus.Options{
		JS:        js,
		QueueSize: 4,
		Workers:   2,
		Logger:    discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)
	cancel()
	if !p.Stop(time.Second) {
		t.Fatal("workers did not exit after ctx cancel")
	}
}

func TestPublisher_DefaultsAppliedOnZeroOptions(t *testing.T) {
	t.Parallel()

	js := &stubJS{}
	p := bus.New(bus.Options{JS: js})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop(time.Second)

	p.Publish(newEnv("default", []byte(`{}`)))
	waitForStats(t, p, bus.Stats{Published: 1})
}

func TestPublisher_PublishAssignsSchemaVersion(t *testing.T) {
	t.Parallel()

	js := &stubJS{}
	p := bus.New(bus.Options{JS: js, Workers: 1, Logger: discardLogger()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop(time.Second)

	p.Publish(bus.Envelope{
		EventID:       "no-version",
		EventType:     "request",
		Timestamp:     time.Now().UTC(),
		Mode:          bus.PayloadInline,
		InlinePayload: []byte(`{}`),
	})

	waitForStats(t, p, bus.Stats{Published: 1})
	msgs := js.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("publish count = %d, want 1", len(msgs))
	}
	var got bus.Envelope
	if err := msgpack.Unmarshal(msgs[0].Data, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SchemaVersion != bus.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, bus.SchemaVersion)
	}
}

func TestPublisher_StatsReflectAtomicState(t *testing.T) {
	t.Parallel()

	// Smoke test: counters never go negative and Publish is goroutine-safe.
	js := &stubJS{}
	p := bus.New(bus.Options{
		JS:        js,
		QueueSize: 256,
		Workers:   4,
		Logger:    discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop(2 * time.Second)

	var wg sync.WaitGroup
	const N = 200
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Publish(newEnv("race", []byte(`r`)))
		}()
	}
	wg.Wait()

	deadline := time.Now().Add(time.Second)
	var counted atomic.Bool
	for time.Now().Before(deadline) {
		s := p.Stats()
		if s.Published+s.Dropped == N {
			counted.Store(true)
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !counted.Load() {
		s := p.Stats()
		t.Fatalf("counters did not settle: Published=%d Dropped=%d (want sum=%d)", s.Published, s.Dropped, N)
	}
}

func TestPublisher_StopTimesOutWhenWorkerHung(t *testing.T) {
	t.Parallel()

	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })

	js := &stubJS{
		publishFn: func(_ context.Context, _ string, _ []byte) (*jetstream.PubAck, error) {
			<-hold
			return &jetstream.PubAck{}, nil
		},
	}
	p := bus.New(bus.Options{
		JS:        js,
		QueueSize: 4,
		Workers:   1,
		Logger:    discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)

	p.Publish(newEnv("hang", []byte(`x`)))
	for p.QueueLen() > 0 {
		time.Sleep(time.Millisecond)
	}

	if p.Stop(50 * time.Millisecond) {
		t.Fatal("Stop returned true while worker is hung; expected timeout")
	}
}

func TestPublisher_StashEmptyEventIDFails(t *testing.T) {
	t.Parallel()

	js := &stubJS{}
	store := &stubStore{}
	p := bus.New(bus.Options{
		JS:             js,
		ObjectStore:    store,
		QueueSize:      4,
		Workers:        1,
		StashThreshold: 8,
		Logger:         discardLogger(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop(time.Second)

	big := make([]byte, 64)
	p.Publish(bus.Envelope{
		EventType:     "request",
		Timestamp:     time.Now().UTC(),
		Mode:          bus.PayloadInline,
		InlinePayload: big,
	})

	got := waitForStats(t, p, bus.Stats{Dropped: 1})
	if got.Published != 0 {
		t.Fatalf("Published = %d, want 0 on stash with empty event id", got.Published)
	}
	if len(store.snapshot()) != 0 {
		t.Fatalf("PutBytes called despite empty event id")
	}
}

// Ensure the stub satisfies the publisher's interface at compile time.
var _ bus.JetStreamPublisher = (*stubJS)(nil)
var _ bus.ObjectPutter = (*stubStore)(nil)
