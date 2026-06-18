package admin

import (
	"net/http"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

// statusRecorder is a minimal http.ResponseWriter wrapper that captures
// the response status code so the instrumentation middleware can label
// the counter after the handler runs. Defaults to 200 if WriteHeader
// is never called (the stdlib's documented contract).
//
// Optional ResponseWriter capability interfaces (http.Flusher today;
// Hijacker / pusher could be added later if needed) are forwarded
// explicitly. Wrapping a Flusher and silently dropping the capability
// breaks SSE handlers downstream — the /api/v1/messages/stream route
// is the immediate consumer; PR #24's recordingResponseWriter shipped
// the same bug in the data plane.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Flush forwards to the wrapped ResponseWriter when it implements
// http.Flusher. Required for SSE: net/http's *response satisfies
// Flusher for HTTP/1.1 + HTTP/2, so production paths exercise the
// forward; the no-op fallback keeps tests with bare recorders working
// without panicking.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// InstrumentRoute wraps next with a counter increment on response. The
// route label is fixed by the caller (e.g. "/api/v1/auth/me", "static",
// "fallback") rather than read from the URL, so cardinality stays bounded
// even when the SPA serves arbitrary asset paths.
//
// A nil meters bundle is a no-op passthrough; this is the path tests
// take when they exercise downstream handlers without standing up a
// full observability pipeline.
func InstrumentRoute(meters *observability.Meters, route string, next http.Handler) http.Handler {
	if meters == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		meters.AdminRequestsTotal.Add(r.Context(), 1, metric.WithAttributes(
			attribute.String("route", route),
			attribute.String("status", strconv.Itoa(status)),
		))
	})
}
