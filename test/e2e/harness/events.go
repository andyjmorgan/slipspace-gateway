//go:build e2e

package harness

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/vmihailenco/msgpack/v5"
)

// PayloadMode mirrors internal/bus.PayloadMode without importing it (private
// package). Keep in sync with the NATS Reporting design note.
type PayloadMode uint8

const (
	// PayloadInline marks an Envelope whose payload travels inline in InlinePayload.
	PayloadInline PayloadMode = 0

	// PayloadStashed marks an Envelope whose payload was uploaded to the
	// Object Store and is referenced by ObjectRef.
	PayloadStashed PayloadMode = 1
)

// Envelope is the on-the-wire shape published to `gateway.*` subjects.
// Field tags mirror the NATS Reporting (Envelope Pattern) design note.
type Envelope struct {
	SchemaVersion int         `msgpack:"v"`
	EventID       string      `msgpack:"id"`
	EventType     string      `msgpack:"t"`
	Timestamp     time.Time   `msgpack:"ts"`
	Mode          PayloadMode `msgpack:"m"`

	InlinePayload []byte     `msgpack:"p,omitempty"`
	ObjectRef     *ObjectRef `msgpack:"o,omitempty"`
}

// ObjectRef points at a stashed payload in the GATEWAY_EVENT_STASH bucket.
type ObjectRef struct {
	Bucket string `msgpack:"b"`
	Key    string `msgpack:"k"`
	Size   int64  `msgpack:"s"`
}

// ExpectEvent waits up to timeout for a message on subject and decodes the
// payload as an Envelope. Fails the test on timeout or decode error.
//
// Subscription is core-NATS (not JetStream-pull) since the gateway publishes
// to the JetStream-backed `gateway.>` subjects — core subscribers receive
// the same messages.
func (h *Harness) ExpectEvent(subject string, timeout time.Duration) Envelope {
	h.T.Helper()
	env, err := h.waitEvent(subject, timeout)
	if err != nil {
		h.T.Fatalf("harness: expect event %q: %v", subject, err)
	}
	return env
}

// ExpectNoEvent asserts that no message arrives on subject within window.
func (h *Harness) ExpectNoEvent(subject string, window time.Duration) {
	h.T.Helper()
	if _, err := h.waitEvent(subject, window); err == nil {
		h.T.Fatalf("harness: expected silence on %q within %s, got event", subject, window)
	} else if !errors.Is(err, errEventTimeout) {
		h.T.Fatalf("harness: expect-no-event %q: %v", subject, err)
	}
}

var errEventTimeout = errors.New("timeout waiting for event")

func (h *Harness) waitEvent(subject string, timeout time.Duration) (Envelope, error) {
	if h.NATS == nil {
		return Envelope{}, errors.New("nats connection not initialized")
	}

	inbox := "gateway-events-consumer-" + randomID()
	ch := make(chan *nats.Msg, 16)

	sub, err := h.NATS.ChanSubscribe(subject, ch)
	if err != nil {
		return Envelope{}, fmt.Errorf("subscribe %s: %w", subject, err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	_ = inbox

	select {
	case msg := <-ch:
		var env Envelope
		if err := msgpack.Unmarshal(msg.Data, &env); err != nil {
			return Envelope{}, fmt.Errorf("decode envelope: %w", err)
		}
		return env, nil
	case <-time.After(timeout):
		return Envelope{}, errEventTimeout
	}
}

func randomID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(b[:])
}
