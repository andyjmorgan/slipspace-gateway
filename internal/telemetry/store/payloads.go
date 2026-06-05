package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrPayloadNotFound is returned when a correlation id has no stored payloads.
var ErrPayloadNotFound = errors.New("store: request payload not found")

// Payload kinds. The discriminator separates the items that share a
// correlation_id so the inspector can render the right tab for each.
const (
	// KindRequestBody is the captured client→gateway request envelope.
	KindRequestBody = "request_body"
	// KindResponseBody is the captured gateway→client response envelope.
	KindResponseBody = "response_body"
	// KindSSERollup is the assembled (de-chunked) SSE stream.
	KindSSERollup = "sse_rollup"
	// KindRequestHeaders is the captured request header map (JSON object),
	// sourced from the Record feed alongside the request body.
	KindRequestHeaders = "request_headers"
	// KindResponseHeaders is the captured response header map (JSON object),
	// sourced from the Record feed alongside the response body.
	KindResponseHeaders = "response_headers"
)

// Payload is one captured large item — a request body, a response body, or an
// assembled-SSE rollup — pushed by a gateway via the HMAC webhook and stored
// ONE ROW PER ITEM. Joined to a request_event by correlation_id; the kind
// discriminator separates the items that share it. Cross-instance order uses
// (ts_ns, instance_id, seq) — invariant #8, never receive order.
type Payload struct {
	// CorrelationID joins the payload to its request_event.
	CorrelationID string
	// Kind is one of the Kind* constants.
	Kind string
	// InstanceID names the producing gateway instance.
	InstanceID string
	// Seq orders items within an instance.
	Seq uint64
	// TsNs is the producer-stamped capture time in unix nanoseconds.
	TsNs int64
	// GatewayID names the registered gateway whose HMAC trusted this item.
	GatewayID string
	// Body is the raw captured bytes.
	Body []byte
	// CapturedAt is the server-side store time.
	CapturedAt time.Time
}

// UpsertPayload stores one captured item, idempotent on its composite key
// (correlation_id, kind, instance_id, seq) so a re-pushed item converges.
func (s *Store) UpsertPayload(ctx context.Context, p Payload) error {
	_, err := s.db.Exec(ctx, `
INSERT INTO request_payloads (correlation_id, kind, instance_id, seq, ts_ns, gateway_id, body)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (correlation_id, kind, instance_id, seq) DO UPDATE SET
  body = EXCLUDED.body, ts_ns = EXCLUDED.ts_ns, gateway_id = EXCLUDED.gateway_id`,
		p.CorrelationID, p.Kind, p.InstanceID, int64(p.Seq), p.TsNs, p.GatewayID, p.Body) //nolint:gosec // seq is small, app-assigned
	if err != nil {
		return fmt.Errorf("store: upsert payload: %w", err)
	}
	return nil
}

// ListPayloads returns all captured items for a correlation id in the stable
// composite order (ts_ns, instance_id, seq) — invariant #8 — or
// ErrPayloadNotFound when there are none.
func (s *Store) ListPayloads(ctx context.Context, correlationID string) ([]Payload, error) {
	rows, err := s.db.Query(ctx, `
SELECT correlation_id, kind, instance_id, seq, ts_ns, gateway_id, body, captured_at
FROM request_payloads WHERE correlation_id=$1 ORDER BY ts_ns, instance_id, seq`, correlationID)
	if err != nil {
		return nil, fmt.Errorf("store: list payloads: %w", err)
	}
	defer rows.Close()

	var out []Payload
	for rows.Next() {
		var (
			p   Payload
			seq int64
		)
		if err := rows.Scan(&p.CorrelationID, &p.Kind, &p.InstanceID, &seq, &p.TsNs, &p.GatewayID, &p.Body, &p.CapturedAt); err != nil {
			return nil, fmt.Errorf("store: scan payload: %w", err)
		}
		p.Seq = uint64(seq) //nolint:gosec // seq from BIGINT, always >= 0
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrPayloadNotFound
	}
	return out, nil
}
