package pipeline_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/internal/pipeline"
)

func collect(t *testing.T, ch <-chan pipeline.Message, want int, timeout time.Duration) []pipeline.Message {
	t.Helper()

	got := make([]pipeline.Message, 0, want)
	deadline := time.After(timeout)
	for len(got) < want {
		select {
		case msg, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, msg)
		case <-deadline:
			t.Fatalf("timed out waiting for messages: got %d, want %d", len(got), want)
		}
	}
	return got
}

func drain(t *testing.T, ch <-chan pipeline.Message) []pipeline.Message {
	t.Helper()

	got := []pipeline.Message{}
	deadline := time.After(time.Second)
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, msg)
		case <-deadline:
			t.Fatalf("timed out draining channel after %d messages", len(got))
		}
	}
}

func recordingMW(label string, log *[]string, mu *sync.Mutex) pipeline.Middleware {
	return func(in <-chan pipeline.Message) <-chan pipeline.Message {
		out := make(chan pipeline.Message)
		go func() {
			defer close(out)
			for msg := range in {
				mu.Lock()
				*log = append(*log, label)
				mu.Unlock()
				out <- msg
			}
		}()
		return out
	}
}

func TestChain_EmptyIsIdentity(t *testing.T) {
	t.Parallel()

	in := pipeline.Source(pipeline.Complete{})
	chained := pipeline.Chain()
	out := chained(in)

	got := drain(t, out)
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if _, ok := got[0].(pipeline.Complete); !ok {
		t.Fatalf("got %T, want Complete", got[0])
	}
}

func TestChain_ExecutesInOrder(t *testing.T) {
	t.Parallel()

	var (
		log []string
		mu  sync.Mutex
	)

	chained := pipeline.Chain(
		recordingMW("a", &log, &mu),
		recordingMW("b", &log, &mu),
		recordingMW("c", &log, &mu),
	)

	out := chained(pipeline.Source(pipeline.JSONResponse{Body: []byte("1")}))

	got := drain(t, out)
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}

	mu.Lock()
	defer mu.Unlock()

	want := []string{"a", "b", "c"}
	if len(log) != len(want) {
		t.Fatalf("log = %v, want %v", log, want)
	}
	for i, label := range want {
		if log[i] != label {
			t.Fatalf("log[%d] = %s, want %s (full=%v)", i, log[i], label, log)
		}
	}
}

func TestChain_EachMiddlewareSeesEveryMessage(t *testing.T) {
	t.Parallel()

	var (
		log []string
		mu  sync.Mutex
	)

	chained := pipeline.Chain(
		recordingMW("a", &log, &mu),
		recordingMW("b", &log, &mu),
		recordingMW("c", &log, &mu),
	)

	out := chained(pipeline.Source(
		pipeline.JSONResponse{Body: []byte("1")},
		pipeline.JSONResponse{Body: []byte("2")},
		pipeline.JSONResponse{Body: []byte("3")},
	))

	got := drain(t, out)
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}

	mu.Lock()
	defer mu.Unlock()

	counts := map[string]int{}
	for _, label := range log {
		counts[label]++
	}
	for _, label := range []string{"a", "b", "c"} {
		if counts[label] != 3 {
			t.Errorf("middleware %s recorded %d messages, want 3 (full log=%v)", label, counts[label], log)
		}
	}
}

func TestChain_ErrorPropagatesUnchanged(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("upstream failed")

	chained := pipeline.Chain(
		passthrough(),
		passthrough(),
	)

	out := chained(pipeline.Source(
		pipeline.Error{StatusCode: 502, Err: sentinel},
		pipeline.Complete{},
	))

	got := drain(t, out)
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}

	errMsg, ok := got[0].(pipeline.Error)
	if !ok {
		t.Fatalf("got[0] = %T, want pipeline.Error", got[0])
	}
	if errMsg.StatusCode != 502 {
		t.Fatalf("StatusCode = %d, want 502", errMsg.StatusCode)
	}
	if !errors.Is(errMsg.Err, sentinel) {
		t.Fatalf("Err = %v, want %v", errMsg.Err, sentinel)
	}
	if _, ok := got[1].(pipeline.Complete); !ok {
		t.Fatalf("got[1] = %T, want Complete", got[1])
	}
}

