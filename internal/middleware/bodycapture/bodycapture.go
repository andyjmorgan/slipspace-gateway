// Package bodycapture reads the inbound request body once, deserializes it
// into the typed provider struct selected by the resolved route's
// RequestKind, stashes the Captured on context for downstream middleware,
// and replaces r.Body so the forwarder can resend the original bytes.
//
// The middleware preserves DynamicProperties on the typed value so unknown
// provider fields survive the round-trip; downstream consumers must
// re-marshal the typed Body if they intend to mutate it on the wire.
package bodycapture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/andyjmorgan/slipspace-gateway/internal/headers"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
	"github.com/andyjmorgan/slipspace-gateway/protocols/gemini/content"
	openaichat "github.com/andyjmorgan/slipspace-gateway/protocols/openai/chat"
	openairesponses "github.com/andyjmorgan/slipspace-gateway/protocols/openai/responses"
)

// RequestKind enumerates the typed body shapes the middleware knows how to
// deserialize. The kind is supplied per request through the
// KindFromContextFunc seam passed to HTTPHandler, not from YAML.
type RequestKind string

// RequestKind values. Keep in sync with the mapping in
// cmd/gateway/pipeline.go::kindFromProtocol, which maps the path-derived
// protocol onto these values (embeddings and non-generative passthrough
// families fall to KindPassthrough). New providers add a new kind here plus a
// case in Capture.
const (
	KindPassthrough RequestKind = "passthrough"

	KindChat RequestKind = "chat"

	KindResponses RequestKind = "responses"

	KindMessages RequestKind = "messages"

	KindGenerateContent RequestKind = "generate_content"
)

// MaxBodyBytes caps the inbound body the middleware will buffer. The gateway
// holds the entire payload in memory to feed both the typed deserialiser and
// the downstream forwarder, so the cap protects against accidental OOM from
// a misbehaving client. 10 MiB comfortably covers vision payloads with a few
// base64-encoded images; raise it per-endpoint later if needed.
const MaxBodyBytes int64 = 10 * 1024 * 1024

// Captured is the result of reading and (optionally) decoding a request body.
// It lands on the request context after HTTPHandler runs and is read by
// downstream middleware (rules, forwarder) via FromContext.
type Captured struct {
	// Kind is the RequestKind the route declared — the dispatch key that
	// selected Body's concrete type.
	Kind RequestKind

	// Raw is the verbatim inbound body bytes, capped at MaxBodyBytes. The
	// forwarder reads from this (via r.Body, which HTTPHandler replaces
	// with a reader over Raw) so the upstream sees byte-for-byte what the
	// client sent.
	Raw []byte

	// Body is the typed deserialised value: one of
	// *openaichat.ChatCompletionRequest, *openairesponses.ResponsesRequest,
	// *messages.MessagesRequest, *content.GenerateContentRequest, or nil
	// for KindPassthrough. Type-switch on Body when consuming.
	//
	// DynamicProperties on each concrete type preserves unknown fields, so
	// re-marshalling Body (rather than Raw) is safe but not equivalent —
	// key ordering and whitespace differ.
	Body any

	// Headers is the inbound request header map, with credential-bearing
	// values replaced by "[REDACTED]" via internal/headers.Redactor.
	// Surfaced to operators through the live-feed body envelope so the
	// admin console can show what the client sent without leaking the
	// secret. Stored as the plain string→[]string shape rather than
	// http.Header to keep the body-store free of net/http types.
	Headers map[string][]string
}

// KindFromContextFunc returns the RequestKind the routing middleware stashed
// on the request context. ok is false when routing has not run or the
// matched route did not carry a kind.
//
// It exists as a dependency-injection point so bodycapture does not import
// internal/routing — routing depends on config + contracts and a direct
// import would create a cycle once routing's tests pull in bodycapture
// fixtures.
type KindFromContextFunc func(ctx context.Context) (RequestKind, bool)

// Capture reads r.Body up to MaxBodyBytes and, when kind is a typed shape,
// deserialises it into the matching provider struct via the package's
// DynamicProperties-aware UnmarshalJSON. The returned Captured carries the
// verbatim raw bytes so callers can resend them downstream. headers.Redact
// is applied to r.Header to populate Captured.Headers; pass nil for the
// default built-in substring list.
//
// Errors: ErrBodyTooLarge if the body exceeds MaxBodyBytes; ErrParse wrapped
// around the json error on malformed input; ErrUnknownKind on an unmodelled
// RequestKind.
func Capture(r *http.Request, kind RequestKind, redactor *headers.Redactor) (Captured, error) {
	raw, err := readBody(r)
	if err != nil {
		return Captured{}, err
	}

	hdrs := redactor.Redact(r.Header)

	if kind == KindPassthrough {
		return Captured{Kind: kind, Raw: raw, Body: nil, Headers: hdrs}, nil
	}

	target, err := allocate(kind)
	if err != nil {
		return Captured{}, err
	}

	if err := json.Unmarshal(raw, target); err != nil {
		return Captured{}, fmt.Errorf("%w: %w", ErrParse, err)
	}

	return Captured{Kind: kind, Raw: raw, Body: target, Headers: hdrs}, nil
}

