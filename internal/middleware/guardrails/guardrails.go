// Package guardrails is the gateway's DLP / content-safety seam. v1.0 ships a
// pure passthrough Middleware backed by NopInspector; v1.2+ swaps in real
// Inspector implementations (regex redaction, classifier blocks, etc.) without
// touching pipeline wiring.
package guardrails

import (
	"context"
	"net/http"

	"github.com/andyjmorgan/sluice-gateway/internal/pipeline"
	"github.com/andyjmorgan/sluice-gateway/internal/safego"
)

// Inspector is the seam where v1.2+ DLP engines plug in. Implementations
// may transform the message, leave it untouched, or return an error to
// terminate the pipeline.
//
// Inspect is invoked once per pipeline.Message; implementations must be
// safe for concurrent use across requests since one Inspector instance is
// shared by all goroutines.
type Inspector interface {
	Inspect(ctx context.Context, msg pipeline.Message) (pipeline.Message, error)
}

// NopInspector is the v1.0 default Inspector: it forwards every message
// unchanged. It exists so the pipeline wiring can include a guardrails
// stage today without committing to a particular DLP engine.
type NopInspector struct{}

// Inspect returns msg unchanged.
func (NopInspector) Inspect(_ context.Context, msg pipeline.Message) (pipeline.Message, error) {
	return msg, nil
}

// Middleware returns a pipeline.Middleware that runs each message through
// inspector.Inspect.
//
// On Inspector error the middleware emits a terminal pipeline.Error
// (StatusCode 500) and shuts down — guardrail failures are treated as
// internal, since the DLP engine itself is a gateway concern and not a
// client-facing one.
func Middleware(ctx context.Context, inspector Inspector) pipeline.Middleware {
	return func(in <-chan pipeline.Message) <-chan pipeline.Message {
		out := make(chan pipeline.Message)
		safego.Go(ctx, "guardrails.inspect", nil, nil, func() {
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
		})
		return out
	}
}
