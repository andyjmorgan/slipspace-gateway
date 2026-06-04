//go:build integration

package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// sharedDSN points at a Postgres container started once for the whole package.
// startErr is recorded when Docker is unavailable so tests Skip cleanly rather
// than fail on a machine without a daemon.
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
			Image:        "postgres:16-alpine",
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

func openStore(t *testing.T) *Store {
	t.Helper()
	if startErr != nil {
		t.Skipf("postgres container unavailable: %v", startErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, err := Open(ctx, sharedDSN)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestOpenAndPing(t *testing.T) {
	st := openStore(t)
	if err := st.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestOpen_BadDSN(t *testing.T) {
	if startErr != nil {
		t.Skipf("postgres container unavailable: %v", startErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Open(ctx, "postgres://nope:nope@127.0.0.1:1/none?sslmode=disable"); err == nil {
		t.Fatal("Open with unreachable DSN: want error")
	}
}

func TestMigrate_AppliesAndIsIdempotent(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	v, err := st.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	want := migrations[len(migrations)-1].version
	if v != want {
		t.Fatalf("schema version = %d, want %d", v, want)
	}

	// Second run must be a no-op and leave the version unchanged.
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (second): %v", err)
	}
	v2, err := st.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion (second): %v", err)
	}
	if v2 != want {
		t.Fatalf("schema version after re-run = %d, want %d", v2, want)
	}
}