func passthrough() pipeline.Middleware {
	return func(in <-chan pipeline.Message) <-chan pipeline.Message {
		out := make(chan pipeline.Message)
		go func() {
			defer close(out)
			for msg := range in {
				out <- msg
			}
		}()
		return out
	}
}

func TestPass_ForwardsMessages(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := pipeline.Source(
		pipeline.JSONResponse{Body: []byte("a")},
		pipeline.JSONResponse{Body: []byte("b")},
		pipeline.Complete{},
	)

	out := pipeline.Pass(ctx)(in)

	got := drain(t, out)
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}
}

func TestPass_ClosesOutputWhenInputCloses(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan pipeline.Message)
	out := pipeline.Pass(ctx)(in)

	close(in)

	select {
	case _, ok := <-out:
		if ok {
			t.Fatalf("expected closed output, got message")
		}
	case <-time.After(time.Second):
		t.Fatal("output channel was not closed after input closed")
	}
}

func TestPass_CancelExitsGoroutineWhileWaitingOnInput(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	in := make(chan pipeline.Message)
	out := pipeline.Pass(ctx)(in)

	cancel()

	select {
	case _, ok := <-out:
		if ok {
			t.Fatalf("expected closed output, got message")
		}
	case <-time.After(time.Second):
		t.Fatal("output channel was not closed after ctx cancel")
	}

	close(in)
}

func TestPass_CancelExitsGoroutineWhileBlockedOnSend(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	in := make(chan pipeline.Message, 1)
	in <- pipeline.JSONResponse{Body: []byte("x")}

	out := pipeline.Pass(ctx)(in)

	cancel()

	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-out:
			if !ok {
				close(in)
				return
			}
		case <-deadline:
			t.Fatal("output channel was not closed after ctx cancel mid-send")
		}
	}
}

func TestChain_ContextCancellationCleansUpGoroutines(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	chained := pipeline.Chain(
		pipeline.Pass(ctx),
		pipeline.Pass(ctx),
		pipeline.Pass(ctx),
	)

	in := make(chan pipeline.Message)
	out := chained(in)

	cancel()

	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("expected closed output after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("output channel did not close after ctx cancel")
	}

	close(in)
}

func TestSource_YieldsInOrderAndCloses(t *testing.T) {
	t.Parallel()

	out := pipeline.Source(
		pipeline.JSONResponse{Body: []byte("1")},
		pipeline.JSONResponse{Body: []byte("2")},
		pipeline.JSONResponse{Body: []byte("3")},
	)

	got := collect(t, out, 3, time.Second)
	for i, m := range got {
		j, ok := m.(pipeline.JSONResponse)
		if !ok {
			t.Fatalf("got[%d] = %T, want JSONResponse", i, m)
		}
		want := []byte{byte('1' + i)}
		if string(j.Body) != string(want) {
			t.Fatalf("got[%d] body = %s, want %s", i, j.Body, want)
		}
	}

	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("Source channel still open after draining")
		}
	default:
	}
}

func TestSource_NoArgsReturnsClosedChannel(t *testing.T) {
	t.Parallel()

	out := pipeline.Source()
	_, ok := <-out
	if ok {
		t.Fatal("expected closed empty channel")
	}
}

func TestChain_AppliesMiddlewareThatTransforms(t *testing.T) {
	t.Parallel()

	var seen atomic.Int64
	counter := func(in <-chan pipeline.Message) <-chan pipeline.Message {
		out := make(chan pipeline.Message)
		go func() {
			defer close(out)
			for msg := range in {
				seen.Add(1)
				out <- msg
			}
		}()
		return out
	}

	chained := pipeline.Chain(counter)
	out := chained(pipeline.Source(
		pipeline.JSONResponse{Body: []byte("a")},
		pipeline.JSONResponse{Body: []byte("b")},
	))

	got := drain(t, out)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if seen.Load() != 2 {
		t.Fatalf("counter saw %d messages, want 2", seen.Load())
	}
}
