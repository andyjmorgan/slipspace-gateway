package store

import (
	"context"
	"fmt"
	"time"
)

// MetricPoint is one numeric telemetry sample a gateway pushed via OTLP — a
// metric name, its label set (as JSON), the value, and when it was observed. It
// is the raw timeseries feed the dashboard aggregates into rate/token/latency
// panels.
type MetricPoint struct {
	// Name is the OTLP metric name.
	Name string
	// Labels is the merged attribute set as JSON.
	Labels []byte
	// Value is the sample value.
	Value float64
	// ObservedAt is the sample's timestamp.
	ObservedAt time.Time
}

// InsertMetricPoints appends a batch of metric samples in one transaction. An
// empty batch is a no-op.
func (s *Store) InsertMetricPoints(ctx context.Context, points []MetricPoint) error {
	if len(points) == 0 {
		return nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin metric insert: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, p := range points {
		labels := p.Labels
		if len(labels) == 0 {
			labels = []byte("{}")
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO metric_points (metric_name, labels, value, observed_at) VALUES ($1, $2, $3, $4)`,
			p.Name, labels, p.Value, p.ObservedAt); err != nil {
			return fmt.Errorf("store: insert metric point: %w", err)
		}
	}
	return commit(ctx, tx)
}

// ListRecentMetricPoints returns the most recent samples, newest first, capped
// at limit (defaulted to 500 when <= 0).
func (s *Store) ListRecentMetricPoints(ctx context.Context, limit int) ([]MetricPoint, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(ctx,
		`SELECT metric_name, labels, value, observed_at FROM metric_points ORDER BY observed_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list metric points: %w", err)
	}
	defer rows.Close()

	var out []MetricPoint
	for rows.Next() {
		var p MetricPoint
		if err := rows.Scan(&p.Name, &p.Labels, &p.Value, &p.ObservedAt); err != nil {
			return nil, fmt.Errorf("store: scan metric point: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
