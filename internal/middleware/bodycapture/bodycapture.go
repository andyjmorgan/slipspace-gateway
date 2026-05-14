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

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/providers/anthropic/messages"
	"github.com/andyjmorgan/sluice-gateway/providers/gemini/content"
	openaichat "github.com/andyjmorgan/sluice-gateway/providers/openai/chat"
	openairesponses "github.com/andyjmorgan/sluice-gateway/providers/openai/responses"
)

// RequestKind enumerates the typed body shapes the middleware knows how to
// deserialize. The values mirror the strings accepted by
// contracts/config.Endpoint.RequestKind.
type RequestKind string

// RequestKind values. Keep in sync with the YAML schema accepted by the
// config loader. New providers add a new kind here plus a case in Capture.
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
// Raw is always populated with the verbatim wire bytes so the forwarder can
// resend them. Body is the typed deserialised value or nil for passthrough.
type Captured struct {
	Kind RequestKind

	Raw []byte

	Body any
}

// KindFromContextFunc returns the RequestKind the routing middleware stashed
// on the request context. ok is false when routing has not run or the route
// did not carry a kind.
type KindFromContextFunc func(ctx context.Context) (RequestKind, bool)

// Capture reads r.Body up to MaxBodyBytes and, when kind is a typed shape,
// deserialises it into the matching provider struct via the package's
// DynamicProperties-aware UnmarshalJSON. The returned Captured carries the
// verbatim raw bytes so callers can resend them downstream.
//
// Errors: ErrBodyTooLarge if the body exceeds MaxBodyBytes; ErrParse wrapped
// around the json error on malformed input; ErrUnknownKind on an unmodelled
// RequestKind.
func Capture(r *http.Request, kind RequestKind) (Captured, error) {
	raw, err := readBody(r)
	if err != nil {
		return Captured{}, err
	}

	if kind == KindPassthrough {
		return Captured{Kind: kind, Raw: raw, Body: nil}, nil
	}

	target, err := allocate(kind)
	if err != nil {
		return Captured{}, err
	}

	if err := json.Unmarshal(raw, target); err != nil {
		return Captured{}, fmt.Errorf("%w: %w", ErrParse, err)
	}

	return Captured{Kind: kind, Raw: raw, Body: target}, nil
}

// HTTPHandler wraps next with the capture step. The route's RequestKind is
// pulled from context via kindFrom. On success the Captured lands on the
// request context and r.Body is replaced with a reader over the original
// bytes so the forwarder reads identical content.
//
// On ErrBodyTooLarge the handler writes 413; on ErrParse it writes 400 and
// logs at warn level; on missing route or unknown kind it writes 500 — those
// are wiring bugs that should not reach clients silently.
func HTTPHandler(kindFrom KindFromContextFunc, next http.Handler) http.Handler {
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

		captured, err := Capture(r, kind)
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
