package rules

import (
	"context"
	"io"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/andyjmorgan/sluice-gateway/internal/bodypatch"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// BodyRewriteHandler applies the body-patch operations the rules engine
// queued (rewriteField / removeField / appendField) to the serialized
// request body, after the typed re-marshal step has run.
//
// Placement: innermost, just before the forwarder, so it patches the
// final bytes — either the typed re-marshal output (when a typed action
// like changeModelName ran) or the verbatim inbound bytes. It reads the
// current r.Body, applies the patches via gjson/sjson, and replaces
// r.Body + Content-Length.
//
// No-op when no rewrites were queued — the common path reads the state,
// finds an empty slice, and falls through with zero allocation.
func BodyRewriteHandler(meters *observability.Meters, next http.Handler) http.Handler {
	if next == nil {
		panic("rules: BodyRewriteHandler called with nil next handler")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		state := MutableStateFromContext(ctx)
		if state == nil || len(state.BodyRewrites) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		body, err := readRequestBody(r)
		if err != nil {
			logger := observability.FromContext(ctx)
			logger.ErrorContext(ctx, "rules: body rewrite read", "err", err.Error())
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		refs := bodypatch.Refs{
			PathParams: state.PathParams,
			Provider:   state.Provider,
			Endpoint:   state.Endpoint,
		}
		patched, results := bodypatch.Apply(body, state.BodyRewrites, refs)
		recordRewriteResults(ctx, meters, results)
		bodycapture.ApplyBodyBytes(r, patched)
		next.ServeHTTP(w, r)
	})
}

// readRequestBody buffers the current outgoing body. By the time this
// handler runs the body is a reader over in-memory bytes (set by
// bodycapture or the re-marshal step), so the read is cheap and cannot
// block on the network.
func readRequestBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return []byte{}, nil
	}
	defer func() { _ = r.Body.Close() }()
	return io.ReadAll(r.Body)
}

// recordRewriteResults fans the per-op outcomes onto the applied/
// dropped counters. Label cardinality is bounded by the action set and
// the small drop-reason taxonomy — never client-derived.
func recordRewriteResults(ctx context.Context, meters *observability.Meters, results []bodypatch.Result) {
	if meters == nil {
		return
	}
	for _, res := range results {
		if res.Applied {
			if meters.RewriteAppliedTotal != nil {
				meters.RewriteAppliedTotal.Add(ctx, 1, metric.WithAttributes(
					attribute.String("action_type", res.ActionType),
				))
			}
			continue
		}
		if meters.RewriteDroppedTotal != nil {
			meters.RewriteDroppedTotal.Add(ctx, 1, metric.WithAttributes(
				attribute.String("action_type", res.ActionType),
				attribute.String("reason", res.Reason),
			))
		}
	}
}
