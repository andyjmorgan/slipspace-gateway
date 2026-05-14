package bodycapture

import "context"

type capturedKey struct{}

// FromContext retrieves the Captured request stashed by HTTPHandler. The bool
// is false when bodycapture has not yet run on this context.
func FromContext(ctx context.Context) (Captured, bool) {
	if ctx == nil {
		return Captured{}, false
	}
	c, ok := ctx.Value(capturedKey{}).(Captured)
	return c, ok
}

func withCaptured(ctx context.Context, c Captured) context.Context {
	return context.WithValue(ctx, capturedKey{}, c)
}
