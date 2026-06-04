package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/registry"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// Webhook headers a gateway sets on each payload push.
const (
	// HeaderGatewayID names the registered gateway the push claims to be from.
	HeaderGatewayID = "X-Sluice-Gateway-Id"
	// HeaderSignature carries the hex HMAC-SHA256 of the raw request body.
	HeaderSignature = "X-Sluice-Signature"
)

// maxPayloadBytes caps a single webhook body. Large but bounded — a runaway
// payload is rejected rather than buffered without limit.
const maxPayloadBytes = 8 << 20 // 8 MiB

// payloadVerifier is the registry slice the handler needs: HMAC trust.
type payloadVerifier interface {
	Verify(gatewayID string, body []byte, sigHex string) error
}

// payloadWriter is the store slice the handler writes through.
type payloadWriter interface {
	UpsertPayload(ctx context.Context, p store.Payload) error
}

// PayloadEnvelope is the JSON wire shape of one large-payload webhook. The HMAC
// in HeaderSignature is computed over the raw bytes of this envelope.
type PayloadEnvelope struct {
	// CorrelationID joins the payload to its request_event.
	CorrelationID string `json:"correlation_id"`
	// Kind is one of store.Kind* — request_body / response_body / sse_rollup.
	Kind string `json:"kind"`
	// InstanceID names the producing gateway instance.
	InstanceID string `json:"instance_id"`
	// Seq orders items within an instance.
	Seq uint64 `json:"seq"`
	// TsNs is the producer capture time in unix nanoseconds.
	TsNs int64 `json:"ts_ns"`
	// Body is the captured item (kept as raw JSON; the records are JSON).
	Body json.RawMessage `json:"body"`
}

// validKinds is the accepted payload-kind set; anything else is rejected so a
// typo'd kind never silently lands as an un-renderable row.
var validKinds = map[string]struct{}{
	store.KindRequestBody:  {},
	store.KindResponseBody: {},
	store.KindSSERollup:    {},
}

// WebhookHandler accepts one HMAC-trusted large-payload item per HTTP request
// and writes it to the store as a single row. It is the gateway→service trust
// boundary: an item is stored iff its signature verifies against the claimed
// gateway's registered secret.
type WebhookHandler struct {
	registry payloadVerifier
	store    payloadWriter
	logger   *slog.Logger
}

// NewWebhookHandler builds the payload webhook handler.
func NewWebhookHandler(reg payloadVerifier, st payloadWriter, logger *slog.Logger) *WebhookHandler {
	return &WebhookHandler{registry: reg, store: st, logger: logger}
}

// ServeHTTP handles POST of a single payload envelope. It verifies the HMAC,
// validates the envelope, and upserts one row.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()

	gatewayID := r.Header.Get(HeaderGatewayID)
	sig := r.Header.Get(HeaderSignature)
	if gatewayID == "" || sig == "" {
		writeError(w, http.StatusUnauthorized, "missing gateway id or signature")
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPayloadBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "payload too large")
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

	var env PayloadEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		writeError(w, http.StatusBadRequest, "malformed envelope")
		return
	}
	if env.CorrelationID == "" {
		writeError(w, http.StatusBadRequest, "missing correlation_id")
		return
	}
	if _, ok := validKinds[env.Kind]; !ok {
		writeError(w, http.StatusBadRequest, "unknown payload kind")
		return
	}

	if err := h.store.UpsertPayload(r.Context(), store.Payload{
		CorrelationID: env.CorrelationID,
		Kind:          env.Kind,
		InstanceID:    env.InstanceID,
		Seq:           env.Seq,
		TsNs:          env.TsNs,
		GatewayID:     gatewayID,
		Body:          env.Body,
	}); err != nil {
		h.logger.Warn("payload ingest: upsert", "correlation_id", env.CorrelationID, "error", err)
		writeError(w, http.StatusInternalServerError, "store payload")
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
