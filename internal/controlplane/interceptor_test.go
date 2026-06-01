package controlplane

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func okHandler(_ context.Context, _ any) (any, error) { return "ok", nil }

func ctxWithBearer(token string) context.Context {
	md := metadata.New(map[string]string{authMetadataKey: bearerPrefix + token})
	return metadata.NewIncomingContext(context.Background(), md)
}

func TestTokenAuthInterceptor(t *testing.T) {
	info := &grpc.UnaryServerInfo{FullMethod: "/svc/Method"}

	t.Run("empty token disables auth", func(t *testing.T) {
		intc := TokenAuthInterceptor("")
		got, err := intc(context.Background(), nil, info, okHandler)
		if err != nil || got != "ok" {
			t.Fatalf("want pass-through, got %v / %v", got, err)
		}
	})

	t.Run("valid bearer passes", func(t *testing.T) {
		intc := TokenAuthInterceptor("s3cret")
		got, err := intc(ctxWithBearer("s3cret"), nil, info, okHandler)
		if err != nil || got != "ok" {
			t.Fatalf("want pass, got %v / %v", got, err)
		}
	})

	t.Run("wrong token is Unauthenticated", func(t *testing.T) {
		intc := TokenAuthInterceptor("s3cret")
		_, err := intc(ctxWithBearer("nope"), nil, info, okHandler)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("want Unauthenticated, got %v", err)
		}
	})

	t.Run("no metadata is Unauthenticated", func(t *testing.T) {
		intc := TokenAuthInterceptor("s3cret")
		_, err := intc(context.Background(), nil, info, okHandler)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("want Unauthenticated, got %v", err)
		}
	})

	t.Run("metadata without auth key is Unauthenticated", func(t *testing.T) {
		intc := TokenAuthInterceptor("s3cret")
		ctx := metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"x": "y"}))
		_, err := intc(ctx, nil, info, okHandler)
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("want Unauthenticated, got %v", err)
		}
	})
}
