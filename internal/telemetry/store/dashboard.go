package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DashboardParams is the input to the dashboard rollups: a half-open [From, To)
// window narrowed by the shared Filter. RecentFrom bounds the short trailing
// window (typically To-5m) the provider-health snapshot is computed over.
type DashboardParams struct {
	From       time.Time
	To         time.Time
	RecentFrom time.Time
	Filter     EventFilter
}

// DashboardTotals is the headline volume/outcome/token rollup over the window.
type DashboardTotals struct {
	Requests            int64
	RequestsSuccess     int64
	RequestsErrored     int64
	TokensIn            int64
	TokensOut           int64
	TokensCached        int64
	TokensCacheCreation int64
}

// DashboardLatency is the request-duration quantile set in milliseconds.
type DashboardLatency struct {
	P50 int64
	P95 int64
	P99 int64
}

// DashboardDimensionRow is one breakdown row keyed by a single dimension
// (provider, configuration).
type DashboardDimensionRow struct {
	Key          string
	Requests     int64
	P95LatencyMs int64
	ErrorRate    float64
}

// DashboardEndpointRow is one (provider, endpoint) breakdown row.
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

// DashboardFiredRow counts a single rule's matches (or a tag's applications)
// across the window and lists the distinct configurations that produced it.
type DashboardFiredRow struct {
	Key                  string
	Count                int64
	UsedByConfigurations []string
}

// DashboardProviderHealth is one provider's short-window health snapshot.
type DashboardProviderHealth struct {
	Provider    string
	Requests    int64
	ErrorRate   float64
	TotalErrors int64
}

// DashboardSummary is the full rollup the console dashboard renders. All
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

// QueryDashboardSummary computes the whole dashboard rollup over [From, To),
// one query per panel, each narrowed by the shared Filter.
func (s *Store) QueryDashboardSummary(ctx context.Context, p DashboardParams) (DashboardSummary, error) {
	var out DashboardSummary
	var err error
	if out.Totals, err = s.queryDashTotals(ctx, p); err != nil {
		return DashboardSummary{}, err
	}
	if out.Latency, err = s.queryDashLatency(ctx, p); err != nil {
		return DashboardSummary{}, err
	}
	if out.ByProvider, err = s.queryDashDimension(ctx, p, "backend"); err != nil {
		return DashboardSummary{}, err
	}
	if out.ByConfiguration, err = s.queryDashDimension(ctx, p, "configuration"); err != nil {
		return DashboardSummary{}, err
	}
	if out.ByEndpoint, err = s.queryDashEndpoint(ctx, p); err != nil {
		return DashboardSummary{}, err
	}
	if out.ByModel, err = s.queryDashModel(ctx, p); err != nil {
		return DashboardSummary{}, err
	}
	if out.RulesFired, err = s.queryDashFired(ctx, p, "rules_fired"); err != nil {
		return DashboardSummary{}, err
	}
	if out.TagsFired, err = s.queryDashFired(ctx, p, "tags"); err != nil {
		return DashboardSummary{}, err
	}
	if out.ProviderHealth, err = s.queryDashProviderHealth(ctx, p); err != nil {
		return DashboardSummary{}, err
	}
	return out, nil
}

// dashWindow builds the half-open window WHERE fragments + args seeded with the
// shared Filter. Bound order ($1=From, $2=To) is fixed so callers extend $3+.
func dashWindow(p DashboardParams) ([]string, []any) {
	where := []string{"observed_at >= $1", "observed_at < $2"}
	args := []any{p.From, p.To}
	return appendFilter(where, args, p.Filter)
}

func (s *Store) queryDashTotals(ctx context.Context, p DashboardParams) (DashboardTotals, error) {
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
	if err := s.db.QueryRow(ctx, q, args...).Scan(
		&t.Requests, &t.RequestsSuccess, &t.RequestsErrored,
		&t.TokensIn, &t.TokensOut, &t.TokensCached, &t.TokensCacheCreation,
	); err != nil {
		return DashboardTotals{}, fmt.Errorf("store: dashboard totals: %w", err)
	}
	return t, nil
}

