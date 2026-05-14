// Package guardrails is the gateway's DLP / content-safety seam. v1.0 ships a
// pure passthrough Middleware backed by NopInspector; v1.2+ swaps in real
// Inspector implementations (regex redaction, classifier blocks, etc.) without
// touching pipeline wiring.
package guardrails

import (
	"context"
	"net/http"

	"github.com/andyjmorgan/sluice-gateway/internal/pipeline"
)

// Inspector is the seam where v1.2+ DLP engines plug in. Implementations may
// transform the message, leave it untouched, or return an error to terminate
// the pipeline.
type Inspector interface {
	Inspect(ctx context.Context, msg pipeline.Message) (pipeline.Message, error)
}

// NopInspector implements Inspector as a strict passthrough.
type NopInspector struct{}

// Inspect returns msg unchanged.
func (NopInspector) Inspect(_ context.Context, msg pipeline.Message) (pipeline.Message, error) {
	return msg, nil
}

// Middleware returns a pipeline.Middleware that runs each message through the
// supplied Inspector. An Inspector error is converted to a terminal
// pipeline.Error and the goroutine exits.
func Middleware(ctx context.Context, inspector Inspector) pipeline.Middleware {
	return func(in <-chan pipeline.Message) <-chan pipeline.Message {
		out := make(chan pipeline.Message)
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
					result, err := inspector.Inspect(ctx, msg)
					if err != nil {
						select {
						case <-ctx.Done():
						case out <- pipeline.Error{StatusCode: http.StatusInternalServerError, Err: err}:
						}
						return
					}
					select {
					case <-ctx.Done():
						return
					case out <- result:
					}
				}
			}
		}()
		return out
	}
}
