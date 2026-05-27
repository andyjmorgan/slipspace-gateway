package rules

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"

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

// ApplyResponseRewrites applies the response-phase body patches the
// rules engine queued (response.body.* targets) to an upstream
// response, in place. It is the response-side counterpart to
// BodyRewriteHandler, invoked from the proxy's ModifyResponse hook via
// a thin adapter in cmd/gateway (so internal/proxy stays decoupled from
// the rules engine).
//
// Streaming responses (server-sent events) are never mutated — the
// queued rewrites are dropped with a structured warn and counted,
// because SSE chunks don't form one JSON document. For a non-streaming
// response the body is buffered, patched, and re-installed with a fixed
// Content-Length. externalURL resolves {external_url} template refs;
// the request body snapshot (for {request.body.x} refs) is read from
// the captured request on ctx.
//
// Returns an error only when the response body cannot be read — the
// proxy turns that into a 502. Patch-level failures are per-op drops,
// never fatal.
func ApplyResponseRewrites(ctx context.Context, meters *observability.Meters, externalURL string, resp *http.Response, streaming bool) error {
	state := MutableStateFromContext(ctx)
	if state == nil || len(state.ResponseRewrites) == 0 {
		return nil
	}

	if streaming {
		recordResponseRewriteDrops(ctx, meters, state.ResponseRewrites, bodypatch.ReasonStreamingResponse)
		observability.FromContext(ctx).WarnContext(ctx,
			"rules: response rewrites dropped on streaming response",
			"count", len(state.ResponseRewrites),
		)
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if cerr := resp.Body.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("rules: response rewrite read: %w", err)
	}

	var reqBody []byte
	if c, ok := bodycapture.FromContext(ctx); ok {
		reqBody = c.Raw
	}
	refs := bodypatch.Refs{
		Phase:       bodypatch.PhaseResponse,
		ExternalURL: externalURL,
		RequestBody: reqBody,
		Provider:    state.Provider,
		Endpoint:    state.Endpoint,
		PathParams:  state.PathParams,
	}
	patched, results := bodypatch.Apply(body, state.ResponseRewrites, refs)
	recordRewriteResults(ctx, meters, results)

	resp.Body = io.NopCloser(bytes.NewReader(patched))
	resp.ContentLength = int64(len(patched))
	resp.Header.Set("Content-Length", strconv.Itoa(len(patched)))
	return nil
}

// recordResponseRewriteDrops emits the dropped counter for a slice of
// ops that were skipped wholesale for a single reason (e.g. a streaming
// response), where no per-op Apply ran.
func recordResponseRewriteDrops(ctx context.Context, meters *observability.Meters, ops []bodypatch.Op, reason string) {
	if meters == nil || meters.RewriteDroppedTotal == nil {
		return
	}
	for _, op := range ops {
		meters.RewriteDroppedTotal.Add(ctx, 1, metric.WithAttributes(
			attribute.String("action_type", op.ActionType),
			attribute.String("reason", reason),
		))
	}
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
