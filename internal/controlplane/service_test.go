package controlplane

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	fleetpb "github.com/andyjmorgan/sluice-gateway/internal/controlplane/fleetpb"
)

// errRegistry fails every call — used to exercise the service's Internal-error
// branches without a real backing store.
type errRegistry struct{ err error }

func (e errRegistry) Register(context.Context, RegisterInput) (Gateway, error) {
	return Gateway{}, e.err
}

func (e errRegistry) Heartbeat(context.Context, HeartbeatInput) (Gateway, error) {
	return Gateway{}, e.err
}

func (e errRegistry) List(context.Context) ([]Gateway, error) {
	return nil, e.err
}

func wantCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("status code = %v, want %v (err=%v)", status.Code(err), code, err)
	}
}

func TestFleetServer_Register(t *testing.T) {
	t.Run("empty gateway_id is InvalidArgument", func(t *testing.T) {
		s := NewFleetServer(NewMemoryRegistry(), nil)
		_, err := s.Register(context.Background(), &fleetpb.RegisterRequest{})
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("success returns registered_at", func(t *testing.T) {
		s := NewFleetServer(NewMemoryRegistry(), nil)
		resp, err := s.Register(context.Background(), &fleetpb.RegisterRequest{
			GatewayId: "gw-1",
			Version:   "v1.2.0",
		})
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		if resp.GetRegisteredAt() == "" {
			t.Fatal("registered_at empty")
		}
	})

	t.Run("registry error is Internal", func(t *testing.T) {
		s := NewFleetServer(errRegistry{err: errors.New("boom")}, nil)
		_, err := s.Register(context.Background(), &fleetpb.RegisterRequest{GatewayId: "gw-1"})
		wantCode(t, err, codes.Internal)
	})
}

func TestFleetServer_Heartbeat(t *testing.T) {
	t.Run("empty gateway_id is InvalidArgument", func(t *testing.T) {
		s := NewFleetServer(NewMemoryRegistry(), nil)
		_, err := s.Heartbeat(context.Background(), &fleetpb.HeartbeatRequest{})
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("success acks", func(t *testing.T) {
		s := NewFleetServer(NewMemoryRegistry(), nil)
		_, err := s.Heartbeat(context.Background(), &fleetpb.HeartbeatRequest{
			GatewayId:          "gw-1",
			Version:            "v1.2.0",
			CachedConfigHashes: []string{"sha256:abc"},
		})
		if err != nil {
			t.Fatalf("heartbeat: %v", err)
		}
	})

	t.Run("registry error is Internal", func(t *testing.T) {
		s := NewFleetServer(errRegistry{err: errors.New("boom")}, nil)
		_, err := s.Heartbeat(context.Background(), &fleetpb.HeartbeatRequest{GatewayId: "gw-1"})
		wantCode(t, err, codes.Internal)
	})
}
