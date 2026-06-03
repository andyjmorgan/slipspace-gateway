package configdb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DashboardParams is the input to the fleet dashboard rollups: a half-open
// [From, To) window narrowed by the shared equality/status Filter. RecentFrom
// bounds the short trailing window (typically To-5m) the provider-health
// snapshot is computed over.
type DashboardParams struct {
	From       time.Time
	To         time.Time
	RecentFrom time.Time
	Filter     EventFilter
}

// DashboardTotals is the headline volume/outcome/token rollup over the whole
// window. Provider names come from the event's backend column; endpoint from
// protocol — the fleet equivalents of the gateway's gen_ai.provider.name /
// sluice.endpoint metric labels.
type DashboardTotals struct {
	Requests            int64
	RequestsSuccess     int64
	RequestsErrored     int64
	TokensIn            int64
	TokensOut           int64
	TokensCached        int64
	TokensCacheCreation int64
}

// DashboardLatency is the request-duration quantile set in milliseconds,
// computed with percentile_cont over latency_ms.
type DashboardLatency struct {
	P50 int64
	P95 int64
	P99 int64
}

// DashboardDimensionRow is one breakdown row keyed by a single dimension
// (provider, configuration). Requests + p95 + error rate mirror the gateway's
// by_provider / by_configuration cards.
type DashboardDimensionRow struct {
	Key          string
	Requests     int64
	P95LatencyMs int64
	ErrorRate    float64
}

// DashboardEndpointRow is one (provider, endpoint) breakdown row. Provider is
// repeated alongside Endpoint so the console can render a single readable label
// without a join — same shape the gateway's by_endpoint card consumes.
type DashboardEndpointRow struct {
	Provider     string
	Endpoint     string
	Requests     int64
	P95LatencyMs int64
	ErrorRate    float64
}

// DashboardModelRow is one model breakdown row carrying per-model token totals.
type DashboardModelRow struct {
	Model     string
	Provider  string
	Requests  int64
	TokensIn  int64
	TokensOut int64
}

// DashboardFiredRow counts a single rule's matches (or a single tag's
// applications) across the window and lists the distinct configurations that
// produced it. UsedByConfigurations is derived from the events themselves — the
// fleet-wide, DB-backed analog of the gateway's static configuration→rule
// attachment map.
type DashboardFiredRow struct {
	Key                  string
	Count                int64
	UsedByConfigurations []string
}

// DashboardProviderHealth is one provider's short-window health snapshot:
// request volume and error rate over RecentFrom..To, used to colour the
// per-provider health chips.
type DashboardProviderHealth struct {
	Provider    string
	Requests    int64
	ErrorRate   float64
	TotalErrors int64
}

// DashboardSummary is the full fleet rollup the rich console dashboard renders:
// headline totals, rates, latency quantiles, the four dimension breakdowns, the
// rules/tags-fired aggregates, and the per-provider recent-health snapshot. All
// aggregation runs in Postgres over request_events.
type DashboardSummary struct {
	Totals          DashboardTotals
	Latency         DashboardLatency
	ByProvider      []DashboardDimensionRow
	ByEndpoint      []DashboardEndpointRow
	ByConfiguration []DashboardDimensionRow
	ByModel         []DashboardModelRow
	RulesFired      []DashboardFiredRow
	TagsFired       []DashboardFiredRow
	ProviderHealth  []DashboardProviderHealth
}

// QueryDashboardSummary computes the whole fleet dashboard rollup over
// [From, To). It runs one query per panel — totals, latency quantiles, the four
// dimension breakdowns, the rules/tags-fired aggregates, and the recent
// provider-health snapshot — each narrowed by the shared Filter.
func (db *DB) QueryDashboardSummary(ctx context.Context, p DashboardParams) (DashboardSummary, error) {
	var out DashboardSummary
	var err error

	if out.Totals, err = db.queryDashTotals(ctx, p); err != nil {
		return DashboardSummary{}, err
	}
	if out.Latency, err = db.queryDashLatency(ctx, p); err != nil {
		return DashboardSummary{}, err
	}
	if out.ByProvider, err = db.queryDashDimension(ctx, p, "backend"); err != nil {
		return DashboardSummary{}, err
	}
	if out.ByConfiguration, err = db.queryDashDimension(ctx, p, "configuration"); err != nil {
		return DashboardSummary{}, err
	}
	if out.ByEndpoint, err = db.queryDashEndpoint(ctx, p); err != nil {
		return DashboardSummary{}, err
	}
	if out.ByModel, err = db.queryDashModel(ctx, p); err != nil {
		return DashboardSummary{}, err
	}
	if out.RulesFired, err = db.queryDashFired(ctx, p, "rules_fired"); err != nil {
		return DashboardSummary{}, err
	}
	if out.TagsFired, err = db.queryDashFired(ctx, p, "tags"); err != nil {
		return DashboardSummary{}, err
	}
	if out.ProviderHealth, err = db.queryDashProviderHealth(ctx, p); err != nil {
		return DashboardSummary{}, err
	}
	return out, nil
}

