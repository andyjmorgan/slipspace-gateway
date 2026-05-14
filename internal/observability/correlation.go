package observability

import (
	"context"

	"github.com/google/uuid"
)

type correlationIDKey struct{}

// WithCorrelationID stores id on ctx for retrieval via
// CorrelationIDFromContext. Empty IDs are ignored so callers can pipe an
// inbound header through unconditionally.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, correlationIDKey{}, id)
}

// CorrelationIDFromContext returns the correlation ID stored on ctx, or
// the empty string when none is present.
func CorrelationIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(correlationIDKey{}).(string); ok {
		return id
	}
	return ""
}

// NewCorrelationID returns a fresh UUIDv4 string for use as a request
// correlation ID.
func NewCorrelationID() string {
	return uuid.NewString()
}
