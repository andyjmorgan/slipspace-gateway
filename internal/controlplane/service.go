package controlplane

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	fleetpb "github.com/andyjmorgan/sluice-gateway/internal/controlplane/fleetpb"
)

// FleetServer implements the gRPC FleetService over a Registry. Phase 1 serves
// Register + Heartbeat; the config-distribution RPCs land in Phase 2 and are
// not yet part of the proto.
type FleetServer struct {
	fleetpb.UnimplementedFleetServiceServer

	reg Registry
	log *slog.Logger
}

// NewFleetServer constructs a FleetServer backed by reg. log may be nil.
func NewFleetServer(reg Registry, log *slog.Logger) *FleetServer {
	return &FleetServer{reg: reg, log: log}
}

// Register records a gateway's startup announcement.
func (s *FleetServer) Register(ctx context.Context, req *fleetpb.RegisterRequest) (*fleetpb.RegisterResponse, error) {
	if req.GetGatewayId() == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id is required")
	}
	g, err := s.reg.Register(ctx, RegisterInput{
		ID:      req.GetGatewayId(),
		Version: req.GetVersion(),
		Labels:  req.GetLabels(),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "register: %v", err)
	}
	if s.log != nil {
		s.log.InfoContext(ctx, "gateway registered",
			"gateway_id", g.ID,
			"version", g.Version,
		)
	}
	return &fleetpb.RegisterResponse{
		RegisteredAt: g.RegisteredAt.Format(time.RFC3339),
	}, nil
}

// Heartbeat records a liveness ping. The response is an empty ack in Phase 1.
func (s *FleetServer) Heartbeat(ctx context.Context, req *fleetpb.HeartbeatRequest) (*fleetpb.HeartbeatResponse, error) {
	if req.GetGatewayId() == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_id is required")
	}
	if _, err := s.reg.Heartbeat(ctx, HeartbeatInput{
		ID:                 req.GetGatewayId(),
		Version:            req.GetVersion(),
		Labels:             req.GetLabels(),
		CachedConfigHashes: req.GetCachedConfigHashes(),
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "heartbeat: %v", err)
	}
	return &fleetpb.HeartbeatResponse{}, nil
}