// dashWindow builds the half-open window WHERE fragments + args for a dashboard
// query, seeded with the shared equality/status Filter. The bound order
// ($1=From, $2=To) is fixed so callers can extend args with $3+ predictably.
func dashWindow(p DashboardParams) ([]string, []any) {
	where := []string{"observed_at >= $1", "observed_at < $2"}
	args := []any{p.From, p.To}
	return appendFilter(where, args, p.Filter)
}

func (db *DB) queryDashTotals(ctx context.Context, p DashboardParams) (DashboardTotals, error) {
	where, args := dashWindow(p)
	q := `
SELECT
  COUNT(*),
  COALESCE(SUM(CASE WHEN status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(tokens_in), 0),
  COALESCE(SUM(tokens_out), 0),
  COALESCE(SUM(tokens_cached), 0),
  COALESCE(SUM(tokens_cache_creation), 0)
FROM request_events
WHERE ` + strings.Join(where, " AND ")

	var t DashboardTotals
	if err := db.pool.QueryRow(ctx, q, args...).Scan(
		&t.Requests, &t.RequestsSuccess, &t.RequestsErrored,
		&t.TokensIn, &t.TokensOut, &t.TokensCached, &t.TokensCacheCreation,
	); err != nil {
		return DashboardTotals{}, fmt.Errorf("configdb: dashboard totals: %w", err)
	}
	return t, nil
}

func (db *DB) queryDashLatency(ctx context.Context, p DashboardParams) (DashboardLatency, error) {
	where, args := dashWindow(p)
	q := `
SELECT
  COALESCE(percentile_cont(0.50) WITHIN GROUP (ORDER BY latency_ms), 0),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0),
  COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ms), 0)
FROM request_events
WHERE ` + strings.Join(where, " AND ")

	var p50, p95, p99 float64
	if err := db.pool.QueryRow(ctx, q, args...).Scan(&p50, &p95, &p99); err != nil {
		return DashboardLatency{}, fmt.Errorf("configdb: dashboard latency: %w", err)
	}
	return DashboardLatency{
		P50: int64(p50 + 0.5),
		P95: int64(p95 + 0.5),
		P99: int64(p99 + 0.5),
	}, nil
}

// dashDimensionColumns is the allowlist for the single-column dimension
// breakdowns. The value never reaches SQL as text — only the mapped column,
// from this package-internal map, is interpolated.
var dashDimensionColumns = map[string]string{
	"backend":       "backend",
	"configuration": "configuration",
}

