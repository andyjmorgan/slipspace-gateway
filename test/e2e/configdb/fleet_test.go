//go:build e2e

package configdb_test

import (
	"context"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// TestConfigDB_FleetRegistry exercises the Postgres-backed fleet registry SQL
// against real Postgres: register, heartbeat (preserving registered_at +
// updating hashes), heartbeat-self-registers, idempotent re-register, and
// id-ordered List. This is the shared source of truth that lets N CP replicas
// present one consistent fleet.
func TestConfigDB_FleetRegistry(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	// Register a new gateway.
	g, err := db.RegisterGateway(ctx, "gw-1", "v1", map[string]string{"role": "edge"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if g.ID != "gw-1" || g.Version != "v1" || g.Labels["role"] != "edge" {
		t.Fatalf("register = %+v", g)
	}
	if g.RegisteredAt.IsZero() || g.LastSeen.IsZero() {
		t.Fatalf("timestamps not stamped: %+v", g)
	}
	firstRegistered := g.RegisteredAt

	// Heartbeat updates version + cached hashes, preserves registered_at.
	hb, err := db.HeartbeatGateway(ctx, "gw-1", "v2", map[string]string{"role": "edge"}, []string{"h1", "h2"})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if hb.Version != "v2" || len(hb.CachedConfigHashes) != 2 || hb.CachedConfigHashes[0] != "h1" {
		t.Fatalf("heartbeat = %+v", hb)
	}
	if !hb.RegisteredAt.Equal(firstRegistered) {
		t.Errorf("registered_at changed on heartbeat: %v != %v", hb.RegisteredAt, firstRegistered)
	}

	// A heartbeat for an unknown gateway self-registers it.
	if _, err := db.HeartbeatGateway(ctx, "gw-2", "v1", nil, nil); err != nil {
		t.Fatalf("heartbeat self-register: %v", err)
	}

	// List returns both, id-ordered.
	list, err := db.ListGateways(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].ID != "gw-1" || list[1].ID != "gw-2" {
		t.Fatalf("list = %+v", list)
	}

	// Register is idempotent on gateway_id — refreshes, never duplicates.
	again, err := db.RegisterGateway(ctx, "gw-1", "v3", nil)
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if again.Version != "v3" {
		t.Errorf("re-register version = %q, want v3", again.Version)
	}
	if list2, _ := db.ListGateways(ctx); len(list2) != 2 {
		t.Errorf("idempotent register grew the fleet to %d", len(list2))
	}
}
