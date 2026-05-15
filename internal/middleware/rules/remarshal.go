package rules

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// BodyRemarshalHandler sits between the rules middleware and the
// downstream forwarder. It re-encodes bodycapture.Captured.Body to
// bytes when a rule mutated the typed body (state.BodyMutated true)
// and replaces r.Body + Content-Length on the request. Otherwise it
// is a no-op — the forwarder reads the unchanged raw bytes.
//
// Marshal failures surface as a 500 response and increment
// gateway.rule.errors.total{error_kind="body_remarshal"}; the typed
// body's MarshalJSON is responsible for preserving DynamicProperties
// (the providers/* packages already do).
func BodyRemarshalHandler(meters *observability.Meters, next http.Handler) http.Handler {
	if next == nil {
		panic("rules: BodyRemarshalHandler called with nil next handler")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		state := MutableStateFromContext(ctx)
		if state == nil || !state.BodyMutated {
			next.ServeHTTP(w, r)
			return
		}

		captured, ok := bodycapture.FromContext(ctx)
		if !ok || captured.Body == nil {
			next.ServeHTTP(w, r)
			return
		}

		newBytes, err := json.Marshal(captured.Body)
		if err != nil {
			recordRemarshalError(ctx, meters)
			logger := observability.FromContext(ctx)
			logger.ErrorContext(ctx, "rules: body remarshal", "err", err.Error())
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(newBytes))
		r.ContentLength = int64(len(newBytes))
		r.Header.Set("Content-Length", strconv.Itoa(len(newBytes)))

		next.ServeHTTP(w, r)
	})
}

func recordRemarshalError(ctx context.Context, meters *observability.Meters) {
	if meters == nil || meters.RuleErrorsTotal == nil {
		return
	}
	meters.RuleErrorsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("error_kind", "body_remarshal"),
	))
}

