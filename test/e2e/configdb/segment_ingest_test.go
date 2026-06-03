//go:build e2e

package configdb_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	cpconn "github.com/andyjmorgan/sluice-gateway/internal/connector/controlplane"
	cp "github.com/andyjmorgan/sluice-gateway/internal/controlplane"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// writeZstdSegment writes a real ndjson.zst segment whose single record carries
// the header fields the ingest handler keys on, and returns its path.
func writeZstdSegment(t *testing.T, line string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "1715000000000000001-7.ndjson.zst")
	f, err := os.Create(p) //nolint:gosec // test temp path
	if err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(enc, line+"\n"); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func newCPConnector(t *testing.T, url, secretRef string) *cpconn.Connector {
	t.Helper()
	c, err := cpconn.New(context.Background(), cpconn.Options{
		Config: contractsconfig.Connector{
			Type: contractsconfig.ConnectorTypeControlPlane, Name: "cp-audit",
			URL: url, SecretRef: secretRef, TimeoutMS: 5000,
		},
	})
	if err != nil {
		t.Fatalf("connector New: %v", err)
	}
	return c
}

// TestSegmentIngest_ConnectorToControlPlane proves the spool→CP leg end to end:
// the controlplane connector POSTs a real ndjson.zst segment to the token-gated
// ingest endpoint, and the CP decodes it into request_bodies.
func TestSegmentIngest_ConnectorToControlPlane(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	const token = "fleet-bootstrap-token"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := cp.BearerAuth(token, cp.NewSegmentIngestHandler(db, logger))
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	t.Setenv("TEST_CP_TOKEN", token)
	c := newCPConnector(t, srv.URL+"/api/v1/ingest/segment", "env:TEST_CP_TOKEN")

	seg := writeZstdSegment(t, `{"correlation_id":"corr-seg-1","instance_id":"gw-export","seq":7,"ts_ns":1715000000000000001,"model":"gpt-4o"}`)
	if err := c.Upload(ctx, cc.SealedSegment{Path: seg, DeliveryID: "d1", Connector: "cp-audit"}); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	bodies, err := db.ListRequestBodies(ctx, "corr-seg-1")
	if err != nil {
		t.Fatalf("list bodies: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("bodies = %d, want 1", len(bodies))
	}
	if bodies[0].InstanceID != "gw-export" || bodies[0].Seq != 7 {
		t.Errorf("stored body header = %+v", bodies[0])
	}
}

// TestSegmentIngest_WrongTokenRejected proves the ingest endpoint is token-gated:
// a connector with the wrong token gets a Permanent 401 and stores nothing.
func TestSegmentIngest_WrongTokenRejected(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(cp.BearerAuth("the-real-token", cp.NewSegmentIngestHandler(db, logger)))
	t.Cleanup(srv.Close)

	t.Setenv("TEST_CP_BAD", "the-wrong-token")
	c := newCPConnector(t, srv.URL+"/api/v1/ingest/segment", "env:TEST_CP_BAD")

	seg := writeZstdSegment(t, `{"correlation_id":"corr-seg-2","instance_id":"gw","seq":1,"ts_ns":1}`)
	err = c.Upload(ctx, cc.SealedSegment{Path: seg})
	var perm *cc.Permanent
	if !errors.As(err, &perm) {
		t.Fatalf("wrong token: want Permanent (401), got %v", err)
	}

	bodies, err := db.ListRequestBodies(ctx, "corr-seg-2")
	if err != nil {
		t.Fatalf("list bodies: %v", err)
	}
	if len(bodies) != 0 {
		t.Fatalf("bodies = %d, want 0 (rejected before ingest)", len(bodies))
	}
}
