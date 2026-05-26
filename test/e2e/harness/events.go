//go:build e2e

package harness

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/contracts/events"
)

// PayloadMode is the legacy mode constant the in-tree harness API
// historically carried over from the NATS reporting envelope. Kept
// here for backwards-compatible test code; only PayloadInline is
// emitted by the connector-spool harness.
type PayloadMode uint8

const (
	// PayloadInline marks an Envelope whose payload travels inline in
	// InlinePayload.
	PayloadInline PayloadMode = 0

	// PayloadStashed marked NATS-era envelopes whose payload was
	// stashed in the JetStream Object Store. Retained as an exported
	// constant so existing tests that compare against it still
	// compile; the connector path never emits it.
	PayloadStashed PayloadMode = 1
)

// Envelope is the legacy in-tree shape ExpectEvent returns. Tests pull
// InlinePayload + json.Unmarshal it into events.Request or
// events.RuleMatched. The harness synthesises these envelopes by
// translating captured connector.Records back into events.Request and
// fanning each Record.RulesFired entry out as a separate envelope on
// the gateway.rule.matched subject so test assertions that count
// rule-matched events still work.
type Envelope struct {
	SchemaVersion int
	EventID       string
	EventType     string
	Timestamp     time.Time
	Mode          PayloadMode

	InlinePayload []byte
	ObjectRef     *ObjectRef
}

// ObjectRef is the legacy stashed-payload pointer. Connector path
// never sets this — kept for type-equivalence with the NATS-era API.
type ObjectRef struct {
	Bucket string
	Key    string
	Size   int64
}

// ExpectEvent waits up to timeout for an envelope on the named
// subject. Translates the connector spool's captured Record into the
// legacy Envelope wrapper. Fails the test on timeout.
func (h *Harness) ExpectEvent(subject string, timeout time.Duration) Envelope {
	h.T.Helper()
	env, err := h.waitEvent(subject, timeout)
	if err != nil {
		h.T.Fatalf("harness: expect event %q: %v", subject, err)
	}
	return env
}

// ExpectNoEvent asserts no envelope arrives on subject within window.
func (h *Harness) ExpectNoEvent(subject string, window time.Duration) {
	h.T.Helper()
	if _, err := h.waitEvent(subject, window); err == nil {
		h.T.Fatalf("harness: expected silence on %q within %s, got event", subject, window)
	} else if !errors.Is(err, errEventTimeout) {
		h.T.Fatalf("harness: expect-no-event %q: %v", subject, err)
	}
}

var errEventTimeout = errors.New("timeout waiting for event")

// startCaptureServer spins up the in-process HTTP receiver the
// gateway's webhook connector POSTs sealed segments to. Every POST
// payload is a zstd-compressed ndjson batch of connector.Record;
// captureHandler decompresses, parses each record, and pushes one or
// more pre-translated Envelopes onto eventBuf for ExpectEvent.
func (h *Harness) startCaptureServer() error {
	h.eventBuf = make(chan Envelope, 256)

	srv := httptest.NewServer(http.HandlerFunc(h.captureHandler))
	h.captureServer = srv
	h.captureURL = srv.URL
	return nil
}

// captureHandler decompresses the POST body, parses records, and pushes
// the equivalent legacy envelopes onto eventBuf. The handler always
// returns 202 — the gateway side considers any 2xx a success.
func (h *Harness) captureHandler(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_ = r.Body.Close()

	body := raw
	if strings.Contains(r.Header.Get("Content-Encoding"), "zstd") {
		dec, derr := zstd.NewReader(bytes.NewReader(raw))
		if derr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		decompressed, derr := io.ReadAll(dec)
		dec.Close()
		if derr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body = decompressed
	}

	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var rec cc.Record
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		h.emitRecord(rec)
	}
	w.WriteHeader(http.StatusAccepted)
}

// emitRecord translates one captured Record into the legacy
// Envelope(s) tests expect: one gateway.request envelope per record,
// plus one gateway.rule.matched envelope per rule entry. Order
// matches what the NATS-era publisher emitted so test assertions on
// arrival order continue to pass.
func (h *Harness) emitRecord(rec cc.Record) {
	req := events.Request{
		CorrelationID: rec.CorrelationID,
		Provider:      rec.Provider,
		Endpoint:      rec.Endpoint,
		Model:         rec.Model,
		Method:        rec.Request.Method,
		StatusCode:    rec.UpstreamStatus,
		DurationMs:    durationMsFromTimestamps(rec),
		Streaming:     rec.Response.StreamChunks > 0,
		UpstreamError: rec.UpstreamError,
		Tags:          rec.Tags,
	}
	if rec.Tokens != nil {
		req.TokensIn = rec.Tokens.Input
		req.TokensOut = rec.Tokens.Output
		req.TokensCached = rec.Tokens.Cached
		req.TokensCacheCreation = rec.Tokens.CacheCreation
	}

	req.PolicyRef = rec.PolicyRef
	if len(rec.Attempts) > 0 {
		req.Attempts = make([]events.AttemptRecord, 0, len(rec.Attempts))
		for _, a := range rec.Attempts {
			req.Attempts = append(req.Attempts, events.AttemptRecord{
				Target:     a.Target,
				StartedAt:  time.Unix(0, a.StartedAtNs).UTC(),
				DurationMs: a.DurationMs,
				StatusCode: a.StatusCode,
				Error:      a.Error,
				Outcome:    a.Outcome,
			})
		}
	}

	if payload, err := json.Marshal(req); err == nil {
		h.pushEvelope(Envelope{
			SchemaVersion: 1,
			EventID:       rec.ID,
			EventType:     "request",
			Timestamp:     time.Unix(0, rec.TsNs).UTC(),
			Mode:          PayloadInline,
			InlinePayload: payload,
		})
	}

	for _, rf := range rec.RulesFired {
		rm := events.RuleMatched{
			CorrelationID:  rec.CorrelationID,
			Configuration:  rec.Configuration,
			RuleName:       rf.Name,
			ActionsApplied: rf.ActionsApplied,
			Terminated:     rf.Terminated,
			ErrorMessage:   rf.ErrorMessage,
			MatchedAt:      time.Unix(0, rec.TsNs).UTC(),
		}
		if payload, err := json.Marshal(rm); err == nil {
			h.pushEvelope(Envelope{
				SchemaVersion: 1,
				EventID:       rec.ID + "-rule-" + rf.Name,
				EventType:     "rule.matched",
				Timestamp:     time.Unix(0, rec.TsNs).UTC(),
				Mode:          PayloadInline,
				InlinePayload: payload,
			})
		}
	}
}

