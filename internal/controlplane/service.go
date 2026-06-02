package controlplane

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	fleetpb "github.com/andyjmorgan/sluice-gateway/internal/controlplane/fleetpb"
)

// FleetServer implements the gRPC FleetService over a Registry. Register +
// Heartbeat are Phase 1; FetchConfig (config distribution) is Phase 2 and is
// served only when a ConfigProvider is wired.
type FleetServer struct {
	fleetpb.UnimplementedFleetServiceServer

	reg    Registry
	config ConfigProvider
	log    *slog.Logger
}

// NewFleetServer constructs a FleetServer backed by reg. config may be nil
// (FetchConfig then reports Unimplemented); log may be nil.
func NewFleetServer(reg Registry, config ConfigProvider, log *slog.Logger) *FleetServer {
	return &FleetServer{reg: reg, config: config, log: log}
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

// FetchConfig resolves the presented api-key to its per-configuration closure.
// Returns Unimplemented when this control plane has no ConfigProvider (Phase 1
// deployments), NotFound for an unknown/disabled key, and not_modified when the
// caller's known_hash already matches.
func (s *FleetServer) FetchConfig(ctx context.Context, req *fleetpb.FetchConfigRequest) (*fleetpb.FetchConfigResponse, error) {
	if s.config == nil {
		return nil, status.Error(codes.Unimplemented, "config distribution not enabled")
	}
	if req.GetApiKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "api_key is required")
	}

	cl, err := s.config.ClosureForAPIKey(ctx, req.GetApiKey())
	switch {
	case errors.Is(err, ErrUnknownAPIKey):
		return nil, status.Error(codes.NotFound, "unknown or disabled api key")
	case errors.Is(err, ErrNoConfig):
		return nil, status.Error(codes.Unavailable, "no configuration loaded")
	case err != nil:
		return nil, status.Errorf(codes.Internal, "closure: %v", err)
	}

	if req.GetKnownHash() != "" && req.GetKnownHash() == cl.Hash {
		return &fleetpb.FetchConfigResponse{
			NotModified:   true,
			Configuration: cl.Configuration,
			Hash:          cl.Hash,
		}, nil
	}
	return &fleetpb.FetchConfigResponse{
		Configuration: cl.Configuration,
		Hash:          cl.Hash,
		Body:          cl.Body,
	}, nil
}
