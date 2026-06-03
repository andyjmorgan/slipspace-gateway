package controlplane

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/receipt"
)

// eventReader is the read slice of the request_events store the observability
// API needs.
type eventReader interface {
	ListRecentRequestEvents(ctx context.Context, limit int) ([]configdb.RequestEvent, error)
	GetRequestEvent(ctx context.Context, correlationID string) (configdb.RequestEvent, error)
}

// bodyReader is the read slice of the request_bodies store — the heavy
// audit/replay payloads the spool ships to the CP, joined to events by
// correlation_id.
type bodyReader interface {
	ListRequestBodies(ctx context.Context, correlationID string) ([]configdb.RequestBody, error)
}

// receiptLister reads a gateway's tamper-evidence chain for verification.
type receiptLister interface {
	ListReceipts(ctx context.Context, gatewayID string) ([]receipt.Receipt, error)
}

// ObservabilityHandler serves read-only telemetry — the recent request-event
// list, per-request drill-down, and tamper-evidence chain verification the
// fleet console renders. It is mounted under the authenticated CP HTTP surface
// and reads straight from Postgres.
type ObservabilityHandler struct {
	store    eventReader
	bodies   bodyReader
	receipts receiptLister
	pub      ed25519.PublicKey
	mux      *http.ServeMux
}

// NewObservabilityHandler builds the handler over the request_events store, the
// request_bodies store, the receipt chain store, and the receipt-signing public
// key used to verify chains.
func NewObservabilityHandler(store eventReader, bodies bodyReader, receipts receiptLister, pub ed25519.PublicKey) *ObservabilityHandler {
	h := &ObservabilityHandler{store: store, bodies: bodies, receipts: receipts, pub: pub}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/observability/events", h.listEvents)
	mux.HandleFunc("GET /api/v1/observability/events/{correlation_id}", h.getEvent)
	mux.HandleFunc("GET /api/v1/observability/bodies/{correlation_id}", h.getBodies)
	mux.HandleFunc("GET /api/v1/observability/receipts/{gateway_id}/verify", h.verifyChain)
	h.mux = mux
	return h
}

func (h *ObservabilityHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// eventView is the JSON projection of a request event (configdb types carry no
// json tags, so the API shape is pinned here).
type eventView struct {
	CorrelationID string          `json:"correlation_id"`
	GatewayID     string          `json:"gateway_id"`
	Configuration string          `json:"configuration"`
	Backend       string          `json:"backend"`
	Model         string          `json:"model"`
	Protocol      string          `json:"protocol"`
	StatusCode    int             `json:"status_code"`
	LatencyMs     int64           `json:"latency_ms"`
	TokensIn      int64           `json:"tokens_in"`
	TokensOut     int64           `json:"tokens_out"`
	GenAIContent  json.RawMessage `json:"gen_ai_content,omitempty"`
	ObservedAt    time.Time       `json:"observed_at"`
}

func toEventView(e configdb.RequestEvent) eventView {
	return eventView{
		CorrelationID: e.CorrelationID,
		GatewayID:     e.GatewayID,
		Configuration: e.Configuration,
		Backend:       e.Backend,
		Model:         e.Model,
		Protocol:      e.Protocol,
		StatusCode:    e.StatusCode,
		LatencyMs:     e.LatencyMs,
		TokensIn:      e.TokensIn,
		TokensOut:     e.TokensOut,
		GenAIContent:  json.RawMessage(e.GenAIContent),
		ObservedAt:    e.ObservedAt,
	}
}

// listEvents returns the most recent events, newest first. ?limit caps the
// count (defaulted and bounded by the store).
func (h *ObservabilityHandler) listEvents(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil {
			limit = n
		}
	}
	events, err := h.store.ListRecentRequestEvents(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list events")
		return
	}
	views := make([]eventView, 0, len(events))
	for _, e := range events {
		views = append(views, toEventView(e))
	}
	writeJSON(w, http.StatusOK, views)
}

// getEvent returns one request event by correlation id, 404 if unknown.
func (h *ObservabilityHandler) getEvent(w http.ResponseWriter, r *http.Request) {
	e, err := h.store.GetRequestEvent(r.Context(), r.PathValue("correlation_id"))
	if errors.Is(err, configdb.ErrRequestEventNotFound) {
		writeError(w, http.StatusNotFound, "request event not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get event")
		return
	}
	writeJSON(w, http.StatusOK, toEventView(e))
}

// bodyView is the JSON projection of a captured request body. Body is the raw
// record line the spool shipped, surfaced verbatim as embedded JSON.
type bodyView struct {
	CorrelationID string          `json:"correlation_id"`
	InstanceID    string          `json:"instance_id"`
	Seq           uint64          `json:"seq"`
	TsNs          int64           `json:"ts_ns"`
	Body          json.RawMessage `json:"body"`
	CapturedAt    time.Time       `json:"captured_at"`
}

func toBodyView(b configdb.RequestBody) bodyView {
	return bodyView{
		CorrelationID: b.CorrelationID,
		InstanceID:    b.InstanceID,
		Seq:           b.Seq,
		TsNs:          b.TsNs,
		Body:          json.RawMessage(b.Body),
		CapturedAt:    b.CapturedAt,
	}
}

// getBodies returns every captured body for a correlation id, ordered stably by
// (instance_id, seq) — the full audit/replay payload for one request. 404 when
// none was captured (no connector binding, sampled out, or not yet ingested).
func (h *ObservabilityHandler) getBodies(w http.ResponseWriter, r *http.Request) {
	bodies, err := h.bodies.ListRequestBodies(r.Context(), r.PathValue("correlation_id"))
	if errors.Is(err, configdb.ErrRequestBodyNotFound) {
		writeError(w, http.StatusNotFound, "no captured bodies for correlation id")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list bodies")
		return
	}
	views := make([]bodyView, 0, len(bodies))
	for _, b := range bodies {
		views = append(views, toBodyView(b))
	}
	writeJSON(w, http.StatusOK, views)
}

// chainVerifyView is the result of verifying a gateway's receipt chain — the
// "verified ✓" the console renders, with the failure reason when broken.
type chainVerifyView struct {
	GatewayID string `json:"gateway_id"`
	Count     int    `json:"count"`
	Verified  bool   `json:"verified"`
	Error     string `json:"error,omitempty"`
}

// verifyChain loads a gateway's tamper-evidence chain and verifies it under the
// CP signing key. A broken chain is reported as verified=false with the reason,
// not as an HTTP error — the verification result is the payload.
func (h *ObservabilityHandler) verifyChain(w http.ResponseWriter, r *http.Request) {
	gatewayID := r.PathValue("gateway_id")
	chain, err := h.receipts.ListReceipts(r.Context(), gatewayID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list receipts")
		return
	}
	result := chainVerifyView{GatewayID: gatewayID, Count: len(chain), Verified: true}
	if verr := receipt.VerifyChain(chain, h.pub); verr != nil {
		result.Verified = false
		result.Error = verr.Error()
	}
	writeJSON(w, http.StatusOK, result)
}
