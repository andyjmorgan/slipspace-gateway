package controlplane

import (
	"context"
	"sort"
	"sync"
	"time"
)

// stubRegistry is a minimal in-memory Registry for tests that exercise the
// gRPC service / HTTP read API / server wiring without standing up a Postgres
// container. Production code uses only DBRegistry.
type stubRegistry struct {
	mu  sync.Mutex
	now func() time.Time
	gws map[string]Gateway
}

func newStubRegistry() *stubRegistry {
	return &stubRegistry{
		now: func() time.Time { return time.Now().UTC() },
		gws: make(map[string]Gateway),
	}
}

// fixedClock returns a clock function pinned to t, for deterministic liveness
// tests.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func (r *stubRegistry) Register(_ context.Context, in RegisterInput) (Gateway, error) {
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
	return g, nil
}

func (r *stubRegistry) Heartbeat(_ context.Context, in HeartbeatInput) (Gateway, error) {
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
	return g, nil
}

func (r *stubRegistry) List(_ context.Context) ([]Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Gateway, 0, len(r.gws))
	for _, g := range r.gws {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
