//go:build e2e

package configdb_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	connc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
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

// TestObservabilityRich_HTTP drives the gateway-shaped fleet dashboard +
// message-inspector endpoints through the real ObservabilityHandler against
// real Postgres — proving the *configdb.DB type-assertion lights up the rich
// routes and they decode into the gateway's contracts/admin shapes.
func TestObservabilityRich_HTTP(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	now := time.Now().UTC()
	if err := db.UpsertRequestEvent(ctx, configdb.RequestEvent{
		CorrelationID: "rich-1",
		Configuration: "prod",
		Backend:       "openai",
		Protocol:      "chat",
		Model:         "gpt-4o",
		Method:        "POST",
		StatusCode:    200,
		LatencyMs:     25,
		TokensIn:      10,
		TokensOut:     20,
		Streaming:     true,
		ObservedAt:    now.Add(-1 * time.Minute),
		Detail:        detailJSON(t, []string{"env:prod"}, []string{"redirect"}),
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	bodyRec := connc.Record{
		CorrelationID: "rich-1",
		Request:       connc.RequestPart{Body: json.RawMessage(`{"model":"gpt-4o"}`), BodyBytes: 18},
		Response:      connc.ResponsePart{Status: 200, Body: json.RawMessage(`{"id":"x"}`), BodyBytes: 10},
	}
	bodyJSON, _ := json.Marshal(bodyRec)
	if err := db.UpsertRequestBody(ctx, configdb.RequestBody{CorrelationID: "rich-1", InstanceID: "gw", Seq: 1, TsNs: 1, Body: bodyJSON}); err != nil {
		t.Fatalf("seed body: %v", err)
	}

	srv := httptest.NewServer(cp.NewObservabilityHandler(db, db, db, nil))
	t.Cleanup(srv.Close)

	t.Run("dashboard summary", func(t *testing.T) {
		var s adminc.DashboardSummary
		getJSON(t, srv.URL+"/api/v1/observability/dashboard/summary?window=1h", &s)
		if s.Window != "1h" || s.Totals.Requests != 1 || s.Totals.TokensIn != 10 {
			t.Fatalf("summary = %+v", s)
		}
		if len(s.ByProvider) != 1 || s.ByProvider[0].Provider != "openai" {
			t.Fatalf("by_provider = %+v", s.ByProvider)
		}
		if len(s.RulesFired) != 1 || s.RulesFired[0].RuleName != "redirect" {
			t.Fatalf("rules_fired = %+v", s.RulesFired)
		}
	})

	t.Run("dashboard timeseries", func(t *testing.T) {
		var ts adminc.DashboardTimeseries
		getJSON(t, srv.URL+"/api/v1/observability/dashboard/timeseries?window=1h&series=rps", &ts)
		if len(ts.Series) != 1 || ts.Series[0].Unit != "req/s" {
			t.Fatalf("timeseries = %+v", ts)
		}
	})

	t.Run("messages recent", func(t *testing.T) {
		var resp adminc.MessagesRecentResponse
		getJSON(t, srv.URL+"/api/v1/observability/messages/recent?limit=10", &resp)
		if len(resp.Entries) != 1 {
			t.Fatalf("entries = %d, want 1", len(resp.Entries))
		}
		e := resp.Entries[0]
		if e.EventID != "rich-1" || e.Provider != "openai" || e.Endpoint != "chat" || !e.Streaming {
			t.Fatalf("entry = %+v", e)
		}
		if len(e.Tags) != 1 || e.Tags[0] != "env:prod" {
			t.Fatalf("tags = %+v", e.Tags)
		}
	})

	t.Run("message body", func(t *testing.T) {
		var d adminc.MessageBodyDetail
		getJSON(t, srv.URL+"/api/v1/observability/messages/rich-1/body", &d)
		if d.EventID != "rich-1" || d.Request != `{"model":"gpt-4o"}` || d.Response != `{"id":"x"}` {
			t.Fatalf("body = %+v", d)
		}
	})

	t.Run("message body 404", func(t *testing.T) {
		resp, gerr := http.Get(srv.URL + "/api/v1/observability/messages/ghost/body")
		if gerr != nil {
			t.Fatalf("GET: %v", gerr)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
}

// getJSON GETs url, asserts 200, and decodes the body into v.
func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // test-only httptest.Server URL, not attacker-controlled
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}
