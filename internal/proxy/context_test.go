package proxy

import (
	"context"
	"testing"
	"time"
)

func TestResponseHeaderTimeoutContext_RoundTrip(t *testing.T) {
	base := context.Background()

	if d, ok := ResponseHeaderTimeoutFromContext(base); ok || d != 0 {
		t.Fatalf("empty context: got (%s, %v), want (0, false)", d, ok)
	}

	ctx := WithResponseHeaderTimeout(base, 7*time.Second)
	d, ok := ResponseHeaderTimeoutFromContext(ctx)
	if !ok || d != 7*time.Second {
		t.Fatalf("after set: got (%s, %v), want (7s, true)", d, ok)
	}
}

func TestWithResponseHeaderTimeout_NonPositiveIsNoOp(t *testing.T) {
	for _, d := range []time.Duration{0, -1 * time.Second} {
		ctx := WithResponseHeaderTimeout(context.Background(), d)
		if got, ok := ResponseHeaderTimeoutFromContext(ctx); ok || got != 0 {
			t.Fatalf("d=%s: got (%s, %v), want no override", d, got, ok)
		}
	}
}