// durationMsFromTimestamps recovers the request duration when present
// in the Record's response timing fields. The reporter populates
// LastByteNs as started+DurationMs so this is exact (no rounding).
func durationMsFromTimestamps(rec cc.Record) int64 {
	if rec.Response.LastByteNs > 0 && rec.TsNs > 0 {
		return (rec.Response.LastByteNs - rec.TsNs) / int64(time.Millisecond)
	}
	return 0
}

// pushEvelope sends one envelope into the event buffer. Drops when
// the buffer is full — tests issue ExpectEvent shortly after the
// request, so 256 slots is plenty of headroom for the slowest test.
func (h *Harness) pushEvelope(env Envelope) {
	select {
	case h.eventBuf <- env:
	default:
		// Buffer overflow — tests fell behind. Print so it's visible
		// in the harness log without failing the run; if a test
		// genuinely needs more, raise the buffer size.
		if h.T != nil {
			h.T.Logf("harness: event buffer full, dropping %s", env.EventType)
		}
	}
}

// waitEvent returns the next envelope whose subject satisfies the
// supplied wildcard pattern, or errEventTimeout when none arrives
// within the window.
func (h *Harness) waitEvent(subject string, timeout time.Duration) (Envelope, error) {
	if h.eventBuf == nil {
		return Envelope{}, errors.New("capture server not started")
	}
	wantType := subjectToEventType(subject)

	h.pendingMu.Lock()
	for i, env := range h.pendingEnvelopes {
		if eventTypeMatches(wantType, env.EventType) {
			h.pendingEnvelopes = append(h.pendingEnvelopes[:i], h.pendingEnvelopes[i+1:]...)
			h.pendingMu.Unlock()
			return env, nil
		}
	}
	h.pendingMu.Unlock()

	deadline := time.After(timeout)
	for {
		select {
		case env := <-h.eventBuf:
			if !eventTypeMatches(wantType, env.EventType) {
				h.pendingMu.Lock()
				h.pendingEnvelopes = append(h.pendingEnvelopes, env)
				h.pendingMu.Unlock()
				continue
			}
			return env, nil
		case <-deadline:
			return Envelope{}, errEventTimeout
		}
	}
}

// subjectToEventType maps a NATS-style subject ("gateway.request" or
// "gateway.rule.matched") into the bare event type the harness stores
// on each Envelope.
func subjectToEventType(subject string) string {
	return strings.TrimPrefix(subject, "gateway.")
}

// eventTypeMatches accepts a "<type>" or "<prefix>.>" form. The
// trailing-> is the only wildcard pattern the harness ever sees in
// practice ("gateway.>" maps to "" + ">" suffix).
func eventTypeMatches(wantType, actualType string) bool {
	if wantType == ">" || wantType == "" {
		return true
	}
	if wantType == actualType {
		return true
	}
	if strings.HasSuffix(wantType, ".>") {
		return strings.HasPrefix(actualType, strings.TrimSuffix(wantType, ".>"))
	}
	return false
}

// shutdownCaptureServer closes the httptest.Server and clears any
// pending events. Safe to call multiple times.
func (h *Harness) shutdownCaptureServer() {
	if h.captureServer != nil {
		h.captureServer.Close()
		h.captureServer = nil
	}
}

// randomID returns a hex-encoded 64-bit random identifier. Used for
// the per-harness webhook HMAC secret and tests that synthesise their
// own delivery IDs.
func randomID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(b[:])
}

// pendingEnvelopes guards the per-harness buffer of envelopes
// fetched-but-not-yet-matched by waitEvent. Mirrors the NATS-era
// pendingEvents slice.
type pendingEnvelopeBuffer struct {
	mu        sync.Mutex
	envelopes []Envelope
}

// captureContext is the per-harness context used by captureHandler so
// any out-of-process timing race surfaces as a context error rather
// than blocking forever. Today the handler is synchronous, but the
// hook is here for the eventual streaming-style capture.
var _ = context.Background // referenced for forward compat
