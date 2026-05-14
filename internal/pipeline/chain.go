package pipeline

import "context"

// Middleware reads messages from an input channel and returns a downstream
// channel that yields the (optionally transformed) output stream.
type Middleware func(in <-chan Message) <-chan Message

// Chain composes the given middlewares into a single Middleware. The first
// middleware sees the original input; each subsequent middleware reads from
// the output of the previous one.
func Chain(mws ...Middleware) Middleware {
	return func(in <-chan Message) <-chan Message {
		for _, mw := range mws {
			in = mw(in)
		}
		return in
	}
}

// Pass returns a Middleware that owns one goroutine and forwards every
// received message downstream without modification. The goroutine exits when
// the input channel closes or ctx is cancelled, at which point the output
// channel is closed.
func Pass(ctx context.Context) Middleware {
	return func(in <-chan Message) <-chan Message {
		out := make(chan Message)
		go func() {
			defer close(out)
			for {
				select {
				case <-ctx.Done():
					return
				case msg, ok := <-in:
					if !ok {
						return
					}
					select {
					case <-ctx.Done():
						return
					case out <- msg:
					}
				}
			}
		}()
		return out
	}
}

// Source returns a channel that yields the given messages in order and then
// closes. Useful for seeding a pipeline in tests.
func Source(msgs ...Message) <-chan Message {
	out := make(chan Message, len(msgs))
	for _, m := range msgs {
		out <- m
	}
	close(out)
	return out
}
