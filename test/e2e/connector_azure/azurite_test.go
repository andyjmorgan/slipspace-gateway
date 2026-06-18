//go:build e2e

package connector_azure_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/connector/azureblob"
)

const (
	azuriteImage    = "mcr.microsoft.com/azure-storage/azurite:latest"
	azuriteBlobPort = "10000/tcp"
	azuriteAccount  = "devstoreaccount1"
	// Azurite ships this key with its README; not a real credential.
	azuriteAccKey = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==" //nolint:gosec // G101: Azurite's documented public default key
	// startupTimeout covers the Azurite image pull + emulator cold-start +
	// the port-listening readiness wait, all under the same ctx. Under the
	// full parallel e2e suite the testcontainer runtime is contended (MinIO,
	// Azurite, and the gateway/mockllm processes all spinning up at once), so
	// 60s timed out at ~63s (#158). Matches the s3 connector's 180s (#129);
	// in isolation Azurite is up in ~5s, so this is pure headroom for load.
	startupTimeout = 180 * time.Second
)

// startAzurite spins up the Azurite emulator with the blob service
// exposed on a random host port. Returns the service URL the connector
// should target plus a cleanup func.
func startAzurite(t *testing.T) (serviceURL string, cleanup func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	req := testcontainers.ContainerRequest{
		Image:        azuriteImage,
		ExposedPorts: []string{azuriteBlobPort},
		Cmd:          []string{"azurite-blob", "--blobHost", "0.0.0.0", "--skipApiVersionCheck"},
		WaitingFor: wait.ForListeningPort(azuriteBlobPort).
			WithStartupTimeout(startupTimeout),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("azurite start: %v", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(context.Background())
		t.Fatalf("azurite host: %v", err)
	}
	port, err := c.MappedPort(ctx, azuriteBlobPort)
	if err != nil {
		_ = c.Terminate(context.Background())
		t.Fatalf("azurite port: %v", err)
	}
	serviceURL = fmt.Sprintf("http://%s:%s/%s", host, port.Port(), azuriteAccount)
	cleanup = func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		_ = c.Terminate(stopCtx)
	}
	return serviceURL, cleanup
}

// azuriteAdminClient builds a thin azblob client at the Azurite service
// URL using the well-known account key. Used by the test for setup
// (CreateContainer) and assertion (DownloadStream).
func azuriteAdminClient(t *testing.T, serviceURL string) *azblob.Client {
	t.Helper()
	cred, err := azblob.NewSharedKeyCredential(azuriteAccount, azuriteAccKey)
	if err != nil {
		t.Fatalf("shared key: %v", err)
	}
	c, err := azblob.NewClientWithSharedKeyCredential(serviceURL, cred, nil)
	if err != nil {
		t.Fatalf("admin client: %v", err)
	}
	return c
}

func TestAzurite_UploadRoundTrip(t *testing.T) {
	serviceURL, cleanup := startAzurite(t)
	t.Cleanup(cleanup)

	admin := azuriteAdminClient(t, serviceURL)
	const container = "sluice-test"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.CreateContainer(ctx, container, nil); err != nil &&
		!strings.Contains(err.Error(), "ContainerAlreadyExists") {
		t.Fatalf("CreateContainer: %v", err)
	}

	t.Setenv("SLUICE_TEST_AZ_KEY", azuriteAccKey)
	c, err := azureblob.New(ctx, azureblob.Options{
		Config: contractsconfig.Connector{
			Type:      "azure_blob",
			Name:      "azurite-test",
			Account:   azuriteAccount,
			Container: container,
			Prefix:    "refinement",
			Auth: &contractsconfig.ConnectorAuth{
				Mode:          contractsconfig.AuthModeAccountKey,
				AccountKeyRef: "env:SLUICE_TEST_AZ_KEY",
			},
		},
		InstanceID:         "test-instance",
		Clock:              func() time.Time { return time.Date(2026, 5, 22, 14, 30, 0, 0, time.UTC) },
		ServiceURLOverride: serviceURL,
	})
	if err != nil {
		t.Fatalf("azureblob.New: %v", err)
	}

	src := filepath.Join(t.TempDir(), "1715000000000000001-42.ndjson.zst")
	payload := []byte("synthetic zstd-framed batch payload")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	seg := cc.SealedSegment{
		Path:       src,
		TsMinNs:    time.Date(2026, 5, 22, 14, 30, 0, 0, time.UTC).UnixNano(),
		DeliveryID: "deliv-azure",
		Connector:  "azurite-test",
	}
	if err := c.Upload(ctx, seg); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	wantBlob := "refinement/records/instance=test-instance/date=2026-05-22/hour=14/1715000000000000001-42-deliv-azure.ndjson.zst"
	resp, err := admin.DownloadStream(ctx, container, wantBlob, nil)
	if err != nil {
		t.Fatalf("DownloadStream %q: %v", wantBlob, err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("payload mismatch:\nwant %q\n got %q", payload, got)
	}
}
