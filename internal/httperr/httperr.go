// Package httperr writes consistent JSON error responses on behalf of the
// gateway middleware chain. Callers pass a structured (status, layer, code,
// message) tuple and the helper writes a Content-Type, status code, and a
// body of shape {"error": "<code>", "message": "<msg>"}. When a counter is
// injected, every write also increments an OTel counter labelled with the
// originating layer and the error code so dashboards can break down error
// classes without scraping bodies.
package httperr

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// fallbackBody is written verbatim when the structured body fails to
// marshal. Hard-coded so the writer cannot recurse into json.Marshal a
// second time on the failure path.
const fallbackBody = `{"error":"internal"}`

// marshalFunc indirects json.Marshal so the marshal-failure branch can be
// exercised in tests without racing on a package-level var. Production
// code always uses json.Marshal — Body has no unmarshalable fields, so
// the production path cannot fail.
type marshalFunc func(any) ([]byte, error)

// Body is the JSON shape this package writes. Two stable fields:
// Error carries the machine-readable code and consumers match on it;
// Message is human-readable detail for logs and UI rendering.
type Body struct {
	// Error is the machine-readable code (e.g., "no_route", "internal",
	// "forward_failed"). It is intended to be stable across releases so
	// dashboards and clients can pin on it.
	Error string `json:"error"`

	// Message is the human-readable detail. Optional — omitted from the
	// JSON when empty so terse 404s stay terse.
	Message string `json:"message,omitempty"`
}

// Writer formats and writes the typed error response and, when configured
// with a counter, fires one Add per write so error volume by layer and
// code shows up alongside the other gateway counters.
type Writer struct {
	// counter is the OTel counter incremented on every Write. Optional —
	// when nil the Writer is purely a formatter, which is useful before
	// observability.Setup has run (early startup, tests).
	counter metric.Int64Counter

	// logger is used to record marshal-fallback events. Optional — when
	// nil the writer drops the warning silently.
	logger *slog.Logger

	// marshal is the JSON marshaller. Always json.Marshal in production;
	// tests override it via newWithMarshaller to exercise the
	// marshal-failure fallback branch.
	marshal marshalFunc
}

// New returns a Writer that writes JSON error responses and, when counter
// is non-nil, increments it on each call. logger is used only on the
// json.Marshal failure path; both arguments may be nil for an entirely
// passive formatter.
func New(counter metric.Int64Counter, logger *slog.Logger) *Writer {
	return &Writer{counter: counter, logger: logger, marshal: json.Marshal}
}

// newWithMarshaller is the test seam: same as New but takes a custom
// marshaller so the marshal-failure branch can be exercised deterministically.
func newWithMarshaller(counter metric.Int64Counter, logger *slog.Logger, marshal marshalFunc) *Writer {
	return &Writer{counter: counter, logger: logger, marshal: marshal}
}

// Write emits the structured error response. layer names the middleware
// that produced the error (e.g., "routing", "handler") so dashboards can
// split error volume by source. code is the stable machine-readable
// identifier; msg is the human-readable detail. The HTTP body is set
// before the status header is written, but write order itself must call
// w.WriteHeader after the Content-Type header — net/http ignores headers
// added after WriteHeader.
func (wr *Writer) Write(ctx context.Context, w http.ResponseWriter, status int, layer, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	data, err := wr.marshal(Body{Error: code, Message: msg})
	if err != nil {
		if wr.logger != nil {
			wr.logger.WarnContext(ctx, "httperr: marshal error body", "err", err.Error(), "layer", layer, "code", code)
		}
		_, _ = w.Write([]byte(fallbackBody))
	} else {
		_, _ = w.Write(data)
	}

	if wr.counter != nil {
		wr.counter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("layer", layer),
			attribute.String("code", code),
			attribute.Int("status_code", status),
		))
	}
}
