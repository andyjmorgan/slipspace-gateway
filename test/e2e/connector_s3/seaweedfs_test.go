//go:build e2e

package connector_s3_test

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

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
	slipspaces3 "github.com/andyjmorgan/slipspace-gateway/internal/connector/s3"
)

// SeaweedFS exposes S3 on 8333 by default when started with `-s3`.
const (
	seaweedImage  = "chrislusf/seaweedfs:3.71"
	seaweedS3Port = "8333/tcp"
	seaweedRegion = "us-east-1"
	seaweedAccess = "any" //nolint:gosec // SeaweedFS default anonymous creds; not a real secret
	seaweedSecret = "any" //nolint:gosec // see above

	// startupTimeout gives the all-in-one `weed server -s3` (master + volume
	// + filer + s3) room to answer HTTP on the S3 port. The HTTP readiness
	// probe needs the full S3 layer up — slower than a bare port-listening
	// wait — so on loaded CI runners 60s timed out deterministically (#129);
	// 180s is comfortable headroom.
	startupTimeout = 180 * time.Second
)

// startSeaweedFS spins up a SeaweedFS testcontainer with S3 enabled and
// returns the host endpoint (http://<host>:<port>) plus a cleanup
// function. The container's S3 layer accepts anonymous PUT/GET by
// default — that's what makes it a friendly target for connector
// tests.
func startSeaweedFS(t *testing.T) (endpoint string, cleanup func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	req := testcontainers.ContainerRequest{
		Image:        seaweedImage,
		ExposedPorts: []string{seaweedS3Port},
		// `weed server -s3` is the all-in-one entrypoint (master + volume
		// + filer + s3). Single-binary, no orchestration needed for
		// per-test isolation.
		Cmd: []string{"server", "-s3", "-s3.port=8333", "-ip=0.0.0.0"},
		WaitingFor: wait.ForHTTP("/").
			WithPort(seaweedS3Port).
			WithStatusCodeMatcher(func(status int) bool {
				// SeaweedFS S3 returns 403 on the bucket-list root with
				// the default config; that's still proof the listener is
				// alive.
				return status == 200 || status == 403 || status == 404
			}).
			WithStartupTimeout(startupTimeout),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("seaweedfs start: %v", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(context.Background())
		t.Fatalf("seaweedfs host: %v", err)
	}
	port, err := c.MappedPort(ctx, seaweedS3Port)
	if err != nil {
		_ = c.Terminate(context.Background())
		t.Fatalf("seaweedfs port: %v", err)
	}
	endpoint = fmt.Sprintf("http://%s:%s", host, port.Port())
	cleanup = func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		_ = c.Terminate(stopCtx)
	}
	return endpoint, cleanup
}

// seaweedAdminClient builds a thin S3 client against the SeaweedFS
// endpoint with the same auth knobs the connector itself uses.
// Used by the test for setup (create bucket) and assertion (read
// back uploaded objects).
func seaweedAdminClient(t *testing.T, endpoint string) *s3sdk.Client {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(seaweedRegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			seaweedAccess, seaweedSecret, "")),
	)
	if err != nil {
		t.Fatalf("admin config: %v", err)
	}
	return s3sdk.NewFromConfig(cfg, func(o *s3sdk.Options) {
		o.BaseEndpoint = awssdk.String(endpoint)
		o.UsePathStyle = true
	})
}

func TestSeaweedFS_UploadRoundTrip(t *testing.T) {
	endpoint, cleanup := startSeaweedFS(t)
	t.Cleanup(cleanup)

	admin := seaweedAdminClient(t, endpoint)
	const bucket = "slipspace-test"

	// Create the bucket. SeaweedFS auto-creates buckets on PUT in some
	// modes but it's cleaner to be explicit.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.CreateBucket(ctx, &s3sdk.CreateBucketInput{
		Bucket: awssdk.String(bucket),
	}); err != nil && !strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") {
		t.Fatalf("CreateBucket: %v", err)
	}

	// Connector under test, pointed at SeaweedFS via endpoint_url +
	// path-style.
	t.Setenv("SLIPSPACE_TEST_AK", seaweedAccess)
	t.Setenv("SLIPSPACE_TEST_SK", seaweedSecret)
	c, err := slipspaces3.New(ctx, slipspaces3.Options{
		Config: contractsconfig.Connector{
			Type:         "s3",
			Name:         "seaweedfs-test",
			Bucket:       bucket,
			Prefix:       "refinement",
			Region:       seaweedRegion,
			EndpointURL:  endpoint,
			UsePathStyle: true,
			Auth: &contractsconfig.ConnectorAuth{
				Mode:               contractsconfig.AuthModeStatic,
				AccessKeyIDRef:     "env:SLIPSPACE_TEST_AK",
				SecretAccessKeyRef: "env:SLIPSPACE_TEST_SK",
			},
		},
		InstanceID: "test-instance",
		Clock:      func() time.Time { return time.Date(2026, 5, 22, 14, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Build a sealed segment fixture on disk.
	src := filepath.Join(t.TempDir(), "1715000000000000001-42.ndjson.zst")
	payload := []byte("synthetic zstd-framed batch payload")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	seg := cc.SealedSegment{
		Path:       src,
		TsMinNs:    time.Date(2026, 5, 22, 14, 30, 0, 0, time.UTC).UnixNano(),
		DeliveryID: "deliv-test",
		Connector:  "seaweedfs-test",
	}
	if err := c.Upload(ctx, seg); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// Assert: the object lives at the design-note key.
	wantKey := "refinement/records/instance=test-instance/date=2026-05-22/hour=14/1715000000000000001-42-deliv-test.ndjson.zst"
	out, err := admin.GetObject(ctx, &s3sdk.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(wantKey),
	})
	if err != nil {
		t.Fatalf("GetObject %q: %v", wantKey, err)
	}
	t.Cleanup(func() { _ = out.Body.Close() })
	got, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("payload mismatch:\nwant %q\n got %q", payload, got)
	}
}
