package connector

import (
	"context"

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
)

// Connector ships sealed segments to a configured destination. The
// spooler owns per-segment retry, per-destination circuit-breaker, and
// segment-file lifecycle (the file at SealedSegment.Path stays on disk
// after a non-permanent Upload error so the spooler can retry).
//
// Implementations return:
//   - nil on 2xx-equivalent success — the spooler deletes the file
//   - *contracts/connector.Retryable for 5xx / timeout / 429 — the spooler
//     applies backoff and retries
//   - *contracts/connector.Permanent for 4xx (not 429) / auth / signature
//     errors — the spooler moves the file to the deadletter directory
//   - any other (untyped) error is treated as retryable; impls SHOULD
//     wrap explicitly to avoid surprising the spooler
//
// Upload may be called multiple times for the same SealedSegment
// (retry-on-Retryable). Implementations MUST be idempotent on the
// destination side — use the SealedSegment.DeliveryID for idempotency
// headers / object keys / dedupe semantics.
type Connector interface {
	// Name returns the operator-configured name (matches YAML).
	Name() string

	// Type returns the connector type identifier ("s3", "azure_blob",
	// "webhook", "testfs"). Used for metric labels and recovery routing.
	Type() string

	// Upload ships a single sealed segment. ctx carries the per-attempt
	// deadline; honour it.
	Upload(ctx context.Context, segment cc.SealedSegment) error
}
