//go:build e2e

package configdb_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// One Postgres container is shared across the whole suite (started in TestMain);
// each test gets a fresh, empty database on it. This avoids spinning a separate
// container per test, which overwhelmed the Docker daemon under -race and caused
// flaky mapped-port timeouts as the suite grew.
var (
	sharedAdminDSN string
	containerErr   error
	dbCounter      atomic.Int64
)

func TestMain(m *testing.M) {
	dsn, terminate, err := startSharedPostgres()
	if err != nil {
		// No Docker / container failed: record it so each test Skips cleanly.
		containerErr = err
		os.Exit(m.Run())
	}
	sharedAdminDSN = dsn
	code := m.Run()
	terminate()
	os.Exit(code)
}

func startSharedPostgres() (dsn string, terminate func(), err error) {
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "postgres:16-alpine",
			ExposedPorts: []string{"5432/tcp"},
			Env:          map[string]string{"POSTGRES_PASSWORD": "test", "POSTGRES_DB": "cp"},
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
	return fmt.Sprintf("postgres://postgres:test@%s:%s/cp?sslmode=disable", host, port.Port()),
		func() { _ = c.Terminate(context.Background()) }, nil
}

// startPostgres returns a DSN to a fresh, empty database on the shared
// container, isolating each test from the others. Skips when Docker / the
// shared container is unavailable.
func startPostgres(t *testing.T) string {
	t.Helper()
	if containerErr != nil || sharedAdminDSN == "" {
		t.Skipf("shared postgres unavailable: %v", containerErr)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, sharedAdminDSN)
	if err != nil {
		t.Fatalf("connect shared postgres: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	name := fmt.Sprintf("t%d", dbCounter.Add(1))
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	return strings.Replace(sharedAdminDSN, "/cp?", "/"+name+"?", 1)
}
