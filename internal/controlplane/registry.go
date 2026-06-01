// Package controlplane is the central control plane (CP): the fleet
// registration channel plus the read-only fleet console backend.
//
// Phase 1 (this package's current surface) is registration + heartbeat over
// gRPC, backed by an in-memory registry, plus an HTTP read API the console
// reads. Config distribution (per-configuration closures fetched by api-key)
// and a Postgres-backed registry land in later phases behind the same
// interfaces.
//
// Cardinal invariant CP-0 (design note "Central Control Plane"): the control
// plane is never in the data-plane request path. Nothing here is reachable
// from a gateway's request handling — gateways heartbeat out of band and serve
// entirely from local config.
package controlplane

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrMissingGatewayID is returned when a Register/Heartbeat input carries no
// gateway id — the registry keys on it, so it is mandatory.
var ErrMissingGatewayID = errors.New("controlplane: gateway id is required")

// Gateway is one fleet member's last-known state as the registry holds it.
// Phase 1 carries identity + liveness only; config-assignment fields arrive
// with Phase 2 distribution.
type Gateway struct {
	// ID is the stable, operator-assigned identity of the instance.
	ID string

	// Version is the gateway binary version last reported.
	Version string

	// Labels are operator metadata (cluster, role, ...).
	Labels map[string]string

	// CachedConfigHashes are the content hashes of the closures the gateway
	// currently holds. Empty in Phase 1 (no distribution yet).
	CachedConfigHashes []string

	// RegisteredAt is when the CP first saw this gateway (UTC).
	RegisteredAt time.Time

	// LastSeen is the most recent Register or Heartbeat (UTC). Liveness is
	// derived from its age.
	LastSeen time.Time
}

// RegisterInput is the announce payload a gateway sends on startup.
type RegisterInput struct {
	ID      string
	Version string
	Labels  map[string]string
}

// HeartbeatInput is the periodic liveness payload.
type HeartbeatInput struct {
	ID                 string
	Version            string
	Labels             map[string]string
	CachedConfigHashes []string
}

// Registry tracks the live fleet: which gateways exist, what version they run,
// and when each was last seen. Phase 1 is in-memory (MemoryRegistry); a
// Postgres-backed implementation swaps in behind this interface without
// touching the gRPC service or the HTTP read API.
type Registry interface {
	// Register records a gateway's first appearance, or refreshes an existing
	// one, and returns its stored record. Idempotent on ID.
	Register(ctx context.Context, in RegisterInput) (Gateway, error)

	// Heartbeat updates last-seen plus mutable state for a gateway. A
	// heartbeat for an unknown gateway self-registers it, so a CP restart that
	// drops the in-memory registry self-heals on the next heartbeat round.
	Heartbeat(ctx context.Context, in HeartbeatInput) (Gateway, error)

	// List returns every known gateway, ordered by ID for stable rendering.
	List(ctx context.Context) ([]Gateway, error)
}

// MemoryRegistry is the in-memory Registry used in Phase 1. Safe for
// concurrent use. now is overridable in tests for deterministic timestamps.
type MemoryRegistry struct {
	mu  sync.Mutex
	now func() time.Time
	gws map[string]Gateway
}

// NewMemoryRegistry constructs an empty in-memory registry.
func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		now: func() time.Time { return time.Now().UTC() },
		gws: make(map[string]Gateway),
	}
}

// Register records or refreshes a gateway.
func (r *MemoryRegistry) Register(_ context.Context, in RegisterInput) (Gateway, error) {
	if in.ID == "" {
		return Gateway{}, ErrMissingGatewayID
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	g, ok := r.gws[in.ID]
	if !ok {
		g = Gateway{ID: in.ID, RegisteredAt: now}
	}
	g.Version = in.Version
	g.Labels = cloneLabels(in.Labels)
	g.LastSeen = now
	r.gws[in.ID] = g
	return cloneGateway(g), nil
}

// Heartbeat updates liveness and mutable state, self-registering an unknown
// gateway.
func (r *MemoryRegistry) Heartbeat(_ context.Context, in HeartbeatInput) (Gateway, error) {
	if in.ID == "" {
		return Gateway{}, ErrMissingGatewayID
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	g, ok := r.gws[in.ID]
	if !ok {
		g = Gateway{ID: in.ID, RegisteredAt: now}
	}
	g.Version = in.Version
	g.Labels = cloneLabels(in.Labels)
	g.CachedConfigHashes = cloneStrings(in.CachedConfigHashes)
	g.LastSeen = now
	r.gws[in.ID] = g
	return cloneGateway(g), nil
}

// List returns a stable, ID-ordered snapshot of the fleet.
func (r *MemoryRegistry) List(_ context.Context) ([]Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]Gateway, 0, len(r.gws))
	for _, g := range r.gws {
		out = append(out, cloneGateway(g))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func cloneLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func cloneGateway(g Gateway) Gateway {
	g.Labels = cloneLabels(g.Labels)
	g.CachedConfigHashes = cloneStrings(g.CachedConfigHashes)
	return g
}
