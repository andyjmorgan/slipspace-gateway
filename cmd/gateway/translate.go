package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
	"github.com/andyjmorgan/sluice-gateway/internal/translate"
)

// errNoTranslator is returned when an active translation has no registered
// translator. The final handler fails closed before reaching here, so this is
// a defensive guard.
var errNoTranslator = errors.New("translate: no translator for protocol pair")

// lossyHeaderName is the response header listing source features dropped during
// cross-provider translation. Emitted only when the lossy-header flag is on.
const lossyHeaderName = "X-Sluice-Translation-Lossy"

// dropSink accumulates translation Drops across the request and response legs
// of one request so they can be counted and (optionally) reported in a single
// place. One per request; not used across goroutines.
type dropSink struct {
	drops []translate.Drop
}

func (d *dropSink) add(drops []translate.Drop) {
	if d != nil && len(drops) > 0 {
		d.drops = append(d.drops, drops...)
	}
}

type dropSinkKey struct{}

// withDropSink attaches a fresh dropSink to ctx and returns both. Installed by
// the final handler when translation is active so both the request-leg
// translation and the response-phase transform append to the same sink.
func withDropSink(ctx context.Context) context.Context {
	return context.WithValue(ctx, dropSinkKey{}, &dropSink{})
}

func dropSinkFromContext(ctx context.Context) *dropSink {
	s, _ := ctx.Value(dropSinkKey{}).(*dropSink)
	return s
}

// translationActive reports whether a translate rule retargeted the upstream
// protocol away from the inbound protocol for this request. SourceProtocol is
// set (and differs from the post-rule Protocol) only when a TranslateAction
// ran; a translate whose target equals the source is a no-op.
func translationActive(state *rules.MutableState) bool {
	return state != nil && state.SourceProtocol != "" && state.SourceProtocol != state.Protocol
}

// translatorRegistered reports whether a translator exists for an active
// translation's (source, target) protocol pair. The final handler fails closed
// when translation is active but this returns false — an undeclared or
// unsupported protocol pair must never forward silently (decision #3).
func translatorRegistered(state *rules.MutableState) bool {
	_, ok := translate.Lookup(state.SourceProtocol, state.Protocol)
	return ok
}

// streamCapable reports whether the registered translator for an active
// translation can translate streaming responses.
func streamCapable(state *rules.MutableState) bool {
	tr, ok := translate.Lookup(state.SourceProtocol, state.Protocol)
	if !ok {
		return false
	}
	_, ok = tr.(translate.StreamCapable)
	return ok
}

// translateRequestBody rewrites the outgoing request body from the source
// protocol into the target protocol when translation is active. It runs in the
// final handler, which the resilience orchestrator re-enters per attempt after
// BodyRemarshal has re-encoded any rule body mutations, so it always translates
// the final per-attempt body. Dropped source features are recorded on the
// request's drop sink.
//
// It returns streaming=true when the translated request asks for SSE; the
// caller rejects such requests (501) for non-stream-capable pairs.
func translateRequestBody(state *rules.MutableState, r *http.Request) (streaming bool, err error) {
	if !translationActive(state) {
		return false, nil
	}
	tr, ok := translate.Lookup(state.SourceProtocol, state.Protocol)
	if !ok {
		return false, errNoTranslator
	}

	var in []byte
	if r.Body != nil {
		in, err = io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return false, err
		}
	}

	out, drops, err := tr.TranslateRequest(in)
	if err != nil {
		return false, err
	}
	dropSinkFromContext(r.Context()).add(drops)
	setRequestBody(r, out)
	return bodyWantsStream(out), nil
}

