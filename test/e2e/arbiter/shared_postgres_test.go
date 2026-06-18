//go:build e2e

// Package telemetry_test holds the through-Postgres e2e tests for the telemetry
// service's store. They live here (not next to internal/arbiter/store) so the
// store package carries no in-package test files and is gated via `make e2e`
// rather than the unit-coverage profile — the same split the scrapped CP used
// for configdb.
package arbiter_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/andyjmorgan/sluice-gateway/internal/arbiter/store"
)

// sharedDSN points at a Postgres container started once for the whole package.
// startErr is recorded when Docker is unavailable so tests Skip cleanly.
var (
	sharedDSN string
	startErr  error
)

func TestMain(m *testing.M) {
	dsn, terminate, err := startPostgres()
	if err != nil {
		startErr = err
		os.Exit(m.Run())
	}
	sharedDSN = dsn
	code := m.Run()
	terminate()
	os.Exit(code)
}

func startPostgres() (dsn string, terminate func(), err error) {
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			// TimescaleDB (not plain postgres) so migration 0007's CREATE
			// EXTENSION + continuous aggregates apply — same image + preload flag
			// as the attic deployment, so the test exercises the real engine.
			Image:        "timescale/timescaledb:2.27.2-pg16",
			Cmd:          []string{"-c", "shared_preload_libraries=timescaledb"},
			ExposedPorts: []string{"5432/tcp"},
			Env:          map[string]string{"POSTGRES_PASSWORD": "test", "POSTGRES_DB": "telemetry"},
			WaitingFor: wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithStartupTimeout(120 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		return "", nil, err
	}
	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return "", nil, err
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		_ = c.Terminate(ctx)
		return "", nil, err
	}
	return fmt.Sprintf("postgres://postgres:test@%s:%s/telemetry?sslmode=disable", host, port.Port()),
		func() { _ = c.Terminate(context.Background()) }, nil
}

// openStore opens the shared Postgres, skipping when Docker is unavailable.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	if startErr != nil {
		t.Skipf("postgres container unavailable: %v", startErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, err := store.Open(ctx, sharedDSN)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

// migratedStore opens the shared Postgres and applies migrations.
func migratedStore(t *testing.T) *store.Store {
	t.Helper()
	st := openStore(t)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}