func (s *Store) queryDashLatency(ctx context.Context, p DashboardParams) (DashboardLatency, error) {
	where, args := dashWindow(p)
	q := `
SELECT
  COALESCE(percentile_cont(0.50) WITHIN GROUP (ORDER BY latency_ms), 0),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0),
  COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ms), 0)
FROM request_events
WHERE ` + strings.Join(where, " AND ")

	var p50, p95, p99 float64
	if err := s.db.QueryRow(ctx, q, args...).Scan(&p50, &p95, &p99); err != nil {
		return DashboardLatency{}, fmt.Errorf("store: dashboard latency: %w", err)
	}
	return DashboardLatency{P50: round(p50), P95: round(p95), P99: round(p99)}, nil
}

// dashDimensionColumns is the allowlist for the single-column breakdowns.
var dashDimensionColumns = map[string]string{
	"backend":       "backend",
	"configuration": "configuration",
}

func (s *Store) queryDashDimension(ctx context.Context, p DashboardParams, dim string) ([]DashboardDimensionRow, error) {
	col, ok := dashDimensionColumns[dim]
	if !ok {
		return nil, fmt.Errorf("store: unknown dashboard dimension %q", dim)
	}
	where, args := dashWindow(p)
	q := fmt.Sprintf(`
SELECT %s AS grp, COUNT(*),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0),
  COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0)
FROM request_events
WHERE %s AND %s <> ''
GROUP BY grp
ORDER BY COUNT(*) DESC, grp`, col, strings.Join(where, " AND "), col)

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard dimension %s: %w", dim, err)
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
			return nil, fmt.Errorf("store: scan dashboard dimension: %w", err)
		}
		r.P95LatencyMs = round(p95)
		r.ErrorRate = rate(errCnt, r.Requests)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) queryDashEndpoint(ctx context.Context, p DashboardParams) ([]DashboardEndpointRow, error) {
	where, args := dashWindow(p)
	q := `
SELECT backend, protocol, COUNT(*),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0),
  COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0)
FROM request_events
WHERE ` + strings.Join(where, " AND ") + `
  AND backend <> '' AND protocol <> ''
GROUP BY backend, protocol
ORDER BY COUNT(*) DESC, backend, protocol`

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard endpoint: %w", err)
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
			return nil, fmt.Errorf("store: scan dashboard endpoint: %w", err)
		}
		r.P95LatencyMs = round(p95)
		r.ErrorRate = rate(errCnt, r.Requests)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) queryDashModel(ctx context.Context, p DashboardParams) ([]DashboardModelRow, error) {
	where, args := dashWindow(p)
	q := `
SELECT model, COALESCE(MAX(backend), ''), COUNT(*),
  COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0)
FROM request_events
WHERE ` + strings.Join(where, " AND ") + `
  AND model <> ''
GROUP BY model
ORDER BY COUNT(*) DESC, model`

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard model: %w", err)
	}
	defer rows.Close()

	var out []DashboardModelRow
	for rows.Next() {
		var r DashboardModelRow
		if err := rows.Scan(&r.Model, &r.Provider, &r.Requests, &r.TokensIn, &r.TokensOut); err != nil {
			return nil, fmt.Errorf("store: scan dashboard model: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// dashFiredArrayPaths is the allowlist for the detail JSONB array a fired-row
// aggregate unrolls.
var dashFiredArrayPaths = map[string]string{
	"rules_fired": "rules_fired",
	"tags":        "tags",
}

func (s *Store) queryDashFired(ctx context.Context, p DashboardParams, field string) ([]DashboardFiredRow, error) {
	path, ok := dashFiredArrayPaths[field]
	if !ok {
		return nil, fmt.Errorf("store: unknown fired field %q", field)
	}
	where, args := dashWindow(p)
	q := fmt.Sprintf(`
SELECT elem AS key, COUNT(*) AS cnt,
  COALESCE(array_agg(DISTINCT configuration) FILTER (WHERE configuration <> ''), ARRAY[]::text[]) AS configs
FROM request_events,
  LATERAL jsonb_array_elements_text(COALESCE(detail->'%s', '[]'::jsonb)) AS elem
WHERE %s
GROUP BY elem
ORDER BY cnt DESC, key`, path, strings.Join(where, " AND "))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard fired %s: %w", field, err)
	}
	defer rows.Close()

	var out []DashboardFiredRow
	for rows.Next() {
		r := DashboardFiredRow{UsedByConfigurations: []string{}}
		if err := rows.Scan(&r.Key, &r.Count, &r.UsedByConfigurations); err != nil {
			return nil, fmt.Errorf("store: scan dashboard fired: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) queryDashProviderHealth(ctx context.Context, p DashboardParams) ([]DashboardProviderHealth, error) {
	where := []string{"observed_at >= $1", "observed_at < $2", "backend <> ''"}
	args := []any{p.RecentFrom, p.To}
	where, args = appendFilter(where, args, p.Filter)
	q := `
SELECT backend, COUNT(*),
  COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0)
FROM request_events
WHERE ` + strings.Join(where, " AND ") + `
GROUP BY backend
ORDER BY backend`

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard provider health: %w", err)
	}
	defer rows.Close()

	var out []DashboardProviderHealth
	for rows.Next() {
		var h DashboardProviderHealth
		if err := rows.Scan(&h.Provider, &h.Requests, &h.TotalErrors); err != nil {
			return nil, fmt.Errorf("store: scan provider health: %w", err)
		}
		h.ErrorRate = rate(h.TotalErrors, h.Requests)
		out = append(out, h)
	}
	return out, rows.Err()
}

// DashboardSeriesParams bucketizes [From, To) into BucketSeconds-wide slots,
// narrowed by Filter. Empty buckets zero-fill so the chart axis stays continuous.
type DashboardSeriesParams struct {
	From          time.Time
	To            time.Time
	BucketSeconds int
	Filter        EventFilter
}

// DashboardSeriesBucket is one time bucket carrying every plottable series value.
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
// [From, To), one row per BucketSeconds-wide slot, each carrying request volume,
// errored count, token sums, and the latency quantile set.
func (s *Store) QueryDashboardSeries(ctx context.Context, p DashboardSeriesParams) ([]DashboardSeriesBucket, error) {
	if p.BucketSeconds <= 0 {
		return nil, fmt.Errorf("store: bucket_seconds must be positive")
	}
	where := []string{"e.observed_at >= $1", "e.observed_at < $2"}
	args := []any{p.From, p.To}
	where, args = appendFilter(where, args, p.Filter)
	args = append(args, p.BucketSeconds)
	w := len(args)
	q := fmt.Sprintf(`
WITH buckets AS (
  SELECT generate_series($1::timestamptz, $2::timestamptz - ($%d * INTERVAL '1 second'), ($%d * INTERVAL '1 second')) AS ts
)
SELECT b.ts,
  COUNT(e.correlation_id),
  COALESCE(SUM(CASE WHEN e.status_code >= 400 THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(e.tokens_in), 0),
  COALESCE(SUM(e.tokens_out), 0),
  COALESCE(percentile_cont(0.50) WITHIN GROUP (ORDER BY e.latency_ms), 0),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY e.latency_ms), 0),
  COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY e.latency_ms), 0)
FROM buckets b
LEFT JOIN request_events e
  ON e.observed_at >= b.ts AND e.observed_at < b.ts + ($%d * INTERVAL '1 second') AND %s
GROUP BY b.ts
ORDER BY b.ts`, w, w, w, strings.Join(where, " AND "))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard series: %w", err)
	}
	defer rows.Close()

	var out []DashboardSeriesBucket
	for rows.Next() {
		var (
			b             DashboardSeriesBucket
			p50, p95, p99 float64
		)
		if err := rows.Scan(&b.Ts, &b.Requests, &b.Errored, &b.TokensIn, &b.TokensOut, &p50, &p95, &p99); err != nil {
			return nil, fmt.Errorf("store: scan dashboard series: %w", err)
		}
		b.P50LatencyMs, b.P95LatencyMs, b.P99LatencyMs = round(p50), round(p95), round(p99)
		out = append(out, b)
	}
	return out, rows.Err()
}

// round rounds a float to the nearest non-negative int64 (half up).
func round(f float64) int64 { return int64(f + 0.5) }