// translateResponseBody rewrites an upstream response body from the target
// protocol back into the source protocol when translation is active, running
// from the forwarder's ModifyResponse hook (decision #2). Non-streaming bodies
// are buffered, translated, and replaced (dropped features recorded on the
// sink). Streaming bodies are wrapped in a pull-based translating reader.
func translateResponseBody(ctx context.Context, resp *http.Response, streaming bool) error {
	state := rules.MutableStateFromContext(ctx)
	if !translationActive(state) {
		return nil
	}
	tr, ok := translate.Lookup(state.SourceProtocol, state.Protocol)
	if !ok {
		return nil
	}

	// Error responses (>=400) carry an error body, not a success envelope or
	// SSE — and an upstream error to a streaming request comes back as a JSON
	// error (Content-Type not event-stream), so this branch handles both. The
	// forwarder preserves the status code; only the body shape is translated.
	if resp.StatusCode >= 400 {
		et, ok := tr.(translate.ErrorTranslator)
		if !ok {
			return nil
		}
		in, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		out, err := et.TranslateError(resp.StatusCode, in)
		if err != nil {
			return err
		}
		resp.Body = io.NopCloser(bytes.NewReader(out))
		resp.ContentLength = int64(len(out))
		resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
		resp.Header.Set("Content-Type", "application/json")
		return nil
	}

	if streaming {
		sc, ok := tr.(translate.StreamCapable)
		if !ok {
			// Not stream-capable: the request-time guard should have rejected
			// this, so leave the body untranslated rather than buffer a stream.
			return nil
		}
		resp.Body = translate.NewStreamingReader(resp.Body, sc.NewStreamTranslator())
		resp.ContentLength = -1
		resp.Header.Del("Content-Length")
		resp.Header.Set("Content-Type", "text/event-stream")
		return nil
	}

	in, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	out, drops, err := tr.TranslateResponse(in)
	if err != nil {
		return err
	}
	dropSinkFromContext(ctx).add(drops)

	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return nil
}

// newResponseBodyTransform builds the forwarder's ModifyResponse transform:
// response-phase rule rewrites first (authored against the upstream shape),
// then cross-provider translation back to the client's protocol, then
// finalisation of any dropped features (always-on counter + flag-gated header).
// Shared by main and the integration tests so both exercise the same path.
func newResponseBodyTransform(meters *observability.Meters, externalURL string, lossyHeader bool) proxy.ResponseBodyTransformer {
	return func(ctx context.Context, resp *http.Response, streaming bool) error {
		if err := rules.ApplyResponseRewrites(ctx, meters, externalURL, resp, streaming); err != nil {
			return err
		}
		if err := translateResponseBody(ctx, resp, streaming); err != nil {
			return err
		}
		finalizeTranslationDrops(ctx, meters, resp, lossyHeader)
		return nil
	}
}

// finalizeTranslationDrops emits the always-on drop counter (one increment per
// dropped feature, labelled by source/target protocol and field) and, when the
// flag is on, the X-Sluice-Translation-Lossy response header. No-op when
// translation was inactive or nothing was dropped.
func finalizeTranslationDrops(ctx context.Context, meters *observability.Meters, resp *http.Response, lossyHeader bool) {
	state := rules.MutableStateFromContext(ctx)
	if !translationActive(state) {
		return
	}
	sink := dropSinkFromContext(ctx)
	if sink == nil || len(sink.drops) == 0 {
		return
	}
	if meters != nil && meters.TranslationFieldDropsTotal != nil {
		for _, d := range sink.drops {
			meters.TranslationFieldDropsTotal.Add(ctx, 1, metric.WithAttributes(
				attribute.String(observability.AttrSluiceTranslateSource, state.SourceProtocol),
				attribute.String(observability.AttrSluiceTranslateTarget, state.Protocol),
				attribute.String(observability.AttrSluiceTranslateField, d.Field),
			))
		}
	}
	if lossyHeader {
		resp.Header.Set(lossyHeaderName, uniqueFields(sink.drops))
	}
}

// uniqueFields returns the sorted, de-duplicated dropped-field paths joined by
// ", " for the lossy header.
func uniqueFields(drops []translate.Drop) string {
	seen := make(map[string]struct{}, len(drops))
	fields := make([]string, 0, len(drops))
	for _, d := range drops {
		if _, ok := seen[d.Field]; ok {
			continue
		}
		seen[d.Field] = struct{}{}
		fields = append(fields, d.Field)
	}
	sort.Strings(fields)
	return strings.Join(fields, ", ")
}

// setRequestBody replaces r's body with body and keeps ContentLength, the
// Content-Length header, and GetBody consistent so the forwarder (and any
// resilience retry that re-reads via GetBody) sees the new bytes.
func setRequestBody(r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
}

// bodyWantsStream reports whether a request body sets a top-level "stream":true.
// Protocol-agnostic: OpenAI Chat, Anthropic Messages, and OpenAI Responses all
// carry the streaming flag as a top-level boolean.
func bodyWantsStream(body []byte) bool {
	var probe struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &probe)
	return probe.Stream
}
