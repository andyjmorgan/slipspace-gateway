package configdb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidCursor is returned when a pagination cursor cannot be decoded — a
// truncated, tampered, or hand-typed cursor, never a value this package minted.
var ErrInvalidCursor = errors.New("configdb: invalid cursor")

// EventFilter is the set of equality + status-class predicates the fleet
// dashboards and the message browser share. A zero field means "no filter on
// this dimension". StatusClass is one of "2xx"/"4xx"/"5xx" (empty = any).
type EventFilter struct {
	Configuration string
	Gateway       string
	Model         string
	Backend       string
	Protocol      string
	StatusClass   string
}

// EventStatsParams is the input to QueryEventStats: a half-open [From, To) range
// bucketed into BucketSeconds-wide slots, narrowed by Filter, optionally grouped
// for the "top" breakdown. GroupBy is one of model/configuration/backend/
// gateway/protocol (empty = no top list).
type EventStatsParams struct {
	From          time.Time
	To            time.Time
	BucketSeconds int
	Filter        EventFilter
	GroupBy       string
}

// StatTotals is the headline rollup over the whole range.
type StatTotals struct {
	Requests     int64
	TokensIn     int64
	TokensOut    int64
	Errors       int64
	ErrorRate    float64
	AvgLatencyMs int64
	P95LatencyMs int64
}

// StatBucket is one time bucket in the series, anchored at the bucket start.
type StatBucket struct {
	Ts           time.Time
	Requests     int64
	TokensIn     int64
	TokensOut    int64
	AvgLatencyMs int64
	P95LatencyMs int64
	Status2xx    int64
	Status4xx    int64
	Status5xx    int64
}

// StatGroup is one row of the "top" breakdown when GroupBy is set.
type StatGroup struct {
	Key          string
	Requests     int64
	TokensIn     int64
	TokensOut    int64
	Errors       int64
	ErrorRate    float64
	AvgLatencyMs int64
}

// EventStats is the full stats response: headline totals, the time series, and
// (when grouped) the top-10 breakdown.
type EventStats struct {
	Totals StatTotals
	Series []StatBucket
	Top    []StatGroup
}

// groupByColumns maps the API group_by value to its column. The map is the
// allowlist — an unknown group_by yields no top list rather than an injected
// identifier (the value never reaches SQL as text).
var groupByColumns = map[string]string{
	"model":         "model",
	"configuration": "configuration",
	"backend":       "backend",
	"gateway":       "gateway_id",
	"protocol":      "protocol",
}

// appendFilter grows the WHERE fragments + args for the equality and
// status-class predicates, binding every user value as a parameter ($N). It
// never interpolates a user string into SQL — only the column names, which come
// from the package-internal allowlist.
func appendFilter(where []string, args []any, f EventFilter) ([]string, []any) {
	eq := []struct {
		col string
		val string
	}{
		{"configuration", f.Configuration},
		{"gateway_id", f.Gateway},
		{"model", f.Model},
		{"backend", f.Backend},
		{"protocol", f.Protocol},
	}
	for _, e := range eq {
		if e.val == "" {
			continue
		}
		args = append(args, e.val)
		where = append(where, fmt.Sprintf("%s = $%d", e.col, len(args)))
	}
	if lo, hi, ok := statusClassBounds(f.StatusClass); ok {
		args = append(args, lo)
		loIdx := len(args)
		if hi == 0 {
			where = append(where, fmt.Sprintf("status_code >= $%d", loIdx))
		} else {
			args = append(args, hi)
			where = append(where, fmt.Sprintf("status_code BETWEEN $%d AND $%d", loIdx, len(args)))
		}
	}
	return where, args
}

// statusClassBounds maps a status class to its inclusive [lo, hi] bounds; hi==0
// means "lo and up" (the 5xx open upper bound). ok is false for an empty or
// unrecognized class (no predicate added).
func statusClassBounds(class string) (lo, hi int, ok bool) {
	switch class {
	case "2xx":
		return 200, 299, true
	case "4xx":
		return 400, 499, true
	case "5xx":
		return 500, 0, true
	default:
		return 0, 0, false
	}
}

