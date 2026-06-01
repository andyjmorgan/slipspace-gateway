//go:build e2e

// Package configdb_test exercises the control plane's Postgres config store
// against a real Postgres via testcontainers (real-over-mock, per CLAUDE.md).
package configdb_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_PASSWORD": "test",
			"POSTGRES_DB":       "cp",
		},
		WaitingFor: wait.ForAll(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
			wait.ForListeningPort("5432/tcp"),
		).WithStartupTimeout(90 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	return fmt.Sprintf("postgres://postgres:test@%s:%s/cp?sslmode=disable", host, port.Port())
}

func TestConfigDB_EntityLifecycleAndAudit(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	// Missing entity.
	if _, err := db.GetEntity(ctx, "backend", "ghost"); !errors.Is(err, configdb.ErrEntityNotFound) {
		t.Fatalf("get missing: err = %v, want ErrEntityNotFound", err)
	}
	if err := db.DeleteEntity(ctx, "backend", "ghost", "tester"); !errors.Is(err, configdb.ErrEntityNotFound) {
		t.Fatalf("delete missing: err = %v, want ErrEntityNotFound", err)
	}

	// Create.
	if err := db.UpsertEntity(ctx, "backend", "openai", []byte(`{"base_url":"https://api.openai.com"}`), "alice"); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := db.GetEntity(ctx, "backend", "openai")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "openai" || got.UpdatedBy != "alice" {
		t.Fatalf("entity = %+v", got)
	}

	// Update.
	if err := db.UpsertEntity(ctx, "backend", "openai", []byte(`{"base_url":"https://api.openai.com/v2"}`), "bob"); err != nil {
		t.Fatalf("update: %v", err)
	}

	// A second entity + list.
	if err := db.UpsertEntity(ctx, "configuration", "dev", []byte(`{"bindings":[]}`), "alice"); err != nil {
		t.Fatalf("create config: %v", err)
	}
	ents, err := db.ListEntities(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ents) != 2 {
		t.Fatalf("entities = %d, want 2", len(ents))
	}

	// Delete.
	if err := db.DeleteEntity(ctx, "backend", "openai", "carol"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Audit: create, update, create, delete = 4 changes, newest-first.
	changes, err := db.ListChanges(ctx, 0)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(changes) != 4 {
		t.Fatalf("changes = %d, want 4", len(changes))
	}
	if changes[0].Op != "delete" || changes[0].ChangedBy != "carol" {
		t.Fatalf("newest change = %+v, want delete by carol", changes[0])
	}
	if limited, _ := db.ListChanges(ctx, 2); len(limited) != 2 {
		t.Fatalf("limited changes = %d, want 2", len(limited))
	}
}

func TestConfigDB_PublishVersioningAndRollback(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	if _, err := db.ActiveVersion(ctx); !errors.Is(err, configdb.ErrNoActiveConfig) {
		t.Fatalf("active before publish: err = %v, want ErrNoActiveConfig", err)
	}

	v1, err := db.Publish(ctx, []byte("configurations:\n  dev: {}\n"), "alice")
	if err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	v2, err := db.Publish(ctx, []byte("configurations:\n  prod: {}\n"), "bob")
	if err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	if v1.ID == v2.ID || v1.Hash == v2.Hash {
		t.Fatalf("distinct publishes collided: %s / %s", v1.ID, v2.ID)
	}

	active, err := db.ActiveVersion(ctx)
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active.ID != v2.ID {
		t.Fatalf("active = %s, want latest %s", active.ID, v2.ID)
	}
	if active.ParentID == nil || *active.ParentID != v1.ID {
		t.Fatalf("v2 parent = %v, want v1 %s", active.ParentID, v1.ID)
	}

	// Re-publishing v1's body dedups to the existing snapshot and re-activates it.
	again, err := db.Publish(ctx, []byte("configurations:\n  dev: {}\n"), "alice")
	if err != nil {
		t.Fatalf("re-publish v1: %v", err)
	}
	if again.ID != v1.ID {
		t.Fatalf("dedup failed: got %s, want %s", again.ID, v1.ID)
	}
	if versions, _ := db.ListVersions(ctx); len(versions) != 2 {
		t.Fatalf("versions = %d, want 2 (dedup, not 3)", len(versions))
	}

	// Rollback to v2.
	if err := db.ActivateVersion(ctx, v2.ID); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if a, _ := db.ActiveVersion(ctx); a.ID != v2.ID {
		t.Fatalf("after rollback active = %s, want %s", a.ID, v2.ID)
	}
	if err := db.ActivateVersion(ctx, "nonexistent"); !errors.Is(err, configdb.ErrVersionNotFound) {
		t.Fatalf("activate unknown: err = %v, want ErrVersionNotFound", err)
	}
}
