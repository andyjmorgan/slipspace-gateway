package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Dashboard rollups read the TimescaleDB continuous aggregates (migration 0007),
// never the request_events entity. Each CAGG buckets the sluice meter feed at one
// minute (count/sum only — no percentiles; MVP runs plain timescaledb without the
// percentile toolkit, so latency quantiles are dropped entirely). The dashboard
// re-buckets those 1-minute rows up to the requested width. This stays inside
// invariant #4: the meters are the dashboard's data, never the record spool.
//
// cagg_requests_1m carries status_code as TEXT (the JSONB label value). The
// outcome split casts it to int (see exprStatusInt) so a missing or blank label
// reads as 0 and never matches the 200-299 / >=400 bands.
const (
	// exprStatusInt projects cagg_requests_1m.status_code (TEXT) to an int for
	// the outcome-band filters; blank/non-numeric reads as 0.
	exprStatusInt = `COALESCE(NULLIF(status_code, '')::int, 0)`

	// Token metric_name discriminators in cagg_tokens_1m. The "total" suffix
	// trips gosec's credential heuristic; these are OTel meter names, not secrets.
	metricTokensIn            = "slipspace.tokens.input.total"          //nolint:gosec // OTel meter name, not a credential
	metricTokensOut           = "slipspace.tokens.output.total"         //nolint:gosec // OTel meter name, not a credential
	metricTokensCached        = "slipspace.tokens.cached.total"         //nolint:gosec // OTel meter name, not a credential
	metricTokensCacheCreation = "slipspace.tokens.cache_creation.total" //nolint:gosec // OTel meter name, not a credential
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

// DashboardDimensionRow is one breakdown row keyed by a single dimension
// (provider, configuration).
type DashboardDimensionRow struct {
	Key       string
	Requests  int64
	ErrorRate float64
}

// DashboardProtocolRow is one (provider, protocol) breakdown row.
type DashboardProtocolRow struct {
	Provider  string
	Protocol  string
	Requests  int64
	ErrorRate float64
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

// DashboardSummary is the full rollup the console dashboard renders. Every panel
// aggregates a continuous aggregate: the request/outcome panels over
// cagg_requests_1m, token totals over cagg_tokens_1m, the fired-row panels over
// cagg_rules_1m / cagg_tags_1m (invariant #4: dashboards read meters, never
// record scans).
type DashboardSummary struct {
	Totals          DashboardTotals
	ByProvider      []DashboardDimensionRow
	ByProtocol      []DashboardProtocolRow
	ByConfiguration []DashboardDimensionRow
	ByModel         []DashboardModelRow
	RulesFired      []DashboardFiredRow
	TagsFired       []DashboardFiredRow
	ProviderHealth  []DashboardProviderHealth
}

// QueryDashboardSummary computes the whole dashboard rollup over [From, To),
// one query per panel, each narrowed by the dashboard-scoped Filter.
func (s *Store) QueryDashboardSummary(ctx context.Context, p DashboardParams) (DashboardSummary, error) {
	var out DashboardSummary
	var err error
	if out.Totals, err = s.queryDashTotals(ctx, p); err != nil {
		return DashboardSummary{}, err
	}
	if out.ByProvider, err = s.queryDashDimension(ctx, p, "provider"); err != nil {
		return DashboardSummary{}, err
	}
	if out.ByConfiguration, err = s.queryDashDimension(ctx, p, "configuration"); err != nil {
		return DashboardSummary{}, err
	}
	if out.ByProtocol, err = s.queryDashProtocol(ctx, p); err != nil {
		return DashboardSummary{}, err
	}
	if out.ByModel, err = s.queryDashModel(ctx, p); err != nil {
		return DashboardSummary{}, err
	}
	if out.RulesFired, err = s.queryDashFired(ctx, p, "rules"); err != nil {
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

// dashCaggColumns is the allowlist of equality predicates the dashboard honors
// against a CAGG. The shared EventFilter also carries tags + gateway_id + the id
// search boxes (SessionID/CorrelationID/AgentID/UserID), but the CAGGs carry
// NONE of those dimensions — they are message-browser-only. So the dashboard
// applies only the equality dimensions the CAGGs actually have; everything else
// is silently ignored here. (This is why dashboard rollups must NOT call the
// entity appendFilter, which references request_events columns / span_event JSONB
// that don't exist on a CAGG.)
var dashCaggColumns = []struct {
	col string
	val func(EventFilter) string
}{
	{"configuration", func(f EventFilter) string { return f.Configuration }},
	{"model", func(f EventFilter) string { return f.Model }},
	{"provider", func(f EventFilter) string { return f.Provider }},
	{"protocol", func(f EventFilter) string { return f.Protocol }},
}

// appendCaggFilter grows the WHERE fragments + args for the CAGG-scoped equality
// predicates plus the status-class band, binding every value as a parameter.
// Column names come from the package-internal allowlist, never request input.
// Tags / gateway_id / id-lookups are deliberately not applied — the CAGGs lack
// those columns (see dashCaggColumns).
func appendCaggFilter(where []string, args []any, f EventFilter) ([]string, []any) {
	for _, c := range dashCaggColumns {
		v := c.val(f)
		if v == "" {
			continue
		}
		args = append(args, v)
		where = append(where, fmt.Sprintf("%s = $%d", c.col, len(args)))
	}
	if lo, hi, ok := statusClassBounds(f.StatusClass); ok {
		args = append(args, lo)
		loIdx := len(args)
		if hi == 0 {
			where = append(where, fmt.Sprintf("%s >= $%d", exprStatusInt, loIdx))
		} else {
			args = append(args, hi)
			where = append(where, fmt.Sprintf("%s BETWEEN $%d AND $%d", exprStatusInt, loIdx, len(args)))
		}
	}
	return where, args
}

// dashWindow builds the half-open window WHERE fragments + args seeded with the
// dashboard-scoped Filter against a CAGG (bucket column). Bound order ($1=From,
// $2=To) is fixed so callers extend $3+.
func dashWindow(p DashboardParams) ([]string, []any) {
	where := []string{"bucket >= $1", "bucket < $2"}
	args := []any{p.From, p.To}
	return appendCaggFilter(where, args, p.Filter)
}

func (s *Store) queryDashTotals(ctx context.Context, p DashboardParams) (DashboardTotals, error) {
	where, args := dashWindow(p)
	reqQ := `
SELECT
  COALESCE(SUM(requests), 0),
  COALESCE(SUM(requests) FILTER (WHERE ` + exprStatusInt + ` BETWEEN 200 AND 299), 0),
  COALESCE(SUM(requests) FILTER (WHERE ` + exprStatusInt + ` >= 400), 0)
FROM cagg_requests_1m
WHERE ` + strings.Join(where, " AND ")

	var t DashboardTotals
	if err := s.db.QueryRow(ctx, reqQ, args...).Scan(&t.Requests, &t.RequestsSuccess, &t.RequestsErrored); err != nil {
		return DashboardTotals{}, fmt.Errorf("store: dashboard totals: %w", err)
	}

	// Token totals come from cagg_tokens_1m, pivoted by metric_name. The token
	// CAGG has no status_code column, so the request filter's status band must
	// not apply here — build a token-only window.
	twhere, targs := dashTokenWindow(p)
	tokQ := `
SELECT
  COALESCE(SUM(tokens) FILTER (WHERE metric_name = '` + metricTokensIn + `'), 0),
  COALESCE(SUM(tokens) FILTER (WHERE metric_name = '` + metricTokensOut + `'), 0),
  COALESCE(SUM(tokens) FILTER (WHERE metric_name = '` + metricTokensCached + `'), 0),
  COALESCE(SUM(tokens) FILTER (WHERE metric_name = '` + metricTokensCacheCreation + `'), 0)
FROM cagg_tokens_1m
WHERE ` + strings.Join(twhere, " AND ")

	if err := s.db.QueryRow(ctx, tokQ, targs...).Scan(
		&t.TokensIn, &t.TokensOut, &t.TokensCached, &t.TokensCacheCreation,
	); err != nil {
		return DashboardTotals{}, fmt.Errorf("store: dashboard token totals: %w", err)
	}
	return t, nil
}

// dashTokenWindow builds the window + equality predicates for cagg_tokens_1m,
// which lacks a status_code column — the StatusClass band is dropped so a 5xx
// dashboard filter doesn't reference a missing column. Provider/model/
// configuration/protocol all exist on the token CAGG and still apply.
func dashTokenWindow(p DashboardParams) ([]string, []any) {
	where := []string{"bucket >= $1", "bucket < $2"}
	args := []any{p.From, p.To}
	f := p.Filter
	f.StatusClass = ""
	return appendCaggFilter(where, args, f)
}

// dashDimensionColumns is the allowlist for the single-column breakdowns.
var dashDimensionColumns = map[string]string{
	"provider":      "provider",
	"configuration": "configuration",
}

func (s *Store) queryDashDimension(ctx context.Context, p DashboardParams, dim string) ([]DashboardDimensionRow, error) {
	col, ok := dashDimensionColumns[dim]
	if !ok {
		return nil, fmt.Errorf("store: unknown dashboard dimension %q", dim)
	}
	where, args := dashWindow(p)
	q := fmt.Sprintf(`
SELECT %s AS grp,
  COALESCE(SUM(requests), 0),
  COALESCE(SUM(requests) FILTER (WHERE %s >= 400), 0)
FROM cagg_requests_1m
WHERE %s AND %s <> ''
GROUP BY grp
ORDER BY 2 DESC, grp`, col, exprStatusInt, strings.Join(where, " AND "), col)

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard dimension %s: %w", dim, err)
	}
	defer rows.Close()

	var out []DashboardDimensionRow
	for rows.Next() {
		var (
			r      DashboardDimensionRow
			errCnt int64
		)
		if err := rows.Scan(&r.Key, &r.Requests, &errCnt); err != nil {
			return nil, fmt.Errorf("store: scan dashboard dimension: %w", err)
		}
		r.ErrorRate = rate(errCnt, r.Requests)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) queryDashProtocol(ctx context.Context, p DashboardParams) ([]DashboardProtocolRow, error) {
	where, args := dashWindow(p)
	q := `
SELECT provider, protocol,
  COALESCE(SUM(requests), 0),
  COALESCE(SUM(requests) FILTER (WHERE ` + exprStatusInt + ` >= 400), 0)
FROM cagg_requests_1m
WHERE ` + strings.Join(where, " AND ") + `
  AND provider <> '' AND protocol <> ''
GROUP BY provider, protocol
ORDER BY 3 DESC, provider, protocol`

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard protocol: %w", err)
	}
	defer rows.Close()

	var out []DashboardProtocolRow
	for rows.Next() {
		var (
			r      DashboardProtocolRow
			errCnt int64
		)
		if err := rows.Scan(&r.Provider, &r.Protocol, &r.Requests, &errCnt); err != nil {
			return nil, fmt.Errorf("store: scan dashboard protocol: %w", err)
		}
		r.ErrorRate = rate(errCnt, r.Requests)
		out = append(out, r)
	}
	return out, rows.Err()
}

// queryDashModel joins per-model request counts (cagg_requests_1m) with per-model
// token sums (cagg_tokens_1m) in SQL. A FULL OUTER JOIN keeps a model that has
// only token rows or only request rows; COALESCE collapses the two model keys and
// zero-fills the missing side. provider is taken from whichever CAGG has it.
func (s *Store) queryDashModel(ctx context.Context, p DashboardParams) ([]DashboardModelRow, error) {
	rwhere, rargs := dashWindow(p)
	// Token side shares the same equality filters minus the status band.
	twhere, _ := dashTokenWindow(p)
	// dashTokenWindow re-numbers from $1; but we need its predicates to bind the
	// SAME args as the request side. Both windows are built from the same Filter
	// in the same order, so the request-side args cover both — we reuse rargs and
	// renumber the token predicates onto the shared arg list. Simpler: build one
	// shared filter and reference it in both CTEs.
	q := `
WITH req AS (
  SELECT model, COALESCE(MAX(provider), '') AS provider, COALESCE(SUM(requests), 0) AS requests
  FROM cagg_requests_1m
  WHERE ` + strings.Join(rwhere, " AND ") + ` AND model <> ''
  GROUP BY model
),
tok AS (
  SELECT model,
    COALESCE(MAX(provider), '') AS provider,
    COALESCE(SUM(tokens) FILTER (WHERE metric_name = '` + metricTokensIn + `'), 0) AS tokens_in,
    COALESCE(SUM(tokens) FILTER (WHERE metric_name = '` + metricTokensOut + `'), 0) AS tokens_out
  FROM cagg_tokens_1m
  WHERE ` + strings.Join(twhere, " AND ") + ` AND model <> ''
  GROUP BY model
)
SELECT COALESCE(req.model, tok.model) AS model,
  COALESCE(NULLIF(req.provider, ''), tok.provider, '') AS provider,
  COALESCE(req.requests, 0) AS requests,
  COALESCE(tok.tokens_in, 0) AS tokens_in,
  COALESCE(tok.tokens_out, 0) AS tokens_out
FROM req FULL OUTER JOIN tok ON req.model = tok.model
ORDER BY requests DESC, model`

	rows, err := s.db.Query(ctx, q, rargs...)
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

// dashFiredCagg pairs a fired-row panel with its backing CAGG, key column, and
// value column. The CAGG + columns are interpolated into SQL, so they MUST come
// from this fixed set, never from request input.
type dashFiredCagg struct {
	view     string
	keyCol   string
	valueCol string
}

// dashFiredCaggs is the allowlist for the meter-backed fired-row panels. Each
// reads its dedicated CAGG (cagg_rules_1m / cagg_tags_1m) rather than scanning
// metric_points raw (invariant #4: dashboards read meters, never record scans).
var dashFiredCaggs = map[string]dashFiredCagg{
	"rules": {view: "cagg_rules_1m", keyCol: "rule_name", valueCol: "fired"},
	"tags":  {view: "cagg_tags_1m", keyCol: "tag", valueCol: "applied"},
}

// queryDashFired aggregates a fired-row panel from its continuous aggregate. The
// CAGGs carry only (bucket, key, configuration, value); the only dashboard filter
// they can honor is the window + configuration equality (provider/model/status
// don't exist on these meters). UsedByConfigurations lists the distinct configs
// that produced each key.
func (s *Store) queryDashFired(ctx context.Context, p DashboardParams, panel string) ([]DashboardFiredRow, error) {
	m, ok := dashFiredCaggs[panel]
	if !ok {
		return nil, fmt.Errorf("store: unknown fired panel %q", panel)
	}
	where := []string{
		"bucket >= $1",
		"bucket < $2",
		m.keyCol + " IS NOT NULL",
		m.keyCol + " <> ''",
	}
	args := []any{p.From, p.To}
	if p.Filter.Configuration != "" {
		args = append(args, p.Filter.Configuration)
		where = append(where, fmt.Sprintf("configuration = $%d", len(args)))
	}
	q := fmt.Sprintf(`
SELECT %s AS key, COALESCE(SUM(%s), 0)::bigint AS cnt,
  COALESCE(array_agg(DISTINCT configuration) FILTER (WHERE configuration IS NOT NULL AND configuration <> ''), ARRAY[]::text[]) AS configs
FROM %s
WHERE %s
GROUP BY key
ORDER BY cnt DESC, key`, m.keyCol, m.valueCol, m.view, strings.Join(where, " AND "))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard fired %s: %w", panel, err)
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
	where := []string{"bucket >= $1", "bucket < $2", "provider <> ''"}
	args := []any{p.RecentFrom, p.To}
	where, args = appendCaggFilter(where, args, p.Filter)
	q := `
SELECT provider,
  COALESCE(SUM(requests), 0),
  COALESCE(SUM(requests) FILTER (WHERE ` + exprStatusInt + ` >= 400), 0)
FROM cagg_requests_1m
WHERE ` + strings.Join(where, " AND ") + `
GROUP BY provider
ORDER BY provider`

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
// Latency quantiles are gone (MVP drops percentiles); the series carries volume,
// outcome, and the two token curves only.
type DashboardSeriesBucket struct {
	Ts        time.Time
	Requests  int64
	Errored   int64
	TokensIn  int64
	TokensOut int64
}

// QueryDashboardSeries computes a zero-filled per-bucket time series over
// [From, To), one row per BucketSeconds-wide slot, each carrying request volume,
// errored count, and the two token sums. It re-buckets the 1-minute CAGGs up to
// the requested width with an outer time_bucket and LEFT JOINs onto a generated
// bucket spine so empty slots zero-fill.
func (s *Store) QueryDashboardSeries(ctx context.Context, p DashboardSeriesParams) ([]DashboardSeriesBucket, error) {
	if p.BucketSeconds <= 0 {
		return nil, fmt.Errorf("store: bucket_seconds must be positive")
	}
	// Request side: window + the CAGG-scoped equality/status filter.
	rwhere := []string{"bucket >= $1", "bucket < $2"}
	rargs := []any{p.From, p.To}
	rwhere, rargs = appendCaggFilter(rwhere, rargs, p.Filter)
	// Token side: same window + equality filters, minus the status band (the
	// token CAGG has no status_code column).
	tf := p.Filter
	tf.StatusClass = ""
	twhere := []string{"bucket >= $1", "bucket < $2"}
	twhere, _ = appendCaggFilter(twhere, []any{p.From, p.To}, tf)
	// BucketSeconds is the last bound; reuse it across the spine + both re-buckets.
	rargs = append(rargs, p.BucketSeconds)
	w := len(rargs)
	// The spine is generated on time_bucket boundaries (floor of From up to To)
	// so each spine slot equals a re-bucketed CTE ts exactly — a raw
	// generate_series from an unaligned From would never match the floored CTE
	// keys and every bucket would read empty.
	q := fmt.Sprintf(`
WITH spine AS (
  SELECT generate_series(
    time_bucket(make_interval(secs => $%d), $1::timestamptz),
    time_bucket(make_interval(secs => $%d), $2::timestamptz - make_interval(secs => $%d)),
    make_interval(secs => $%d)) AS ts
),
req AS (
  SELECT time_bucket(make_interval(secs => $%d), bucket) AS ts,
    COALESCE(SUM(requests), 0) AS requests,
    COALESCE(SUM(requests) FILTER (WHERE %s >= 400), 0) AS errored
  FROM cagg_requests_1m
  WHERE %s
  GROUP BY 1
),
tok AS (
  SELECT time_bucket(make_interval(secs => $%d), bucket) AS ts,
    COALESCE(SUM(tokens) FILTER (WHERE metric_name = '%s'), 0) AS tokens_in,
    COALESCE(SUM(tokens) FILTER (WHERE metric_name = '%s'), 0) AS tokens_out
  FROM cagg_tokens_1m
  WHERE %s
  GROUP BY 1
)
SELECT spine.ts,
  COALESCE(req.requests, 0),
  COALESCE(req.errored, 0),
  COALESCE(tok.tokens_in, 0),
  COALESCE(tok.tokens_out, 0)
FROM spine
LEFT JOIN req ON req.ts = spine.ts
LEFT JOIN tok ON tok.ts = spine.ts
ORDER BY spine.ts`,
		w, w, w, w,
		w, exprStatusInt, strings.Join(rwhere, " AND "),
		w, metricTokensIn, metricTokensOut, strings.Join(twhere, " AND "))

	rows, err := s.db.Query(ctx, q, rargs...)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard series: %w", err)
	}
	defer rows.Close()

	var out []DashboardSeriesBucket
	for rows.Next() {
		var b DashboardSeriesBucket
		if err := rows.Scan(&b.Ts, &b.Requests, &b.Errored, &b.TokensIn, &b.TokensOut); err != nil {
			return nil, fmt.Errorf("store: scan dashboard series: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