// QueryEventStats computes the dashboard rollup over [From, To): headline
// totals, a zero-filled per-bucket time series, and (when GroupBy is set) the
// top-10 groups by request count. Buckets with no data are zero-filled via
// generate_series so the frontend renders a continuous axis. p95 uses
// percentile_cont over latency_ms.
func (db *DB) QueryEventStats(ctx context.Context, p EventStatsParams) (EventStats, error) {
	if p.BucketSeconds <= 0 {
		return EventStats{}, fmt.Errorf("configdb: bucket_seconds must be positive")
	}

	totals, err := db.queryStatTotals(ctx, p)
	if err != nil {
		return EventStats{}, err
	}
	series, err := db.queryStatSeries(ctx, p)
	if err != nil {
		return EventStats{}, err
	}
	stats := EventStats{Totals: totals, Series: series}
	if p.GroupBy != "" {
		top, terr := db.queryStatTop(ctx, p)
		if terr != nil {
			return EventStats{}, terr
		}
		stats.Top = top
	}
	return stats, nil
}

func (db *DB) queryStatTotals(ctx context.Context, p EventStatsParams) (StatTotals, error) {
	where := []string{"observed_at >= $1", "observed_at < $2"}
	args := []any{p.From, p.To}
	where, args = appendFilter(where, args, p.Filter)

	q := `
SELECT
  COUNT(*),
  COALESCE(SUM(tokens_in), 0),
  COALESCE(SUM(tokens_out), 0),
  COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0),
  COALESCE(AVG(latency_ms), 0),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0)
FROM request_events
WHERE ` + strings.Join(where, " AND ")

	var (
		t      StatTotals
		avg    float64
		p95    float64
		errCnt int64
	)
	if err := db.pool.QueryRow(ctx, q, args...).Scan(
		&t.Requests, &t.TokensIn, &t.TokensOut, &errCnt, &avg, &p95,
	); err != nil {
		return StatTotals{}, fmt.Errorf("configdb: stat totals: %w", err)
	}
	t.Errors = errCnt
	t.AvgLatencyMs = int64(avg + 0.5)
	t.P95LatencyMs = int64(p95 + 0.5)
	t.ErrorRate = rate(errCnt, t.Requests)
	return t, nil
}

func (db *DB) queryStatSeries(ctx context.Context, p EventStatsParams) ([]StatBucket, error) {
	where := []string{"e.observed_at >= $1", "e.observed_at < $2"}
	args := []any{p.From, p.To}
	where, args = appendFilter(where, args, p.Filter)

	// $3 is the bucket width; generate_series emits every bucket start across
	// [from, to) so empty buckets zero-fill, then a LEFT JOIN folds the matching
	// events in.
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
  COALESCE(SUM(e.tokens_in), 0),
  COALESCE(SUM(e.tokens_out), 0),
  COALESCE(AVG(e.latency_ms), 0),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY e.latency_ms), 0),
  COALESCE(SUM(CASE WHEN e.status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN e.status_code BETWEEN 400 AND 499 THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN e.status_code >= 500 THEN 1 ELSE 0 END), 0)
FROM buckets b
LEFT JOIN request_events e
  ON e.observed_at >= b.ts
 AND e.observed_at < b.ts + ($%d * INTERVAL '1 second')
 AND %s
GROUP BY b.ts
ORDER BY b.ts`, widthIdx, widthIdx, widthIdx, strings.Join(where, " AND "))

	rows, err := db.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("configdb: stat series: %w", err)
	}
	defer rows.Close()

	var out []StatBucket
	for rows.Next() {
		var (
			b   StatBucket
			avg float64
			p95 float64
		)
		if err := rows.Scan(&b.Ts, &b.Requests, &b.TokensIn, &b.TokensOut, &avg, &p95,
			&b.Status2xx, &b.Status4xx, &b.Status5xx); err != nil {
			return nil, fmt.Errorf("configdb: scan stat bucket: %w", err)
		}
		b.AvgLatencyMs = int64(avg + 0.5)
		b.P95LatencyMs = int64(p95 + 0.5)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (db *DB) queryStatTop(ctx context.Context, p EventStatsParams) ([]StatGroup, error) {
	col, ok := groupByColumns[p.GroupBy]
	if !ok {
		return nil, fmt.Errorf("configdb: unknown group_by %q", p.GroupBy)
	}
	where := []string{"observed_at >= $1", "observed_at < $2"}
	args := []any{p.From, p.To}
	where, args = appendFilter(where, args, p.Filter)

	q := fmt.Sprintf(`
