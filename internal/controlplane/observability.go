package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// eventReader is the read slice of the request_events store the observability
// API needs.
type eventReader interface {
	ListRecentRequestEvents(ctx context.Context, limit int) ([]configdb.RequestEvent, error)
	GetRequestEvent(ctx context.Context, correlationID string) (configdb.RequestEvent, error)
}

// ObservabilityHandler serves read-only request-event telemetry — the recent
// list and per-request drill-down the fleet console renders. It is mounted
// under the authenticated CP HTTP surface and reads straight from Postgres.
type ObservabilityHandler struct {
	store eventReader
	mux   *http.ServeMux
}

// NewObservabilityHandler builds the handler over the request_events store.
func NewObservabilityHandler(store eventReader) *ObservabilityHandler {
	h := &ObservabilityHandler{store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/observability/events", h.listEvents)
	mux.HandleFunc("GET /api/v1/observability/events/{correlation_id}", h.getEvent)
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