func (db *DB) queryDashDimension(ctx context.Context, p DashboardParams, dim string) ([]DashboardDimensionRow, error) {
	col, ok := dashDimensionColumns[dim]
	if !ok {
		return nil, fmt.Errorf("configdb: unknown dashboard dimension %q", dim)
	}
	where, args := dashWindow(p)
	q := fmt.Sprintf(`
SELECT
  %s AS grp,
  COUNT(*),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0),
  COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0)
FROM request_events
WHERE %s AND %s <> ''
GROUP BY grp
ORDER BY COUNT(*) DESC, grp`, col, strings.Join(where, " AND "), col)

	rows, err := db.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("configdb: dashboard dimension %s: %w", dim, err)
	}
	defer rows.Close()

	var out []DashboardDimensionRow
	for rows.Next() {
		var (
			r      DashboardDimensionRow
			p95    float64
			errCnt int64
		)
		if err := rows.Scan(&r.Key, &r.Requests, &p95, &errCnt); err != nil {
			return nil, fmt.Errorf("configdb: scan dashboard dimension: %w", err)
		}
		r.P95LatencyMs = int64(p95 + 0.5)
		r.ErrorRate = rate(errCnt, r.Requests)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (db *DB) queryDashEndpoint(ctx context.Context, p DashboardParams) ([]DashboardEndpointRow, error) {
	where, args := dashWindow(p)
	q := `
SELECT
  backend,
  protocol,
  COUNT(*),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0),
  COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0)
FROM request_events
WHERE ` + strings.Join(where, " AND ") + `
  AND backend <> '' AND protocol <> ''
GROUP BY backend, protocol
ORDER BY COUNT(*) DESC, backend, protocol`

	rows, err := db.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("configdb: dashboard endpoint: %w", err)
	}
	defer rows.Close()

	var out []DashboardEndpointRow
	for rows.Next() {
		var (
			r      DashboardEndpointRow
			p95    float64
			errCnt int64
		)
		if err := rows.Scan(&r.Provider, &r.Endpoint, &r.Requests, &p95, &errCnt); err != nil {
			return nil, fmt.Errorf("configdb: scan dashboard endpoint: %w", err)
		}
		r.P95LatencyMs = int64(p95 + 0.5)
		r.ErrorRate = rate(errCnt, r.Requests)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (db *DB) queryDashModel(ctx context.Context, p DashboardParams) ([]DashboardModelRow, error) {
	where, args := dashWindow(p)
	// max(backend) picks one provider label per model; a model is served by a
	// single backend in practice, so the aggregate is just a grouping convenience.
	q := `
SELECT
  model,
  COALESCE(MAX(backend), ''),
  COUNT(*),
  COALESCE(SUM(tokens_in), 0),
  COALESCE(SUM(tokens_out), 0)
FROM request_events
WHERE ` + strings.Join(where, " AND ") + `
  AND model <> ''
GROUP BY model
ORDER BY COUNT(*) DESC, model`

	rows, err := db.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("configdb: dashboard model: %w", err)
	}
	defer rows.Close()

	var out []DashboardModelRow
	for rows.Next() {
		var r DashboardModelRow
		if err := rows.Scan(&r.Model, &r.Provider, &r.Requests, &r.TokensIn, &r.TokensOut); err != nil {
			return nil, fmt.Errorf("configdb: scan dashboard model: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// dashFiredArrayPaths is the allowlist for the detail JSONB array a fired-row
// aggregate unrolls. The mapped path never reaches SQL as user input — only the
// value from this package-internal map is interpolated.
var dashFiredArrayPaths = map[string]string{
	"rules_fired": "rules_fired",
	"tags":        "tags",
}

// queryDashFired unrolls a string array inside the detail JSONB (detail.tags or
// detail.rules_fired), counting occurrences per element across the window and
// collecting the distinct configurations each element appeared under. This is
// the fleet, DB-backed analog of the gateway's static configuration→rule/tag
// attachment join.
func (db *DB) queryDashFired(ctx context.Context, p DashboardParams, field string) ([]DashboardFiredRow, error) {
	path, ok := dashFiredArrayPaths[field]
	if !ok {
		return nil, fmt.Errorf("configdb: unknown fired field %q", field)
	}
	where, args := dashWindow(p)
	q := fmt.Sprintf(`
SELECT
  elem AS key,
  COUNT(*) AS cnt,
  COALESCE(
    array_agg(DISTINCT configuration) FILTER (WHERE configuration <> ''),
    ARRAY[]::text[]
  ) AS configs
FROM request_events,
  LATERAL jsonb_array_elements_text(COALESCE(detail->'%s', '[]'::jsonb)) AS elem
WHERE %s
GROUP BY elem
ORDER BY cnt DESC, key`, path, strings.Join(where, " AND "))

	rows, err := db.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("configdb: dashboard fired %s: %w", field, err)
	}
	defer rows.Close()

	var out []DashboardFiredRow
	for rows.Next() {
		r := DashboardFiredRow{UsedByConfigurations: []string{}}
		if err := rows.Scan(&r.Key, &r.Count, &r.UsedByConfigurations); err != nil {
			return nil, fmt.Errorf("configdb: scan dashboard fired: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (db *DB) queryDashProviderHealth(ctx context.Context, p DashboardParams) ([]DashboardProviderHealth, error) {
	// Recent-window health uses RecentFrom..To rather than the full window, so
	// args are seeded independently of dashWindow's From/To bound order.
	where := []string{"observed_at >= $1", "observed_at < $2", "backend <> ''"}
	args := []any{p.RecentFrom, p.To}
	where, args = appendFilter(where, args, p.Filter)
	q := `
SELECT
  backend,
  COUNT(*),
  COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0)
FROM request_events
WHERE ` + strings.Join(where, " AND ") + `
GROUP BY backend
ORDER BY backend`

	rows, err := db.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("configdb: dashboard provider health: %w", err)
	}
	defer rows.Close()

	var out []DashboardProviderHealth
	for rows.Next() {
		var h DashboardProviderHealth
		if err := rows.Scan(&h.Provider, &h.Requests, &h.TotalErrors); err != nil {
			return nil, fmt.Errorf("configdb: scan provider health: %w", err)
		}
		h.ErrorRate = rate(h.TotalErrors, h.Requests)
		out = append(out, h)
	}
	return out, rows.Err()
}

// DashboardSeriesParams is the input to QueryDashboardSeries: a half-open
// [From, To) window bucketed into BucketSeconds-wide slots, narrowed by Filter.
// Buckets with no data zero-fill via generate_series so the chart axis stays
// continuous.
type DashboardSeriesParams struct {
	From          time.Time
	To            time.Time
	BucketSeconds int
	Filter        EventFilter
}

// DashboardSeriesBucket is one time bucket carrying every series value the
// timeseries endpoint can plot, so a single scan backs the requests / error
// rate / latency / token curves without a query per metric.
type DashboardSeriesBucket struct {
	Ts           time.Time
	Requests     int64
	Errored      int64
	TokensIn     int64
	TokensOut    int64
	P50LatencyMs int64
	P95LatencyMs int64
	P99LatencyMs int64
}

// QueryDashboardSeries computes a zero-filled per-bucket time series over
// [From, To), one row per BucketSeconds-wide slot. Each bucket carries request
// volume, errored count, token sums, and the latency quantile set, so the
// handler can derive any of the gateway's timeseries curves from a single scan.
func (db *DB) QueryDashboardSeries(ctx context.Context, p DashboardSeriesParams) ([]DashboardSeriesBucket, error) {
	if p.BucketSeconds <= 0 {
		return nil, fmt.Errorf("configdb: bucket_seconds must be positive")
	}
	where := []string{"e.observed_at >= $1", "e.observed_at < $2"}
	args := []any{p.From, p.To}
	where, args = appendFilter(where, args, p.Filter)
	args = append(args, p.BucketSeconds)
	widthIdx := len(args)
	q := fmt.Sprintf(`
WITH buckets AS (
  SELECT generate_series(
    $1::timestamptz,
    $2::timestamptz - ($%d * INTERVAL '1 second'),
    ($%d * INTERVAL '1 second')
  ) AS ts
)
SELECT
  b.ts,
  COUNT(e.correlation_id),
  COALESCE(SUM(CASE WHEN e.status_code >= 400 THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(e.tokens_in), 0),
  COALESCE(SUM(e.tokens_out), 0),
  COALESCE(percentile_cont(0.50) WITHIN GROUP (ORDER BY e.latency_ms), 0),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY e.latency_ms), 0),
  COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY e.latency_ms), 0)
FROM buckets b
LEFT JOIN request_events e
  ON e.observed_at >= b.ts
 AND e.observed_at < b.ts + ($%d * INTERVAL '1 second')
 AND %s
GROUP BY b.ts
ORDER BY b.ts`, widthIdx, widthIdx, widthIdx, strings.Join(where, " AND "))

	rows, err := db.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("configdb: dashboard series: %w", err)
	}
	defer rows.Close()

	var out []DashboardSeriesBucket
	for rows.Next() {
		var (
			b             DashboardSeriesBucket
			p50, p95, p99 float64
		)
		if err := rows.Scan(&b.Ts, &b.Requests, &b.Errored, &b.TokensIn, &b.TokensOut, &p50, &p95, &p99); err != nil {
			return nil, fmt.Errorf("configdb: scan dashboard series: %w", err)
		}
		b.P50LatencyMs = int64(p50 + 0.5)
		b.P95LatencyMs = int64(p95 + 0.5)
		b.P99LatencyMs = int64(p99 + 0.5)
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetRequestRecord returns the latest captured connector Record for a
// correlation id — the body envelope the message-inspector drill-in renders.
// When multiple bodies exist (retries, request+response phases), the one with
// the highest (ts_ns, seq) wins, since it carries the most complete response.
// ErrRequestBodyNotFound when none was captured.
func (db *DB) GetRequestRecord(ctx context.Context, correlationID string) (json.RawMessage, error) {
	bodies, err := db.ListRequestBodies(ctx, correlationID)
	if err != nil {
		return nil, err
	}
	best := bodies[0].Body
	bestTs, bestSeq := bodies[0].TsNs, bodies[0].Seq
	for _, b := range bodies[1:] {
		if b.TsNs > bestTs || (b.TsNs == bestTs && b.Seq > bestSeq) {
			best, bestTs, bestSeq = b.Body, b.TsNs, b.Seq
		}
	}
	return json.RawMessage(best), nil
}