SELECT
  %s AS grp,
  COUNT(*),
  COALESCE(SUM(tokens_in), 0),
  COALESCE(SUM(tokens_out), 0),
  COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0),
  COALESCE(AVG(latency_ms), 0)
FROM request_events
WHERE %s
GROUP BY grp
ORDER BY COUNT(*) DESC, grp
LIMIT 10`, col, strings.Join(where, " AND "))

	rows, err := db.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("configdb: stat top: %w", err)
	}
	defer rows.Close()

	var out []StatGroup
	for rows.Next() {
		var (
			g      StatGroup
			errCnt int64
			avg    float64
		)
		if err := rows.Scan(&g.Key, &g.Requests, &g.TokensIn, &g.TokensOut, &errCnt, &avg); err != nil {
			return nil, fmt.Errorf("configdb: scan stat group: %w", err)
		}
		g.Errors = errCnt
		g.AvgLatencyMs = int64(avg + 0.5)
		g.ErrorRate = rate(errCnt, g.Requests)
		out = append(out, g)
	}
	return out, rows.Err()
}

func rate(num, den int64) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// EventListParams is the input to ListEventsFiltered: an optional time window,
// the shared equality/status filters, an opaque keyset Cursor, and a Limit. A
// zero From/To omits that bound; Limit <= 0 defaults to 100 and is capped at
// 500.
type EventListParams struct {
	From   time.Time
	To     time.Time
	Filter EventFilter
	Cursor string
	Limit  int
}

// eventCursor is the keyset position encoded into next_cursor: the last row's
// (observed_at, correlation_id), the tuple the DESC ordering seeks past.
type eventCursor struct {
	ObservedAt  time.Time `json:"o"`
	Correlation string    `json:"c"`
}

func encodeCursor(c eventCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (eventCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return eventCursor{}, ErrInvalidCursor
	}
	var c eventCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return eventCursor{}, ErrInvalidCursor
	}
	return c, nil
}

const eventListPageDefault = 100
const eventListPageMax = 500

// ListEventsFiltered returns a page of request events ordered by
// (observed_at DESC, correlation_id DESC), narrowed by an optional time window
// and the shared filters. Pagination is keyset: it fetches Limit+1 rows to
// learn whether a further page exists; when it does, nextCursor encodes the last
// returned row's position and the caller passes it back as Cursor. An empty
// nextCursor means the last page. A malformed Cursor is ErrInvalidCursor.
func (db *DB) ListEventsFiltered(ctx context.Context, p EventListParams) ([]RequestEvent, string, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = eventListPageDefault
	}
	if limit > eventListPageMax {
		limit = eventListPageMax
	}

	where := []string{}
	args := []any{}
	if !p.From.IsZero() {
		args = append(args, p.From)
		where = append(where, fmt.Sprintf("observed_at >= $%d", len(args)))
	}
	if !p.To.IsZero() {
		args = append(args, p.To)
		where = append(where, fmt.Sprintf("observed_at < $%d", len(args)))
	}
	where, args = appendFilter(where, args, p.Filter)

	if p.Cursor != "" {
		cur, err := decodeCursor(p.Cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, cur.ObservedAt)
		oIdx := len(args)
		args = append(args, cur.Correlation)
		where = append(where, fmt.Sprintf(
			"(observed_at, correlation_id) < ($%d, $%d)", oIdx, len(args)))
	}

	q := `SELECT ` + requestEventColumns + ` FROM request_events`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	args = append(args, limit+1)
	q += fmt.Sprintf(` ORDER BY observed_at DESC, correlation_id DESC LIMIT $%d`, len(args))

	rows, err := db.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("configdb: list events filtered: %w", err)
	}
	defer rows.Close()

	var out []RequestEvent
	for rows.Next() {
		e, serr := scanRequestEvent(rows)
		if serr != nil {
			return nil, "", serr
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	next := ""
	if len(out) > limit {
		last := out[limit-1]
		out = out[:limit]
		next = encodeCursor(eventCursor{ObservedAt: last.ObservedAt, Correlation: last.CorrelationID})
	}
	return out, next, nil
}
