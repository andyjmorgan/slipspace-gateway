package store

import (
	"context"
	"fmt"
)

// Facets is the set of distinct dimension values the message browser's dropdowns
// offer. Each slice is sorted ascending and excludes the empty string; Tags is
// flattened out of every event's detail->'tags' array. Populated by Facets.
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

// Facets returns the distinct values per filter dimension for the dropdowns.
// The scalar columns use SELECT DISTINCT (empty strings excluded); Tags unnests
// detail->'tags' (GIN-indexed) so the AND tag filter and its dropdown share one
// source. The result is small and cacheable — the server layer holds it behind
// a short TTL so a dropdown open is instant after the first scan.
func (s *Store) Facets(ctx context.Context) (Facets, error) {
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
			fmt.Sprintf(`SELECT DISTINCT %s FROM request_events WHERE %s <> '' ORDER BY 1`, c.col, c.col))
		if err != nil {
			return Facets{}, fmt.Errorf("store: facets %s: %w", c.col, err)
		}
		*c.dst = vals
	}
	tags, err := s.distinctStrings(ctx,
		`SELECT DISTINCT t FROM request_events, LATERAL jsonb_array_elements_text(detail->'tags') AS t WHERE detail ? 'tags' ORDER BY 1`)
	if err != nil {
		return Facets{}, fmt.Errorf("store: facets tags: %w", err)
	}
	f.Tags = tags
	return f, nil
}

// distinctStrings runs a single-column query and collects the values.
func (s *Store) distinctStrings(ctx context.Context, query string) ([]string, error) {
	rows, err := s.db.Query(ctx, query)
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
