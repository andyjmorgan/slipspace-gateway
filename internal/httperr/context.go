package httperr

import (
	"context"
	"net/http"
)

// ctxKey is the unexported context key type for the per-request Writer.
type ctxKey struct{}

// passive is the counter-less, logger-less Writer FromContext falls back to
// when no Writer was installed. It still produces the documented JSON body
// shape, so a middleware driven outside the full chain (unit tests, ad-hoc
// wiring) never regresses to text/plain; only the metric increment is lost.
var passive = New(nil, nil)

// WithWriter returns ctx carrying wr so downstream middleware can reject a
// request through the same instrumented JSON writer the handler chain was
// built with. A nil wr leaves ctx unchanged.
func WithWriter(ctx context.Context, wr *Writer) context.Context {
	if wr == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, wr)
}

// FromContext returns the Writer installed by WithWriter, or a passive
// (uninstrumented) Writer when none is present. It never returns nil, so
// callers can write `httperr.FromContext(ctx).Write(...)` unguarded.
func FromContext(ctx context.Context) *Writer {
	if wr, ok := ctx.Value(ctxKey{}).(*Writer); ok && wr != nil {
		return wr
	}
	return passive
}

// Handler installs wr on every request's context before calling next, so
// stages deeper in the chain that were not constructed with the Writer
// (the rules and resilience middleware) still emit instrumented JSON errors
// (docs/pipeline.md, "Error responses"). A nil wr returns next unchanged.
func Handler(wr *Writer, next http.Handler) http.Handler {
	if wr == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithWriter(r.Context(), wr)))
	})
}
