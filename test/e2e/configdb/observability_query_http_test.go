//go:build e2e

package configdb_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cp "github.com/andyjmorgan/sluice-gateway/internal/controlplane"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// TestObservabilityQuery_StatsAndEvents_HTTP drives the new /stats and the
// reworked /events envelope through the real ObservabilityHandler against real
// Postgres — the wire contract the console is built against.
func TestObservabilityQuery_StatsAndEvents_HTTP(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for i, sc := range []int{200, 200, 404, 500} {
		if err := db.UpsertRequestEvent(ctx, configdb.RequestEvent{
			CorrelationID: string(rune('a' + i)),
			Configuration: "prod",
			Model:         "gpt-4o",
			Backend:       "openai",
			Protocol:      "chat",
			StatusCode:    sc,
			LatencyMs:     int64(10 * (i + 1)),
			TokensIn:      int64(i + 1),
			TokensOut:     int64(i + 1),
			ObservedAt:    base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	srv := httptest.NewServer(cp.NewObservabilityHandler(db, db, db, nil))
	t.Cleanup(srv.Close)

	t.Run("stats", func(t *testing.T) {
		resp, gerr := http.Get(srv.URL + "/api/v1/observability/stats?from=2026-06-01T12:00:00Z&to=2026-06-01T12:15:00Z&bucket=300&group_by=model")
		if gerr != nil {
			t.Fatalf("GET: %v", gerr)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var body struct {
			BucketSeconds int `json:"bucket_seconds"`
			Totals        struct {
				Requests  int64   `json:"requests"`
				Errors    int64   `json:"errors"`
				ErrorRate float64 `json:"error_rate"`
			} `json:"totals"`
			Series []struct {
				Requests  int64 `json:"requests"`
				Status2xx int64 `json:"status_2xx"`
			} `json:"series"`
			Top []struct {
				Key      string `json:"key"`
				Requests int64  `json:"requests"`
			} `json:"top"`
		}
		if derr := json.NewDecoder(resp.Body).Decode(&body); derr != nil {
			t.Fatalf("decode: %v", derr)
		}
		if body.BucketSeconds != 300 || body.Totals.Requests != 4 || body.Totals.Errors != 2 {
			t.Fatalf("totals = %+v", body.Totals)
		}
		if len(body.Series) != 3 {
			t.Fatalf("series len = %d, want 3", len(body.Series))
		}
		if len(body.Top) != 1 || body.Top[0].Key != "gpt-4o" || body.Top[0].Requests != 4 {
			t.Fatalf("top = %+v", body.Top)
		}
	})

	t.Run("events envelope + pagination", func(t *testing.T) {
		resp, gerr := http.Get(srv.URL + "/api/v1/observability/events?limit=2&configuration=prod")
		if gerr != nil {
			t.Fatalf("GET: %v", gerr)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var page struct {
			Events []struct {
				CorrelationID string `json:"correlation_id"`
			} `json:"events"`
			NextCursor string `json:"next_cursor"`
		}
		if derr := json.NewDecoder(resp.Body).Decode(&page); derr != nil {
			t.Fatalf("decode: %v", derr)
		}
		if len(page.Events) != 2 {
			t.Fatalf("events len = %d, want 2", len(page.Events))
		}
		// Newest first: d (12:03) then c (12:02).
		if page.Events[0].CorrelationID != "d" || page.Events[1].CorrelationID != "c" {
			t.Fatalf("order = %s,%s want d,c", page.Events[0].CorrelationID, page.Events[1].CorrelationID)
		}
		if page.NextCursor == "" {
			t.Fatal("next_cursor empty, want a further page")
		}
	})
}
