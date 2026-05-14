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
	SchemaVersion int `msgpack:"v"`

	EventID string `msgpack:"id"`

	EventType string `msgpack:"t"`

	Timestamp time.Time `msgpack:"ts"`

	Mode PayloadMode `msgpack:"m"`

	InlinePayload []byte `msgpack:"p,omitempty"`

	ObjectRef *ObjectRef `msgpack:"o,omitempty"`
}

// ObjectRef points at a stashed payload in the GATEWAY_EVENT_STASH bucket.
type ObjectRef struct {
	Bucket string `msgpack:"b"`

	Key string `msgpack:"k"`

	Size int64 `msgpack:"s"`
}
