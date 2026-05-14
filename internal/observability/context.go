package observability

import (
	"context"
	"log/slog"
)

type loggerKey struct{}

// WithLogger stores l on ctx so downstream code can retrieve it via
// FromContext. Passing a nil logger returns ctx unchanged so callers
// don't have to guard against it.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, loggerKey{}, l)
}

// FromContext returns the logger stored on ctx, falling back to
// slog.Default() when none is present. The returned logger is never nil.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