// HTTPHandler wraps next with the body-capture step.
//
// It sits downstream of routing and auth in the request chain. It reads the
// route's RequestKind from context via kindFrom, calls Capture to buffer
// and (when typed) deserialise the body, stashes the Captured on the
// request context (read downstream via FromContext), and replaces r.Body
// with a reader over the captured raw bytes so the forwarder consumes
// identical content.
//
// On ErrBodyTooLarge the handler writes 413; on ErrParse it writes 400 and
// logs at warn level; on missing route or unknown kind it writes 500 —
// those are wiring bugs that should not reach clients silently.
func HTTPHandler(kindFrom KindFromContextFunc, redactor *headers.Redactor, next http.Handler) http.Handler {
	if kindFrom == nil {
		panic("bodycapture: HTTPHandler called with nil kindFrom")
	}
	if next == nil {
		panic("bodycapture: HTTPHandler called with nil next handler")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := observability.FromContext(ctx)

		kind, ok := kindFrom(ctx)
		if !ok {
			logger.ErrorContext(ctx, "bodycapture failed", "result", "no_kind")
			writeError(w, http.StatusInternalServerError, "no request kind on context")
			return
		}

		captured, err := Capture(r, kind, redactor)
		if err != nil {
			switch {
			case errors.Is(err, ErrBodyTooLarge):
				logger.WarnContext(ctx, "bodycapture rejected", "result", "body_too_large", "kind", string(kind))
				writeError(w, http.StatusRequestEntityTooLarge, "body too large")
			case errors.Is(err, ErrParse):
				logger.WarnContext(ctx, "bodycapture rejected",
					"result", "malformed_body",
					"kind", string(kind),
					"error", err.Error(),
				)
				writeError(w, http.StatusBadRequest, "malformed request body")
			case errors.Is(err, ErrUnknownKind):
				logger.ErrorContext(ctx, "bodycapture failed",
					"result", "unknown_kind",
					"kind", string(kind),
				)
				writeError(w, http.StatusInternalServerError, "unknown request kind")
			default:
				logger.ErrorContext(ctx, "bodycapture failed", "result", "internal", "error", err.Error())
				writeError(w, http.StatusInternalServerError, "internal error")
			}
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(captured.Raw))
		next.ServeHTTP(w, r.WithContext(withCaptured(ctx, captured)))
	})
}

func allocate(kind RequestKind) (any, error) {
	switch kind {
	case KindChat:
		return &openaichat.ChatCompletionRequest{}, nil
	case KindResponses:
		return &openairesponses.ResponsesRequest{}, nil
	case KindMessages:
		return &messages.MessagesRequest{}, nil
	case KindGenerateContent:
		return &content.GenerateContentRequest{}, nil
	case KindPassthrough:
		return nil, ErrUnknownKind
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, string(kind))
	}
}

// Model returns the model identifier carried on a decoded typed
// request body, or the empty string if body is nil, of an
// unmodelled type, or of a kind that does not carry a model field
// (Gemini's GenerateContentRequest puts the model on the URL path
// via PathParams, not on the body).
//
// Centralises the type-switch that previously lived in three
// places — `cmd/gateway/handler.go::outboundModel`,
// `internal/middleware/rules/middleware.go::extractInboundModel`,
// and this package's own `allocate`. Add a case here whenever a
// new typed request body lands that carries a top-level Model
// string field, and the rule engine + telemetry pick it up for
// free.
//
// Callers that need the routing-driven {model} path param (Gemini)
// must compose with their own param lookup first — see
// `cmd/gateway/handler.go::outboundModel` for the canonical
// PathParams-then-body pattern.
func Model(body any) string {
	switch b := body.(type) {
	case *openaichat.ChatCompletionRequest:
		return strings.TrimSpace(b.Model)
	case *openairesponses.ResponsesRequest:
		return strings.TrimSpace(b.Model)
	case *messages.MessagesRequest:
		return strings.TrimSpace(b.Model)
	}
	return ""
}

// readBody buffers r.Body with a one-byte overshoot so we can distinguish
// "exactly at the cap" from "over the cap" without reading the full body
// twice. A nil body is treated as empty.
func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return []byte{}, nil
	}
	defer func() { _ = r.Body.Close() }()

	buf, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("bodycapture: read: %w", err)
	}
	if int64(len(buf)) > MaxBodyBytes {
		return nil, ErrBodyTooLarge
	}
	return buf, nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, err := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: message})
	if err != nil {
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}
	_, _ = w.Write(body)
}
