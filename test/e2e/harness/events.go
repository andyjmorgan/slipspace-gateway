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
// The harness establishes a wildcard core-NATS subscription on `gateway.>`
// at boot, before the gateway starts publishing, so callers may invoke this
// method *after* triggering the request and still observe the resulting
// envelope. Without that pre-subscription the test would race the gateway's
// non-blocking publish path.
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

// startEventTap installs a wildcard subscription on gateway.> that buffers
// every received message into eventBuf. Must be called before the gateway
// is started so no event is missed.
func (h *Harness) startEventTap() error {
	if h.NATS == nil {
		return errors.New("nats connection not initialized")
	}
	h.eventBuf = make(chan *nats.Msg, 256)
	sub, err := h.NATS.ChanSubscribe("gateway.>", h.eventBuf)
	if err != nil {
		return fmt.Errorf("subscribe gateway.>: %w", err)
	}
	h.eventSub = sub
	return nil
}

func (h *Harness) waitEvent(subject string, timeout time.Duration) (Envelope, error) {
	if h.eventBuf == nil {
		return Envelope{}, errors.New("event tap not started")
	}

	deadline := time.After(timeout)
	for {
		select {
		case msg := <-h.eventBuf:
			if !subjectMatches(subject, msg.Subject) {
				continue
			}
			var env Envelope
			if err := msgpack.Unmarshal(msg.Data, &env); err != nil {
				return Envelope{}, fmt.Errorf("decode envelope: %w", err)
			}
			return env, nil
		case <-deadline:
			return Envelope{}, errEventTimeout
		}
	}
}

// subjectMatches reports whether actual satisfies a NATS-style subscription
// pattern (with `*` token wildcards and a trailing `>` deep-match). Used by
// the harness to filter the gateway.> tap down to the subject a test cares
// about.
func subjectMatches(pattern, actual string) bool {
	if pattern == actual {
		return true
	}
	pTokens := splitSubject(pattern)
	aTokens := splitSubject(actual)
	for i, p := range pTokens {
		if p == ">" {
			return true
		}
		if i >= len(aTokens) {
			return false
		}
		if p != "*" && p != aTokens[i] {
			return false
		}
	}
	return len(pTokens) == len(aTokens)
}

func splitSubject(s string) []string {
	out := make([]string, 0, 4)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func randomID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(b[:])
}
