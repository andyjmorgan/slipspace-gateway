package observability_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

func TestWithLogger_StoresAndRetrieves(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	ctx := observability.WithLogger(context.Background(), logger)
	if got := observability.FromContext(ctx); got != logger {
		t.Errorf("FromContext returned different logger")
	}
}

func TestWithLogger_NilLoggerIsNoop(t *testing.T) {
	t.Parallel()

	parent := context.Background()
	ctx := observability.WithLogger(parent, nil)
	if ctx != parent {
		t.Errorf("nil logger should return original context")
	}
}

func TestFromContext_FallbackToDefault(t *testing.T) {
	t.Parallel()

	if l := observability.FromContext(context.Background()); l == nil {
		t.Fatalf("expected default logger fallback")
	}
}

func TestFromContext_NilContext(t *testing.T) {
	t.Parallel()

	//nolint:staticcheck // intentional nil context to exercise the guard
	if l := observability.FromContext(nil); l == nil {
		t.Fatalf("expected default logger for nil context")
	}
}

func TestWithCorrelationID_StoresAndRetrieves(t *testing.T) {
	t.Parallel()

	ctx := observability.WithCorrelationID(context.Background(), "abc-123")
	if got := observability.CorrelationIDFromContext(ctx); got != "abc-123" {
		t.Errorf("CorrelationIDFromContext = %q, want %q", got, "abc-123")
	}
}

func TestWithCorrelationID_EmptyIDIsNoop(t *testing.T) {
	t.Parallel()

	parent := context.Background()
	ctx := observability.WithCorrelationID(parent, "")
	if ctx != parent {
		t.Errorf("empty ID should return original context")
	}
}

func TestCorrelationIDFromContext_NilContext(t *testing.T) {
	t.Parallel()

	//nolint:staticcheck // intentional nil context to exercise the guard
	if id := observability.CorrelationIDFromContext(nil); id != "" {
		t.Errorf("expected empty id for nil context, got %q", id)
	}
}

func TestCorrelationIDFromContext_Missing(t *testing.T) {
	t.Parallel()

	if id := observability.CorrelationIDFromContext(context.Background()); id != "" {
		t.Errorf("expected empty id, got %q", id)
	}
}

func TestNewCorrelationID_ProducesUniqueValues(t *testing.T) {
	t.Parallel()

	a := observability.NewCorrelationID()
	b := observability.NewCorrelationID()
	if a == "" || b == "" {
		t.Fatalf("empty correlation id")
	}
	if a == b {
		t.Errorf("expected unique correlation ids, got %q twice", a)
	}
}
