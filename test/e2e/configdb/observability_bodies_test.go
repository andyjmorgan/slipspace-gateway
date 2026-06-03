//go:build e2e

package configdb_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cp "github.com/andyjmorgan/sluice-gateway/internal/controlplane"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// TestObservabilityBodies_ReadEndpoint exercises the body-fetch read API end to
// end against real Postgres: captured bodies for a correlation id come back as
// embedded JSON, stably ordered by (instance_id, seq), and an unknown
// correlation id is a 404.
func TestObservabilityBodies_ReadEndpoint(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	for _, b := range []configdb.RequestBody{
		{CorrelationID: "corr-body", InstanceID: "gw-1", Seq: 2, TsNs: 200, Body: []byte(`{"phase":"response"}`)},
		{CorrelationID: "corr-body", InstanceID: "gw-1", Seq: 1, TsNs: 100, Body: []byte(`{"phase":"request"}`)},
	} {
		if err := db.UpsertRequestBody(ctx, b); err != nil {
			t.Fatalf("upsert body: %v", err)
		}
	}

	srv := httptest.NewServer(cp.NewObservabilityHandler(nil, db, nil, nil))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/v1/observability/bodies/corr-body")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var views []struct {
		Seq  uint64          `json:"seq"`
		Body json.RawMessage `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(views) != 2 || views[0].Seq != 1 || views[1].Seq != 2 {
		t.Fatalf("views = %+v (want seq-ordered 1,2)", views)
	}
	if string(views[0].Body) != `{"phase":"request"}` {
		t.Errorf("body[0] = %s", views[0].Body)
	}

	miss, err := http.Get(srv.URL + "/api/v1/observability/bodies/nope")
	if err != nil {
		t.Fatalf("GET miss: %v", err)
	}
	_ = miss.Body.Close()
	if miss.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown correlation: status = %d, want 404", miss.StatusCode)
	}
}
