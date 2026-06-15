package store

import (
	"context"
	"fmt"
	"time"
)

// The security dashboard rollups read the verdict + finding tables directly,
// not the metric_points CAGGs the rest of the dashboard reads. That is a
// deliberate, invariant-#4-safe exception: verdict/finding are compact derived
// tables on the OTLP span feed (one verdict per request, one row per finding),
// NOT the S3/spool Record channel invariant #4 guards the dashboard against,
// and they carry the security dimensions (verdict state, check_type, category)
// that no OTel meter exposes. They are window-only — verdict/finding carry no
// provider/configuration/model columns, so the dashboard's equality filters do
// not apply here (the same limitation the meter-backed fired panels have).

// securityTopCategories caps the most-common finding categories the dashboard
// surfaces, so a high-cardinality taxonomy can't grow the panel unbounded.
const securityTopCategories = 5

// DashboardSecurity is the Arbiter posture + finding breakdown the console's
// security rows render over a [From, To) window.
type DashboardSecurity struct {
	// Scanned is the number of requests that reached a verdict in the window.
	Scanned int64
	// Flagged / Partial / Clean split Scanned by terminal verdict state.
	// PARTIAL is first-class (FLAGGED ▸ PARTIAL ▸ CLEAN) and never folds into
	// clean — an inconclusive/failed scan must not read as safe.
	Flagged int64
	Partial int64
	Clean   int64
	// ByCheckType counts findings per detector check type (injection / pii /
	// toxicity), descending by count.
	ByCheckType []DashboardSecurityCount
	// TopCategories is the most-common finding categories, capped at
	// securityTopCategories.
	TopCategories []DashboardSecurityCount
}

// DashboardSecurityCount is one (key, count) breakdown row — a check type or a
// finding category.
type DashboardSecurityCount struct {
	Key   string
	Count int64
}

// QueryDashboardSecurity computes the security posture (verdict state split)
// plus the finding breakdowns (by check type, top categories) over [from, to).
func (s *Store) QueryDashboardSecurity(ctx context.Context, from, to time.Time) (DashboardSecurity, error) {
	var out DashboardSecurity

	// Posture: one verdict row per request, split by terminal state.
	const postureQ = `
SELECT
  COUNT(*),
  COUNT(*) FILTER (WHERE state = 'flagged'),
  COUNT(*) FILTER (WHERE state = 'partial'),
  COUNT(*) FILTER (WHERE state = 'clean')
FROM verdict
WHERE decided_at >= $1 AND decided_at < $2`
	if err := s.db.QueryRow(ctx, postureQ, from, to).Scan(
		&out.Scanned, &out.Flagged, &out.Partial, &out.Clean,
	); err != nil {
		return DashboardSecurity{}, fmt.Errorf("store: dashboard security posture: %w", err)
	}

	byType, err := s.queryFindingCounts(ctx, from, to, securityFindingCol("check_type"), 0)
	if err != nil {
		return DashboardSecurity{}, err
	}
	out.ByCheckType = byType

	cats, err := s.queryFindingCounts(ctx, from, to, securityFindingCol("category"), securityTopCategories)
	if err != nil {
		return DashboardSecurity{}, err
	}
	out.TopCategories = cats

	return out, nil
}

// securityFindingCol is the allowlist gate for the finding-breakdown group-by
// column: it is interpolated into SQL, so it MUST come from this fixed set,
// never from request input. An unknown key panics — it can only be a caller bug
// in this package, never reachable from a request.
func securityFindingCol(col string) string {
	switch col {
	case "check_type", "category":
		return col
	default:
		panic("store: invalid security finding column " + col)
	}
}

// queryFindingCounts groups findings in [from, to) by col (descending by count,
// key as tiebreak). limit > 0 caps the rows (the top-N categories); limit == 0
// returns all (the bounded check-type set). col is allowlisted by
// securityFindingCol.
func (s *Store) queryFindingCounts(ctx context.Context, from, to time.Time, col string, limit int) ([]DashboardSecurityCount, error) {
	q := fmt.Sprintf(`
SELECT %s AS key, COUNT(*)::bigint AS cnt
FROM finding
WHERE created_at >= $1 AND created_at < $2 AND %s <> ''
GROUP BY key
ORDER BY cnt DESC, key`, col, col)
	args := []any{from, to}
	if limit > 0 {
		args = append(args, limit)
		q += fmt.Sprintf("\nLIMIT $%d", len(args))
	}

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: dashboard security findings by %s: %w", col, err)
	}
	defer rows.Close()

	var out []DashboardSecurityCount
	for rows.Next() {
		var c DashboardSecurityCount
		if err := rows.Scan(&c.Key, &c.Count); err != nil {
			return nil, fmt.Errorf("store: scan dashboard security finding: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
