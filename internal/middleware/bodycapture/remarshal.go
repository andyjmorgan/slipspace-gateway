package bodycapture

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// RemarshalTyped re-encodes the captured typed request body into
// fresh JSON bytes. Returns (nil, nil) when no typed body was
// captured (e.g. admin routes, unknown endpoints) — callers treat
// that as "nothing to do, leave r.Body as-is."
//
// Re-marshal is byte-equivalent up to key ordering by the provider
// model contract: every type embeds DynamicProperties to round-trip
// unknown fields, every polymorphic base has an UnknownX fallback.
// Upstreams accept any key ordering.
//
// Designed to be called by:
//   - the rules.BodyRemarshalHandler middleware (after rules mutate
//     the typed body),
//   - the v1.2+ resilience orchestrator (per attempt, after a target
//     applies its own destination-mutating actions).
//
// Both callers consume the bytes via ApplyBodyBytes.
func RemarshalTyped(ctx context.Context) ([]byte, error) {
	captured, ok := FromContext(ctx)
	if !ok || captured.Body == nil {
		return nil, nil
	}
	return json.Marshal(captured.Body)
}

// ApplyBodyBytes installs body on r as the outgoing request body and
// updates Content-Length to match. Used by every caller that mutates
// the body after bodycapture has run — rules.BodyRemarshalHandler
// today, the resilience orchestrator's per-attempt apply step in
// v1.2.
//
// Idempotent: calling with the same bytes twice produces the same
// observable r state.
func ApplyBodyBytes(r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
}
