package reconciler

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane"
)

// stubRegistry is a minimal in-memory controlplane.Registry for the reconciler
// tests, which spin up a real gRPC control plane and assert the reconciler
// registered. Production uses only the Postgres-backed DBRegistry.
type stubRegistry struct {
	mu  sync.Mutex
	gws map[string]controlplane.Gateway
}

func newStubRegistry() *stubRegistry {
	return &stubRegistry{gws: make(map[string]controlplane.Gateway)}
}

func (r *stubRegistry) Register(_ context.Context, in controlplane.RegisterInput) (controlplane.Gateway, error) {
	if in.ID == "" {
		return controlplane.Gateway{}, controlplane.ErrMissingGatewayID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	g, ok := r.gws[in.ID]
	if !ok {
		g = controlplane.Gateway{ID: in.ID, RegisteredAt: now}
	}
	g.Version = in.Version
	g.Labels = in.Labels
	g.LastSeen = now
	r.gws[in.ID] = g
	return g, nil
}

func (r *stubRegistry) Heartbeat(_ context.Context, in controlplane.HeartbeatInput) (controlplane.Gateway, error) {
	if in.ID == "" {
		return controlplane.Gateway{}, controlplane.ErrMissingGatewayID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	g, ok := r.gws[in.ID]
	if !ok {
		g = controlplane.Gateway{ID: in.ID, RegisteredAt: now}
	}
	g.Version = in.Version
	g.Labels = in.Labels
	g.CachedConfigHashes = in.CachedConfigHashes
	g.LastSeen = now
	r.gws[in.ID] = g
	return g, nil
}

func (r *stubRegistry) List(_ context.Context) ([]controlplane.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]controlplane.Gateway, 0, len(r.gws))
	for _, g := range r.gws {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
