package guardrails_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/guardrails"
	"github.com/andyjmorgan/slipspace-gateway/internal/pipeline"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestNopInspector_Identity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  pipeline.Message
	}{
		{"response_initial", pipeline.ResponseInitial{StatusCode: 200, Streaming: true}},
		{"stream_chunk", pipeline.StreamChunk{Data: []byte("hello"), Event: "message"}},
		{"json_response", pipeline.JSONResponse{Body: []byte(`{"ok":true}`)}},
		{"error", pipeline.Error{StatusCode: 502, Err: errors.New("boom")}},
		{"complete", pipeline.Complete{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := guardrails.NopInspector{}.Inspect(context.Background(), tc.msg)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if !reflect.DeepEqual(got, tc.msg) {
				t.Fatalf("got = %#v, want %#v", got, tc.msg)
			}
		})
	}
}

func TestNopInspector_SatisfiesInterface(t *testing.T) {
	t.Parallel()
	var _ guardrails.Inspector = guardrails.NopInspector{}
}

func TestMiddleware_PassthroughOrdered(t *testing.T) {
	t.Parallel()

	in := []pipeline.Message{
		pipeline.ResponseInitial{StatusCode: 200, Streaming: true},
		pipeline.StreamChunk{Data: []byte("one"), ID: "1"},
		pipeline.StreamChunk{Data: []byte("two"), ID: "2"},
		pipeline.Complete{},
	}

	mw := guardrails.Middleware(context.Background(), guardrails.NopInspector{})
	out := mw(pipeline.Source(in...))

	got := drain(t, out, len(in), time.Second)
	if len(got) != len(in) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(in))
	}
	for i := range in {
		if !reflect.DeepEqual(got[i], in[i]) {
			t.Errorf("msg[%d] = %#v, want %#v", i, got[i], in[i])
		}
	}
	expectClosed(t, out)
}

func TestMiddleware_EmptyInputClosesOutput(t *testing.T) {
	t.Parallel()

	mw := guardrails.Middleware(context.Background(), guardrails.NopInspector{})
	out := mw(pipeline.Source())

	expectClosed(t, out)
}

func TestMiddleware_ContextCancelMidStream(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	in := make(chan pipeline.Message)

	mw := guardrails.Middleware(ctx, guardrails.NopInspector{})
	out := mw(in)

	in <- pipeline.StreamChunk{Data: []byte("one")}
	select {
	case msg, ok := <-out:
		if !ok {
			t.Fatal("output closed before first message delivered")
		}
		if _, ok := msg.(pipeline.StreamChunk); !ok {
			t.Fatalf("got = %#v, want StreamChunk", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first message")
	}

	cancel()
	expectClosed(t, out)

	close(in)
}

func TestMiddleware_ContextAlreadyCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	in := make(chan pipeline.Message)
	defer close(in)

	mw := guardrails.Middleware(ctx, guardrails.NopInspector{})
	out := mw(in)

	expectClosed(t, out)
}

type stubInspector struct {
	count int
	errAt int
	err   error
}

func (s *stubInspector) Inspect(_ context.Context, msg pipeline.Message) (pipeline.Message, error) {
	s.count++
	if s.count == s.errAt {
		return nil, s.err
	}
	return msg, nil
}

func TestMiddleware_InspectorErrorReplacesMessage(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("dlp violation")
	stub := &stubInspector{errAt: 2, err: sentinel}

	in := []pipeline.Message{
		pipeline.StreamChunk{Data: []byte("first")},
		pipeline.StreamChunk{Data: []byte("second")},
		pipeline.StreamChunk{Data: []byte("third")},
	}

	mw := guardrails.Middleware(context.Background(), stub)
	out := mw(pipeline.Source(in...))

	first := receive(t, out, time.Second)
	chunk, ok := first.(pipeline.StreamChunk)
	if !ok {
		t.Fatalf("first message = %#v, want StreamChunk", first)
	}
	if string(chunk.Data) != "first" {
		t.Errorf("first chunk data = %q, want %q", chunk.Data, "first")
	}

	second := receive(t, out, time.Second)
	errMsg, ok := second.(pipeline.Error)
	if !ok {
		t.Fatalf("second message = %#v, want pipeline.Error", second)
	}
	if !errors.Is(errMsg.Err, sentinel) {
		t.Errorf("err = %v, want %v", errMsg.Err, sentinel)
	}
	if errMsg.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", errMsg.StatusCode, http.StatusInternalServerError)
	}

	expectClosed(t, out)

	if stub.count != 2 {
		t.Errorf("inspector calls = %d, want 2 (third message must not be inspected after error)", stub.count)
	}
}

func TestMiddleware_InspectorErrorRespectsCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	stub := &cancelOnInspect{cancel: cancel, err: errors.New("dlp")}

	in := pipeline.Source(pipeline.StreamChunk{Data: []byte("payload")})
	mw := guardrails.Middleware(ctx, stub)
	out := mw(in)

	expectClosedAfter(t, out, 50*time.Millisecond)
}

func TestMiddleware_PassthroughRespectsCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	stub := &cancelOnInspect{cancel: cancel, err: nil}

	in := pipeline.Source(pipeline.StreamChunk{Data: []byte("payload")})
	mw := guardrails.Middleware(ctx, stub)
	out := mw(in)

	expectClosedAfter(t, out, 50*time.Millisecond)
}

type cancelOnInspect struct {
	cancel context.CancelFunc
	err    error
}

func (c *cancelOnInspect) Inspect(_ context.Context, _ pipeline.Message) (pipeline.Message, error) {
	c.cancel()
	if c.err != nil {
		return nil, c.err
	}
	return pipeline.Complete{}, nil
}

func expectClosedAfter(t *testing.T, ch <-chan pipeline.Message, settle time.Duration) {
	t.Helper()
	time.Sleep(settle)
	select {
	case msg, ok := <-ch:
		if ok {
			t.Fatalf("expected channel close, got %#v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel to close")
	}
}

func drain(t *testing.T, ch <-chan pipeline.Message, n int, timeout time.Duration) []pipeline.Message {
	t.Helper()
	out := make([]pipeline.Message, 0, n)
	deadline := time.After(timeout)
	for i := 0; i < n; i++ {
		select {
		case msg, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed after %d messages, want %d", len(out), n)
			}
			out = append(out, msg)
		case <-deadline:
			t.Fatalf("timed out after %d messages, want %d", len(out), n)
		}
	}
	return out
}

func receive(t *testing.T, ch <-chan pipeline.Message, timeout time.Duration) pipeline.Message {
	t.Helper()
	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
		return msg
	case <-time.After(timeout):
		t.Fatal("timed out waiting for message")
	}
	return nil
}

func expectClosed(t *testing.T, ch <-chan pipeline.Message) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for channel to close")
		}
	}
}
