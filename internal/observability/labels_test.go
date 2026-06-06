package observability_test

import (
	"context"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func TestRequestLabels_RoundTrip(t *testing.T) {
	t.Parallel()

	in := observability.RequestLabels{
		Provider: "openai",
		Protocol: "chat_completions",
		Model:    "gpt-4o-mini",
	}
	ctx := observability.WithRequestLabels(context.Background(), in)

	got := observability.RequestLabelsFromContext(ctx)
	if got != in {
		t.Errorf("round-trip = %+v, want %+v", got, in)
	}
}

func TestRequestLabelsFromContext_NilCtx(t *testing.T) {
	t.Parallel()
	//nolint:staticcheck // exercising the nil-ctx defensive branch
	if got := observability.RequestLabelsFromContext(nil); got != (observability.RequestLabels{}) {
		t.Errorf("nil ctx should yield zero value, got %+v", got)
	}
}

func TestRequestLabelsFromContext_Unsalted(t *testing.T) {
	t.Parallel()
	if got := observability.RequestLabelsFromContext(context.Background()); got != (observability.RequestLabels{}) {
		t.Errorf("unsalted ctx should yield zero value, got %+v", got)
	}
}
