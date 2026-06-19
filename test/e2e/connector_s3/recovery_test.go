//go:build e2e

package connector_s3_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
	slipspaces3 "github.com/andyjmorgan/slipspace-gateway/internal/connector/s3"
	"github.com/andyjmorgan/slipspace-gateway/internal/spool"
)

// TestSpoolRecovery_ShipsOrphansToS3 proves issue #336 end-to-end against a
// real S3 destination: after a simulated crash leaves a segment stranded in
// uploading/ and a cleanly-closed leftover in active/, Spool.Start runs
// recovery before the goroutines launch — fishing the orphan back to sealed/
// and sealing the active leftover — so both records reach the SeaweedFS
// bucket. Before the fix, Recover had no production caller and both segments
// were stranded forever (silent audit/billing-record loss across restarts).
func TestSpoolRecovery_ShipsOrphansToS3(t *testing.T) {
	endpoint, cleanup := startSeaweedFS(t)
	t.Cleanup(cleanup)

	admin := seaweedAdminClient(t, endpoint)
	const bucket = "slipspace-recovery-test"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := admin.CreateBucket(ctx, &s3sdk.CreateBucketInput{
		Bucket: awssdk.String(bucket),
	}); err != nil && !strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") {
		t.Fatalf("CreateBucket: %v", err)
	}

	t.Setenv("SLIPSPACE_TEST_AK", seaweedAccess)
	t.Setenv("SLIPSPACE_TEST_SK", seaweedSecret)
	const connName = "recov-s3"
	conn, err := slipspaces3.New(ctx, slipspaces3.Options{
		Config: contractsconfig.Connector{
			Type:         "s3",
			Name:         connName,
			Bucket:       bucket,
			Prefix:       "recovered",
			Region:       seaweedRegion,
			EndpointURL:  endpoint,
			UsePathStyle: true,
			Auth: &contractsconfig.ConnectorAuth{
				Mode:               contractsconfig.AuthModeStatic,
				AccessKeyIDRef:     "env:SLIPSPACE_TEST_AK",
				SecretAccessKeyRef: "env:SLIPSPACE_TEST_SK",
			},
		},
		InstanceID: "recovery-instance",
	})
	if err != nil {
		t.Fatalf("s3 connector New: %v", err)
	}

	root := t.TempDir()
	s, err := spool.New(spool.Options{Root: root})
	if err != nil {
		t.Fatalf("spool.New: %v", err)
	}
	if err := s.RegisterTrack(spool.RegisterTrackOptions{
		Connector:          conn,
		UploadPollInterval: 100 * time.Millisecond,
	}); err != nil {
		t.Fatalf("RegisterTrack: %v", err)
	}

	// Simulate a prior crash: RegisterTrack created the track's state dirs.
	// Seed one orphan in uploading/ (killed mid-upload) and one cleanly
	// closed leftover in active/ (flushed but never sealed) — both must be
	// recovered by Start.
	trackRoot := filepath.Join(root, "records", connName)
	seedS3Segment(t, filepath.Join(trackRoot, "uploading"), 1, "orphan")
	seedS3Segment(t, filepath.Join(trackRoot, "active"), 2, "leftover")

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start (should run recovery, not fail): %v", err)
	}
	t.Cleanup(func() { s.Stop(5 * time.Second) })

	// Both recovered segments must land under the connector's prefix.
	if !waitForS3Objects(ctx, t, admin, bucket, "recovered/", 2, 10*time.Second) {
		t.Fatal("recovered orphan + active leftover did not reach the S3 bucket")
	}
}

// seedS3Segment writes one valid, closed segment containing a record with
// the given id into dir, simulating a segment left behind by a prior process.
func seedS3Segment(t *testing.T, dir string, seq uint64, id string) {
	t.Helper()
	seg, err := spool.OpenSegment(dir, seq, time.Now())
	if err != nil {
		t.Fatalf("OpenSegment(%s): %v", dir, err)
	}
	rec := cc.Record{
		V:             1,
		ID:            id,
		TsNs:          time.Now().UnixNano(),
		Seq:           seq,
		InstanceID:    "recovery-instance",
		CorrelationID: "c-" + id,
		Configuration: "test",
		Provider:      "openai",
		Protocol:      "chat_completions",
		Request:       cc.RequestPart{Method: "POST", Path: "/x"},
		Response:      cc.ResponsePart{Status: 200},
		SchemaVersion: cc.SchemaVersion,
	}
	if err := seg.Write(rec); err != nil {
		t.Fatalf("seg.Write: %v", err)
	}
	if err := seg.Close(); err != nil {
		t.Fatalf("seg.Close: %v", err)
	}
}

// waitForS3Objects polls the bucket until at least want objects exist under
// prefix, or the deadline passes.
func waitForS3Objects(ctx context.Context, t *testing.T, c *s3sdk.Client, bucket, prefix string, want int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := c.ListObjectsV2(ctx, &s3sdk.ListObjectsV2Input{
			Bucket: awssdk.String(bucket),
			Prefix: awssdk.String(prefix),
		})
		if err == nil && len(out.Contents) >= want {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}
