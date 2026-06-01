package controlplane

import (
	"crypto/tls"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	fleetpb "github.com/andyjmorgan/sluice-gateway/internal/controlplane/fleetpb"
)

// GRPCServerOptions configures the fleet gRPC server.
type GRPCServerOptions struct {
	// Token is the bootstrap token every gateway must present. Empty disables
	// auth (trusted-network dev only).
	Token string

	// TLS, when non-nil, wraps the listener. Nil serves plaintext — only safe
	// inside a trusted internal boundary, since Phase 2 closures carry inline
	// secrets (CP-3 revised). The bootstrap defaults TLS on; plaintext is an
	// explicit, loud opt-out.
	TLS *tls.Config

	// Config, when non-nil, enables FetchConfig (config distribution). Nil
	// leaves the CP registration-only (Phase 1).
	Config ConfigProvider
}

// NewGRPCServer builds a *grpc.Server with the FleetService registered, the
// token interceptor installed, and TLS applied when configured. The caller
// owns the listener and the Serve/GracefulStop lifecycle.
func NewGRPCServer(reg Registry, log *slog.Logger, opts GRPCServerOptions) *grpc.Server {
	serverOpts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(TokenAuthInterceptor(opts.Token)),
	}
	if opts.TLS != nil {
		serverOpts = append(serverOpts, grpc.Creds(credentials.NewTLS(opts.TLS)))
	}
	srv := grpc.NewServer(serverOpts...)
	fleetpb.RegisterFleetServiceServer(srv, NewFleetServer(reg, opts.Config, log))
	return srv
}
