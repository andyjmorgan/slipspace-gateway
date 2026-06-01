package controlplane

import (
	"context"
	"crypto/subtle"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// authMetadataKey is the gRPC metadata key carrying the bootstrap credential.
// gRPC lowercases metadata keys on the wire.
const authMetadataKey = "authorization"

const bearerPrefix = "Bearer "

// TokenAuthInterceptor returns a unary server interceptor that requires every
// call to present "authorization: Bearer <token>" matching the configured
// bootstrap token, compared in constant time. An empty token disables auth —
// intended only for a trusted-network dev loop; production always sets one.
//
// This guards the gateway <-> control-plane channel and is distinct from the
// per-request Sluice api-key. Decision #2 (design note): bootstrap token now,
// mTLS later — the credential type is isolated here so a transport-level
// verifier can be added without touching the service.
func TokenAuthInterceptor(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if token == "" {
			return handler(ctx, req)
		}
		if !validBearer(ctx, token) {
			return nil, status.Error(codes.Unauthenticated, "missing or invalid bootstrap token")
		}
		return handler(ctx, req)
	}
}

func validBearer(ctx context.Context, token string) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	vals := md.Get(authMetadataKey)
	if len(vals) == 0 {
		return false
	}
	got := strings.TrimPrefix(vals[0], bearerPrefix)
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}
