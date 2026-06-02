package controlplane

import (
	"context"
	"crypto/tls"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	fleetpb "github.com/andyjmorgan/sluice-gateway/internal/controlplane/fleetpb"
)

func TestNewGRPCServer_EndToEnd(t *testing.T) {
	reg := newStubRegistry()
	srv := NewGRPCServer(reg, nil, GRPCServerOptions{Token: "s3cret"})
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := fleetpb.NewFleetServiceClient(conn)

	authed := metadata.AppendToOutgoingContext(context.Background(), authMetadataKey, bearerPrefix+"s3cret")
	if _, err := client.Register(authed, &fleetpb.RegisterRequest{GatewayId: "gw-1", Version: "v1"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := client.Heartbeat(authed, &fleetpb.HeartbeatRequest{GatewayId: "gw-1", Version: "v1"}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	gws, err := reg.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(gws) != 1 || gws[0].ID != "gw-1" {
		t.Fatalf("registry state = %+v", gws)
	}

	bad := metadata.AppendToOutgoingContext(context.Background(), authMetadataKey, bearerPrefix+"nope")
	if _, err := client.Register(bad, &fleetpb.RegisterRequest{GatewayId: "gw-2"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated for wrong token, got %v", err)
	}
}

func TestNewGRPCServer_TLSBranch(t *testing.T) {
	srv := NewGRPCServer(newStubRegistry(), nil, GRPCServerOptions{
		TLS: &tls.Config{MinVersion: tls.VersionTLS12},
	})
	t.Cleanup(srv.Stop)
	if _, ok := srv.GetServiceInfo()["sluice.controlplane.v1.FleetService"]; !ok {
		t.Fatalf("FleetService not registered: %v", srv.GetServiceInfo())
	}
}

func TestNewGRPCServer_FetchConfigEndToEnd(t *testing.T) {
	srv := NewGRPCServer(newStubRegistry(), nil, GRPCServerOptions{
		Config: NewStoreConfigProvider(providerTestStore()),
	})
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := fleetpb.NewFleetServiceClient(conn)

	resp, err := client.FetchConfig(context.Background(), &fleetpb.FetchConfigRequest{ApiKey: "sk_live_alpha"}) //nolint:gosec // test fixture, not a credential
	if err != nil {
		t.Fatalf("FetchConfig: %v", err)
	}
	if resp.GetConfiguration() != "alpha" || len(resp.GetBody()) == 0 {
		t.Fatalf("bad closure over the wire: cfg=%q bodylen=%d", resp.GetConfiguration(), len(resp.GetBody()))
	}
}
