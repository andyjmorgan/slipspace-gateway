package auth

import "context"

type authKey struct{}

// FromContext returns the AuthResult stashed on ctx by the auth middleware.
// The bool is false when auth has not yet run.
func FromContext(ctx context.Context) (AuthResult, bool) {
	if ctx == nil {
		return AuthResult{}, false
	}
	ar, ok := ctx.Value(authKey{}).(AuthResult)
	return ar, ok
}

func withAuth(ctx context.Context, ar AuthResult) context.Context {
	return context.WithValue(ctx, authKey{}, ar)
}
