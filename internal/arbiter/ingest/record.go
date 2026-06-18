package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/registry"
)

// Webhook headers a gateway sets on each Record push.
const (
	// HeaderGatewayID names the registered gateway the push claims to be from.
	HeaderGatewayID = "X-Sluice-Gateway-Id"
	// HeaderSignature carries the hex HMAC-SHA256 of the raw request body.
	HeaderSignature = "X-Sluice-Signature"
)

// maxRecordBytes caps a single Record POST. The gateway bounds captured bodies
// at 10 MiB inbound; this leaves headroom for the JSON envelope around them. A
// runaway record is rejected rather than buffered without limit.
const maxRecordBytes = 16 << 20 // 16 MiB

// recordVerifier is the registry slice the handler needs: HMAC trust.
type recordVerifier interface {
	Verify(gatewayID string, body []byte, sigHex string) error
}

// recordWriter is the store slice the Record handler writes through: the lazy
// verbatim blob keyed by correlation_id. Single-writer model — the Record never
// touches request_events; it only lands its own raw bytes for lazy join.
type recordWriter interface {
	UpsertRecord(ctx context.Context, correlationID string, receivedAt time.Time, body []byte) error
}

// RecordHandler accepts one HMAC-trusted cc.Record per HTTP request — the full
// per-request digital record the gateway pushes in real time — and stores the
// RAW request bytes verbatim into the `record` table, keyed by correlation_id.
// It is the gateway→service trust boundary: a record is stored iff its signature
// verifies against the claimed gateway's registered secret.
//
// Single-writer decision (Telemetry Rearchitecture design note): the Record no
// longer writes request_events or per-item payload rows. The console's
// record/inspector tab fetches this blob lazily, json.Unmarshals it into
// cc.Record, and renders the rule lifecycle + raw bodies/headers. An absent row
// simply means reporting forwarding was off (or the push has not arrived).
type RecordHandler struct {
	registry recordVerifier
	store    recordWriter
	logger   *slog.Logger
}

// NewRecordHandler builds the Record ingest handler.
func NewRecordHandler(reg recordVerifier, st recordWriter, logger *slog.Logger) *RecordHandler {
	return &RecordHandler{registry: reg, store: st, logger: logger}
}

// ServeHTTP handles POST of a single cc.Record. It verifies the HMAC over the
// raw bytes, sanity-checks the correlation id, then stores the raw bytes
// verbatim. No fan-out into columns / detail / payload rows.
func (h *RecordHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()

	gatewayID := r.Header.Get(HeaderGatewayID)
	sig := r.Header.Get(HeaderSignature)
	if gatewayID == "" || sig == "" {
		writeError(w, http.StatusUnauthorized, "missing gateway id or signature")
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRecordBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "record too large")
		return
	}

	if err := h.registry.Verify(gatewayID, raw, sig); err != nil {
		// Both unknown-gateway and bad-signature collapse to 401 — never reveal
		// which check failed.
		if errors.Is(err, registry.ErrUnknownGateway) || errors.Is(err, registry.ErrBadSignature) {
			writeError(w, http.StatusUnauthorized, "signature rejected")
			return
		}
		writeError(w, http.StatusInternalServerError, "verify")
		return
	}

	// Decode only far enough to read the correlation id (the join key) — the
	// body is then stored verbatim, undisturbed, so the inspector sees exactly
	// what the gateway signed.
	var rec cc.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		writeError(w, http.StatusBadRequest, "malformed record")
		return
	}
	if rec.CorrelationID == "" {
		writeError(w, http.StatusBadRequest, "missing correlation_id")
		return
	}

	if err := h.store.UpsertRecord(r.Context(), rec.CorrelationID, time.Now().UTC(), raw); err != nil {
		h.logger.Warn("record ingest: upsert record", "correlation_id", rec.CorrelationID, "error", err)
		writeError(w, http.StatusInternalServerError, "store record")
		return
	}

	writeJSON(w, http.StatusOK, map[string]int{"stored": 1})
}

// writeJSON writes a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON {"error":msg} body with the given status.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
