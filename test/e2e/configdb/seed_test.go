//go:build e2e

package configdb_test

import (
	"context"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// TestConfigDB_SeedComposeServe proves the Phase 3b data path against real
// Postgres: seed file config into entities, publish a snapshot, then serve it
// (active version -> ResolveClosure -> per-config closures), and confirm the
// stored entities recompose into the same validated config.
func TestConfigDB_SeedComposeServe(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	resolved, err := config.LoadV2(ctx, "../../../config-dev")
	if err != nil {
		t.Skipf("config-dev not loadable: %v", err)
	}

	// Seed: entities + published snapshot (mirrors cmd/api's seedConfigDB).
	entities, err := controlplane.EntityFromConfig(resolved)
	if err != nil {
		t.Fatalf("EntityFromConfig: %v", err)
	}
	for _, e := range entities {
		if err := db.UpsertEntity(ctx, e.Kind, e.Name, e.Body, "seed"); err != nil {
			t.Fatalf("upsert %s/%s: %v", e.Kind, e.Name, err)
		}
	}
	body, err := config.MarshalConfig(resolved)
	if err != nil {
		t.Fatalf("MarshalConfig: %v", err)
	}
	if _, err := db.Publish(ctx, body, "seed"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Serve: the active published version resolves and closures compose.
	active, err := db.ActiveVersion(ctx)
	if err != nil {
		t.Fatalf("active version: %v", err)
	}
	rc, err := config.ResolveClosure(active.Body)
	if err != nil {
		t.Fatalf("resolve active: %v", err)
	}
	if len(rc.Configurations) != len(resolved.Configurations) {
		t.Fatalf("served configs = %d, want %d", len(rc.Configurations), len(resolved.Configurations))
	}
	if len(rc.APIKeys) > 0 {
		if _, _, err := config.MarshalClosure(rc, rc.APIKeys[0].Configuration); err != nil {
			t.Fatalf("compose closure from served config: %v", err)
		}
	}

	// Entities round-trip through the database back to a valid config.
	stored, err := db.ListEntities(ctx)
	if err != nil {
		t.Fatalf("list entities: %v", err)
	}
	if len(stored) != len(entities) {
		t.Fatalf("stored entities = %d, want %d", len(stored), len(entities))
	}
	recomposed, err := controlplane.EntitiesToConfig(stored)
	if err != nil {
		t.Fatalf("EntitiesToConfig from db: %v", err)
	}
	rb, err := config.MarshalConfig(recomposed)
	if err != nil {
		t.Fatalf("MarshalConfig(recomposed): %v", err)
	}
	if _, err := config.ResolveClosure(rb); err != nil {
		t.Fatalf("db-recomposed config does not validate: %v", err)
	}
	if len(recomposed.Backends) != len(resolved.Backends) {
		t.Fatalf("recomposed backends = %d, want %d", len(recomposed.Backends), len(resolved.Backends))
	}
}
