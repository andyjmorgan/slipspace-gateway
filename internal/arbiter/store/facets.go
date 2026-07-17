package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Facets is the set of distinct dimension values the message browser's dropdowns
// offer. Each slice is sorted ascending and excludes the empty string; Tags is
// flattened out of every event's promoted tags text[] column. Populated by Facets.
type Facets struct {
	// Providers is the distinct post-rule upstream provider names.
	Providers []string
	// Models is the distinct requested model names.
	Models []string
	// Configurations is the distinct resolved policy bundle names.
	Configurations []string
	// Protocols is the distinct post-rule protocol values (the protocol
	// column), or passthrough family names.
	Protocols []string
	// Tags is the distinct post-rule tag set across all events.
	Tags []string
}

// Facets returns the distinct values per filter dimension for the dropdowns,
// bounded to the observed_at window when From/To are non-zero (a zero bound is
// open on that side) — so the dropdowns offer only values actually present in
// the range the table is showing. The scalar columns use SELECT DISTINCT
// (empty strings excluded); Tags unnests the promoted tags text[] column
// (GIN-indexed) so the AND tag filter and its dropdown share one source. The
// result is small and cacheable — the server layer holds it behind a short
// per-window TTL so a dropdown open is instant after the first scan.
func (s *Store) Facets(ctx context.Context, from, to time.Time) (Facets, error) {
	// The window predicate is assembled from fixed fragments + bound params —
	// never user strings — and shared by every per-column query below.
	var bounds []string
	var args []any
	if !from.IsZero() {
		args = append(args, from)
		bounds = append(bounds, fmt.Sprintf("observed_at >= $%d", len(args)))
	}
	if !to.IsZero() {
		args = append(args, to)
		bounds = append(bounds, fmt.Sprintf("observed_at < $%d", len(args)))
	}
	window := ""
	if len(bounds) > 0 {
		window = " AND " + strings.Join(bounds, " AND ")
	}

	var f Facets
	cols := []struct {
		col string
		dst *[]string
	}{
		{"provider", &f.Providers},
		{"model", &f.Models},
		{"configuration", &f.Configurations},
		{"protocol", &f.Protocols},
	}
	for _, c := range cols {
		// Column name comes from this package-internal allowlist, never user
		// input, so the format is safe.
		vals, err := s.distinctStrings(ctx,
			fmt.Sprintf(`SELECT DISTINCT %s FROM request_events WHERE %s <> ''%s ORDER BY 1`, c.col, c.col, window), args...)
		if err != nil {
			return Facets{}, fmt.Errorf("store: facets %s: %w", c.col, err)
		}
		*c.dst = vals
	}
	// Tags unnests the promoted tags text[] column (v12) — empty arrays yield no
	// rows, so only the window needs a WHERE guard. This replaced a
	// jsonb_array_elements_text(span_event->'tags') scan that detoasted every
	// span (~30 s on prod) and emptied all dropdowns when it blew the deadline.
	tagsWhere := ""
	if len(bounds) > 0 {
		tagsWhere = " WHERE " + strings.Join(bounds, " AND ")
	}
	tags, err := s.distinctStrings(ctx,
		`SELECT DISTINCT t FROM request_events, LATERAL unnest(tags) AS t`+tagsWhere+` ORDER BY 1`, args...)
	if err != nil {
		return Facets{}, fmt.Errorf("store: facets tags: %w", err)
	}
	f.Tags = tags
	return f, nil
}

// distinctStrings runs a single-column query and collects the values.
func (s *Store) distinctStrings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
