//go:build e2e

package configdb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// TestConfigDB_AdminCredentials exercises the Postgres-backed admin credential
// store against real Postgres: GetAdminHash on an empty store, first-seed
// inserts, and a second seed is an idempotent no-op (the existing row is the
// source of truth, so concurrent replica boots converge on one credential).
func TestConfigDB_AdminCredentials(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	if _, err := db.GetAdminHash(ctx, "admin"); !errors.Is(err, configdb.ErrAdminNotFound) {
		t.Fatalf("get missing admin: err = %v, want ErrAdminNotFound", err)
	}

	seeded, err := db.SeedAdmin(ctx, "admin", "hash-1")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !seeded {
		t.Error("first seed should report seeded=true")
	}

	got, err := db.GetAdminHash(ctx, "admin")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "hash-1" {
		t.Errorf("hash = %q, want hash-1", got)
	}

	// Second seed must not overwrite — the persisted row wins.
	seeded2, err := db.SeedAdmin(ctx, "admin", "hash-2")
	if err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if seeded2 {
		t.Error("second seed should report seeded=false")
	}
	if got2, _ := db.GetAdminHash(ctx, "admin"); got2 != "hash-1" {
		t.Errorf("hash changed on re-seed: %q, want hash-1", got2)
	}
}
