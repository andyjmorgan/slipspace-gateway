//go:build e2e

package configdb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// TestConfigDB_RequestBodies exercises the request_bodies store against real
// Postgres: idempotent upsert on the composite key, list-by-correlation in
// stable order, and ErrRequestBodyNotFound when absent.
func TestConfigDB_RequestBodies(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	if _, err := db.ListRequestBodies(ctx, "ghost"); !errors.Is(err, configdb.ErrRequestBodyNotFound) {
		t.Fatalf("missing: err = %v, want ErrRequestBodyNotFound", err)
	}

	for _, b := range []configdb.RequestBody{
		{CorrelationID: "c1", InstanceID: "gw-a", Seq: 1, TsNs: 10, Body: []byte(`{"part":"req"}`)},
		{CorrelationID: "c1", InstanceID: "gw-a", Seq: 2, TsNs: 20, Body: []byte(`{"part":"resp"}`)},
	} {
		if err := db.UpsertRequestBody(ctx, b); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	// Idempotent re-upsert (same key) updates rather than duplicates.
	if err := db.UpsertRequestBody(ctx, configdb.RequestBody{CorrelationID: "c1", InstanceID: "gw-a", Seq: 1, TsNs: 11, Body: []byte(`{"part":"req2"}`)}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	bodies, err := db.ListRequestBodies(ctx, "c1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("bodies = %d, want 2 (re-upsert must not duplicate)", len(bodies))
	}
	if bodies[0].Seq != 1 || string(bodies[0].Body) != `{"part":"req2"}` || bodies[0].TsNs != 11 {
		t.Errorf("body[0] = %+v (should reflect the re-upsert)", bodies[0])
	}
	if bodies[1].Seq != 2 {
		t.Errorf("order not (instance, seq): %+v", bodies)
	}
}
