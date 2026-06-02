package controlplane

import (
	"context"
	"fmt"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// fleetStore is the subset of *configdb.DB the Postgres-backed registry needs.
// Narrowed to an interface so DBRegistry is unit-testable with a stub and not
// only through a live Postgres container.
type fleetStore interface {
	RegisterGateway(ctx context.Context, id, version string, labels map[string]string) (configdb.FleetGateway, error)
	HeartbeatGateway(ctx context.Context, id, version string, labels map[string]string, hashes []string) (configdb.FleetGateway, error)
	ListGateways(ctx context.Context) ([]configdb.FleetGateway, error)
}

// DBRegistry is the Postgres-backed Registry. Unlike MemoryRegistry it holds no
// per-process state: every Register/Heartbeat/List goes to the shared fleet
// table, so N CP replicas behind a load balancer present one consistent fleet
// regardless of which replica a gateway happens to reach.
type DBRegistry struct {
	store fleetStore
}

// NewDBRegistry constructs a Postgres-backed registry over store (a *configdb.DB
// in production).
func NewDBRegistry(store fleetStore) *DBRegistry {
	return &DBRegistry{store: store}
}

// Register records or refreshes a gateway in the shared fleet table.
func (r *DBRegistry) Register(ctx context.Context, in RegisterInput) (Gateway, error) {
	if in.ID == "" {
		return Gateway{}, ErrMissingGatewayID
	}
	g, err := r.store.RegisterGateway(ctx, in.ID, in.Version, in.Labels)
	if err != nil {
		return Gateway{}, fmt.Errorf("controlplane: register gateway: %w", err)
	}
	return fromFleetGateway(g), nil
}

// Heartbeat updates liveness + mutable state, self-registering an unknown
// gateway.
func (r *DBRegistry) Heartbeat(ctx context.Context, in HeartbeatInput) (Gateway, error) {
	if in.ID == "" {
		return Gateway{}, ErrMissingGatewayID
	}
	g, err := r.store.HeartbeatGateway(ctx, in.ID, in.Version, in.Labels, in.CachedConfigHashes)
	if err != nil {
		return Gateway{}, fmt.Errorf("controlplane: heartbeat gateway: %w", err)
	}
	return fromFleetGateway(g), nil
}

// List returns the whole fleet, id-ordered, from the shared table.
func (r *DBRegistry) List(ctx context.Context) ([]Gateway, error) {
	rows, err := r.store.ListGateways(ctx)
	if err != nil {
		return nil, fmt.Errorf("controlplane: list gateways: %w", err)
	}
	out := make([]Gateway, 0, len(rows))
	for _, g := range rows {
		out = append(out, fromFleetGateway(g))
	}
	return out, nil
}

// fromFleetGateway maps the persisted row to the registry's Gateway, normalising
// empty collections to nil so the DB and in-memory registries render
// identically through the read API.
func fromFleetGateway(g configdb.FleetGateway) Gateway {
	return Gateway{
		ID:                 g.ID,
		Version:            g.Version,
		Labels:             cloneLabels(g.Labels),
		CachedConfigHashes: cloneStrings(g.CachedConfigHashes),
		RegisteredAt:       g.RegisteredAt,
		LastSeen:           g.LastSeen,
	}
}
