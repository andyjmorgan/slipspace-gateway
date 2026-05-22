package connector

// SealedSegment is the unit a Connector.Upload ships. The file at Path is
// owned by the spooler; connectors MUST NOT mutate or delete it — the
// spooler cleans up after Upload returns. Connectors read the file zero
// or more times (e.g. for retries) but must treat its contents as
// immutable.
//
// A SealedSegment corresponds to one ndjson.zst file on disk holding
// Records records, with timestamps in [TsMinNs, TsMaxNs].
type SealedSegment struct {
	// Path is the absolute path on disk to the ndjson.zst file.
	Path string

	// Bytes is the compressed file size in bytes.
	Bytes int64

	// BytesUncompressed is the sum of record byte lengths before zstd.
	// Used for the manifest's bytes_uncompressed field.
	BytesUncompressed int64

	// Records is the number of records in the segment.
	Records int

	// TsMinNs and TsMaxNs are the min/max [Record.TsNs] across records in
	// the segment. Used by the manifest emitter.
	TsMinNs int64

	TsMaxNs int64

	// DeliveryID is a ULID generated when the segment is sealed and
	// remains stable across upload retries. Connectors use it for
	// idempotency headers (e.g. X-Sluice-Delivery-Id) and as part of the
	// object key for S3/Azure destinations.
	DeliveryID string

	// Connector is the name of the connector this segment belongs to.
	// Connectors should match against their own Name() during Upload
	// for defence in depth, but the spooler guarantees correct routing.
	Connector string
}
