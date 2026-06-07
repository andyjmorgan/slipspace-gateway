package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
	"github.com/andyjmorgan/sluice-gateway/internal/translate"
)

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
// unsupported protocol pair must never forward silently (decision #3,
// fail-closed at destination resolution).
func translatorRegistered(state *rules.MutableState) bool {
	_, ok := translate.Lookup(state.SourceProtocol, state.Protocol)
	return ok
}

// translateRequestBody rewrites the outgoing request body from the source
// protocol into the target protocol when translation is active. It runs in the
// final handler, which the resilience orchestrator re-enters per attempt after
// BodyRemarshal has re-encoded any rule body mutations, so it always translates
// the final per-attempt body.
//
// It returns streaming=true when the translated request asks for SSE. Streaming
// translation is not yet supported, so the caller rejects such requests (501)
// rather than forward a stream the response leg cannot translate back.
func translateRequestBody(state *rules.MutableState, r *http.Request) (streaming bool, err error) {
	if !translationActive(state) {
		return false, nil
	}
	tr, ok := translate.Lookup(state.SourceProtocol, state.Protocol)
	if !ok {
		return false, fmt.Errorf("translate: no translator for %s->%s", state.SourceProtocol, state.Protocol)
	}

	var in []byte
	if r.Body != nil {
		in, err = io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return false, fmt.Errorf("translate: read request body: %w", err)
		}
	}

	// Drops are surfaced to the lossy counter/header in a later PR; discarded
	// here so the request body translation lands independently.
	out, _, err := tr.TranslateRequest(in)
	if err != nil {
		return false, fmt.Errorf("translate request: %w", err)
	}

	setRequestBody(r, out)
	return bodyWantsStream(out), nil
}

// translateResponseBody rewrites a non-streaming upstream response body from the
// target protocol back into the source protocol when translation is active. It
// runs from the forwarder's ModifyResponse hook (decision #2). Streaming
// responses are a defensive no-op: an active+streaming request is rejected at
// request time, so the upstream is never contacted for one.
func translateResponseBody(ctx context.Context, resp *http.Response, streaming bool) error {
	state := rules.MutableStateFromContext(ctx)
	if !translationActive(state) || streaming {
		return nil
	}
	tr, ok := translate.Lookup(state.SourceProtocol, state.Protocol)
	if !ok {
		return nil
	}

	in, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return fmt.Errorf("translate: read response body: %w", err)
	}
	out, _, err := tr.TranslateResponse(in)
	if err != nil {
		return fmt.Errorf("translate response: %w", err)
	}

	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return nil
}

// newResponseBodyTransform builds the forwarder's ModifyResponse transform:
// response-phase rule rewrites first (authored against the upstream shape),
// then cross-provider translation back to the client's protocol. Shared by
// main and the integration tests so both exercise the same response path.
func newResponseBodyTransform(meters *observability.Meters, externalURL string) proxy.ResponseBodyTransformer {
	return func(ctx context.Context, resp *http.Response, streaming bool) error {
		if err := rules.ApplyResponseRewrites(ctx, meters, externalURL, resp, streaming); err != nil {
			return err
		}
		return translateResponseBody(ctx, resp, streaming)
	}
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
