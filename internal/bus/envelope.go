package bus

import "time"

// PayloadMode discriminates how an Envelope's payload is carried.
type PayloadMode uint8

const (
	// PayloadInline marks an Envelope whose payload travels in InlinePayload.
	PayloadInline PayloadMode = 0

	// PayloadStashed marks an Envelope whose payload was uploaded to the
	// Object Store and is referenced by ObjectRef.
	PayloadStashed PayloadMode = 1
)

// SchemaVersion is the current wire schema version published on the bus.
const SchemaVersion = 1

// Envelope is the on-the-wire shape published to gateway.* subjects.
// Field tags match the NATS Reporting (Envelope Pattern) design note and
// the decoder in test/e2e/harness/events.go.
type Envelope struct {
	// SchemaVersion is bumped when the wire shape changes incompatibly.
	// Publisher.Publish stamps it to SchemaVersion when callers leave it
	// zero, so producers cannot accidentally publish version-less events.
	SchemaVersion int `msgpack:"v"`

	// EventID is the unique identifier for this envelope. Required, and
	// reused as the Object Store key when the payload is stashed.
	EventID string `msgpack:"id"`

	// EventType is the suffix appended to subjectPrefix to form the NATS
	// subject the envelope is published on (e.g. "request" →
	// "gateway.request").
	EventType string `msgpack:"t"`

	Timestamp time.Time `msgpack:"ts"`

	// Mode discriminates between an inline payload and a stashed payload
	// referenced via ObjectRef.
	Mode PayloadMode `msgpack:"m"`

	// InlinePayload carries the event body when Mode == PayloadInline.
	// Subscribers should consult Mode rather than testing this field for
	// nil — an inline-mode envelope with an empty body is legal.
	InlinePayload []byte `msgpack:"p,omitempty"`

	// ObjectRef carries the stash location when Mode == PayloadStashed.
	ObjectRef *ObjectRef `msgpack:"o,omitempty"`
}

// ObjectRef points at a stashed payload in the GATEWAY_EVENT_STASH bucket.
type ObjectRef struct {
	// Bucket is the Object Store bucket name the payload lives in.
	Bucket string `msgpack:"b"`

	// Key is the Object Store key — always equal to the envelope's
	// EventID, so consumers can correlate without re-deriving it.
	Key string `msgpack:"k"`

	// Size is the byte length of the stashed payload as reported by the
	// Object Store (falling back to the pre-put inline length when the
	// store reports zero).
	Size int64 `msgpack:"s"`
}
