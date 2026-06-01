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
		s := NewFleetServer(NewMemoryRegistry(), nil, nil)
		_, err := s.Register(context.Background(), &fleetpb.RegisterRequest{})
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("success returns registered_at", func(t *testing.T) {
		s := NewFleetServer(NewMemoryRegistry(), nil, nil)
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
		s := NewFleetServer(errRegistry{err: errors.New("boom")}, nil, nil)
		_, err := s.Register(context.Background(), &fleetpb.RegisterRequest{GatewayId: "gw-1"})
		wantCode(t, err, codes.Internal)
	})
}

func TestFleetServer_Heartbeat(t *testing.T) {
	t.Run("empty gateway_id is InvalidArgument", func(t *testing.T) {
		s := NewFleetServer(NewMemoryRegistry(), nil, nil)
		_, err := s.Heartbeat(context.Background(), &fleetpb.HeartbeatRequest{})
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("success acks", func(t *testing.T) {
		s := NewFleetServer(NewMemoryRegistry(), nil, nil)
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
		s := NewFleetServer(errRegistry{err: errors.New("boom")}, nil, nil)
		_, err := s.Heartbeat(context.Background(), &fleetpb.HeartbeatRequest{GatewayId: "gw-1"})
		wantCode(t, err, codes.Internal)
	})
}

// stubProvider returns a fixed (Closure, error) for the FetchConfig error-mapping branches.
type stubProvider struct {
	cl  Closure
	err error
}

func (s stubProvider) ClosureForAPIKey(string) (Closure, error) { return s.cl, s.err }

func TestFleetServer_FetchConfig(t *testing.T) {
	withProvider := func() *FleetServer {
		return NewFleetServer(NewMemoryRegistry(), NewStoreConfigProvider(providerTestStore()), nil)
	}

	t.Run("nil provider is Unimplemented", func(t *testing.T) {
		s := NewFleetServer(NewMemoryRegistry(), nil, nil)
		_, err := s.FetchConfig(context.Background(), &fleetpb.FetchConfigRequest{ApiKey: "k"})
		wantCode(t, err, codes.Unimplemented)
	})

	t.Run("empty api_key is InvalidArgument", func(t *testing.T) {
		_, err := withProvider().FetchConfig(context.Background(), &fleetpb.FetchConfigRequest{})
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("known key returns closure", func(t *testing.T) {
		resp, err := withProvider().FetchConfig(context.Background(), &fleetpb.FetchConfigRequest{ApiKey: "sk_live_alpha"}) //nolint:gosec // test fixture, not a credential
		if err != nil {
			t.Fatal(err)
		}
		if resp.GetNotModified() {
			t.Fatal("unexpected not_modified on first fetch")
		}
		if resp.GetConfiguration() != "alpha" || resp.GetHash() == "" || len(resp.GetBody()) == 0 {
			t.Fatalf("bad closure: cfg=%q hash=%q bodylen=%d", resp.GetConfiguration(), resp.GetHash(), len(resp.GetBody()))
		}
	})

	t.Run("matching known_hash yields not_modified without body", func(t *testing.T) {
		s := withProvider()
		first, err := s.FetchConfig(context.Background(), &fleetpb.FetchConfigRequest{ApiKey: "sk_live_alpha"}) //nolint:gosec // test fixture, not a credential
		if err != nil {
			t.Fatal(err)
		}
		resp, err := s.FetchConfig(context.Background(), &fleetpb.FetchConfigRequest{ApiKey: "sk_live_alpha", KnownHash: first.GetHash()}) //nolint:gosec // test fixture, not a credential
		if err != nil {
			t.Fatal(err)
		}
		if !resp.GetNotModified() || len(resp.GetBody()) != 0 {
			t.Fatalf("want not_modified with empty body, got not_modified=%v bodylen=%d", resp.GetNotModified(), len(resp.GetBody()))
		}
	})

	t.Run("unknown key is NotFound", func(t *testing.T) {
		_, err := withProvider().FetchConfig(context.Background(), &fleetpb.FetchConfigRequest{ApiKey: "ghost"})
		wantCode(t, err, codes.NotFound)
	})

	t.Run("no-config provider is Unavailable", func(t *testing.T) {
		s := NewFleetServer(NewMemoryRegistry(), stubProvider{err: ErrNoConfig}, nil)
		_, err := s.FetchConfig(context.Background(), &fleetpb.FetchConfigRequest{ApiKey: "k"})
		wantCode(t, err, codes.Unavailable)
	})

	t.Run("other provider error is Internal", func(t *testing.T) {
		s := NewFleetServer(NewMemoryRegistry(), stubProvider{err: errors.New("boom")}, nil)
		_, err := s.FetchConfig(context.Background(), &fleetpb.FetchConfigRequest{ApiKey: "k"})
		wantCode(t, err, codes.Internal)
	})
}
