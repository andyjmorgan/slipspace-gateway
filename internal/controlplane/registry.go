// Package controlplane is the central control plane (CP): the fleet
// registration channel plus the read-only fleet console backend.
//
// The CP is Postgres-backed and runs as N stateless replicas behind a load
// balancer (see DBRegistry and the configdb store). There is no in-memory or
// file-backed production mode: the binary requires a database or it crashes,
// so there is exactly one source of truth for fleet and config state.
//
// Cardinal invariant CP-0 (design note "Central Control Plane"): the control
// plane is never in the data-plane request path. Nothing here is reachable
// from a gateway's request handling — gateways heartbeat out of band and serve
// entirely from local config.
package controlplane

import (
	"context"
	"errors"
	"time"
)

// ErrMissingGatewayID is returned when a Register/Heartbeat input carries no
// gateway id — the registry keys on it, so it is mandatory.
var ErrMissingGatewayID = errors.New("controlplane: gateway id is required")

// Gateway is one fleet member's last-known state as the registry holds it.
type Gateway struct {
	// ID is the stable, operator-assigned identity of the instance.
	ID string

	// Version is the gateway binary version last reported.
	Version string

	// Labels are operator metadata (cluster, role, ...).
	Labels map[string]string

	// CachedConfigHashes are the content hashes of the closures the gateway
	// currently holds.
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
// and when each was last seen. The production implementation is DBRegistry,
// backed by Postgres so every CP replica shares one consistent view.
type Registry interface {
	// Register records a gateway's first appearance, or refreshes an existing
	// one, and returns its stored record. Idempotent on ID.
	Register(ctx context.Context, in RegisterInput) (Gateway, error)

	// Heartbeat updates last-seen plus mutable state for a gateway. A
	// heartbeat for an unknown gateway self-registers it, so a replica that
	// never saw the original Register still converges on the next heartbeat.
	Heartbeat(ctx context.Context, in HeartbeatInput) (Gateway, error)

	// List returns every known gateway, ordered by ID for stable rendering.
	List(ctx context.Context) ([]Gateway, error)
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
