//go:build e2e

package configdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// TestConfigDB_MetricPoints exercises the metric_points store against real
// Postgres: batch insert, newest-first capped list, and the empty-batch no-op.
func TestConfigDB_MetricPoints(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	if err := db.InsertMetricPoints(ctx, nil); err != nil {
		t.Fatalf("empty batch should be a no-op: %v", err)
	}

	base := time.Now().UTC()
	points := []configdb.MetricPoint{
		{Name: "sluice_requests_total", Labels: []byte(`{"model":"gpt-4o"}`), Value: 3, ObservedAt: base.Add(-2 * time.Second)},
		{Name: "sluice_requests_total", Labels: nil, Value: 7, ObservedAt: base},
	}
	if err := db.InsertMetricPoints(ctx, points); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := db.ListRecentMetricPoints(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("points = %d, want 2", len(got))
	}
	// Newest first.
	if got[0].Value != 7 {
		t.Errorf("newest value = %v, want 7", got[0].Value)
	}
	// Nil labels persisted as {}.
	if string(got[0].Labels) != "{}" {
		t.Errorf("nil labels = %s, want {}", got[0].Labels)
	}
}
