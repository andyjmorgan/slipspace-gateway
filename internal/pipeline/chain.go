package pipeline

import (
	"context"

	"github.com/andyjmorgan/slipspace-gateway/internal/safego"
)

// Middleware is the unit of pipeline composition: it consumes a stream of
// Message values and returns the downstream stream. Implementations spawn a
// goroutine bound to a context for cancellation and must close the returned
// channel when their input drains or the context is done.
type Middleware func(in <-chan Message) <-chan Message

// Chain composes mws into a single Middleware by feeding each middleware's
// output into the next. The leftmost middleware reads the original input;
// the rightmost's output becomes the chain's output.
func Chain(mws ...Middleware) Middleware {
	return func(in <-chan Message) <-chan Message {
		for _, mw := range mws {
			in = mw(in)
		}
		return in
	}
}

// Pass is a Middleware that forwards every received Message downstream
// unchanged. It exists as the canonical no-op stage for tests and for
// slots in the chain that are wired but carry no behaviour; the channel
// pipeline as a whole is currently inert and is not attached to the
// request path.
//
// The goroutine exits when the input channel closes or ctx is cancelled,
// closing the output channel on the way out.
func Pass(ctx context.Context) Middleware {
	return func(in <-chan Message) <-chan Message {
		out := make(chan Message)
		safego.Go(ctx, "pipeline.pass", nil, nil, func() {
			defer close(out)
			for {
				select {
				case <-ctx.Done():
					return
				case msg, ok := <-in:
					if !ok {
						return
					}
					// Two-stage cancellation select: the outer
					// select already preempted a blocked read, but a
					// downstream consumer that stops reading would
					// strand us here on send. Re-checking ctx.Done()
					// on the send side guarantees we cannot leak the
					// goroutine when the request is cancelled
					// mid-forward.
					select {
					case <-ctx.Done():
						return
					case out <- msg:
					}
				}
			}
		})
		return out
	}
}

// Source returns a channel that yields msgs in order and then closes. It is a
// test helper for seeding a pipeline with a canned sequence of messages
// (e.g. a synthetic ResponseInitial + JSONResponse + Complete triple) without
// standing up the forwarder.
func Source(msgs ...Message) <-chan Message {
	out := make(chan Message, len(msgs))
	for _, m := range msgs {
		out <- m
	}
	close(out)
	return out
}
