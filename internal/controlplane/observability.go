package controlplane

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/receipt"
)

// eventReader is the read slice of the request_events store the observability
// API needs: the recent-list + drill-down surface, plus the filtered/paginated
// query and the dashboard stats rollup the fleet console renders.
type eventReader interface {
	ListRecentRequestEvents(ctx context.Context, limit int) ([]configdb.RequestEvent, error)
	GetRequestEvent(ctx context.Context, correlationID string) (configdb.RequestEvent, error)
	ListEventsFiltered(ctx context.Context, p configdb.EventListParams) ([]configdb.RequestEvent, string, error)
	QueryEventStats(ctx context.Context, p configdb.EventStatsParams) (configdb.EventStats, error)
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
	mux.HandleFunc("GET /api/v1/observability/stats", h.stats)
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

// eventsEnvelope is the paginated /events response: the page of events plus the
// opaque keyset cursor to fetch the next page (empty on the last page).
type eventsEnvelope struct {
	Events     []eventView `json:"events"`
	NextCursor string      `json:"next_cursor"`
}

// listEvents returns a filtered, keyset-paginated page of events newest-first.
// It accepts an optional [from, to) window, the shared dimension filters, an
// opaque cursor, and a limit (default 100, max 500). The response is an envelope
// carrying the events and the next cursor — a breaking change from the former
// bare array, consumed by the historical-messages browser.
func (h *ObservabilityHandler) listEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, ok := parseTimeRange(w, q, false)
	if !ok {
		return
	}
	limit := 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	events, next, err := h.store.ListEventsFiltered(r.Context(), configdb.EventListParams{
		From:   from,
		To:     to,
		Filter: parseFilter(q),
		Cursor: q.Get("cursor"),
		Limit:  limit,
	})
	if errors.Is(err, configdb.ErrInvalidCursor) {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list events")
		return
	}
	views := make([]eventView, 0, len(events))
	for _, e := range events {
		views = append(views, toEventView(e))
	}
	writeJSON(w, http.StatusOK, eventsEnvelope{Events: views, NextCursor: next})
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

// statsResponse is the dashboard stats payload: the echoed window, headline
// totals, the per-bucket time series, and (when group_by is set) the top-10
// breakdown. The JSON shape is pinned here against the frontend contract.
type statsResponse struct {
	From          time.Time        `json:"from"`
	To            time.Time        `json:"to"`
	BucketSeconds int              `json:"bucket_seconds"`
	Totals        statTotalsView   `json:"totals"`
	Series        []statBucketView `json:"series"`
	Top           []statGroupView  `json:"top,omitempty"`
}

type statTotalsView struct {
	Requests     int64   `json:"requests"`
	TokensIn     int64   `json:"tokens_in"`
	TokensOut    int64   `json:"tokens_out"`
	Errors       int64   `json:"errors"`
	ErrorRate    float64 `json:"error_rate"`
	AvgLatencyMs int64   `json:"avg_latency_ms"`
	P95LatencyMs int64   `json:"p95_latency_ms"`
}

type statBucketView struct {
	Ts           time.Time `json:"ts"`
	Requests     int64     `json:"requests"`
	TokensIn     int64     `json:"tokens_in"`
	TokensOut    int64     `json:"tokens_out"`
	AvgLatencyMs int64     `json:"avg_latency_ms"`
	P95LatencyMs int64     `json:"p95_latency_ms"`
	Status2xx    int64     `json:"status_2xx"`
	Status4xx    int64     `json:"status_4xx"`
	Status5xx    int64     `json:"status_5xx"`
}

type statGroupView struct {
	Key          string  `json:"key"`
	Requests     int64   `json:"requests"`
	TokensIn     int64   `json:"tokens_in"`
	TokensOut    int64   `json:"tokens_out"`
	Errors       int64   `json:"errors"`
	ErrorRate    float64 `json:"error_rate"`
	AvgLatencyMs int64   `json:"avg_latency_ms"`
}

// validGroupBy is the allowlist for the optional group_by param; an unknown
// value is a 400 rather than a silently-ignored breakdown.
var validGroupBy = map[string]bool{
	"model":         true,
	"configuration": true,
	"backend":       true,
	"gateway":       true,
	"protocol":      true,
}

// validStatusClass is the allowlist for the optional status_class filter.
var validStatusClass = map[string]bool{
	"2xx": true,
	"4xx": true,
	"5xx": true,
}

// stats serves the fleet-dashboard rollup over a required [from, to) window
// bucketed by a required bucket (seconds), narrowed by the shared filters and
// optionally grouped for a top-10 breakdown. All aggregation runs in Postgres.
func (h *ObservabilityHandler) stats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, ok := parseTimeRange(w, q, true)
	if !ok {
		return
	}
	bucket, err := strconv.Atoi(q.Get("bucket"))
	if err != nil || bucket <= 0 {
		writeError(w, http.StatusBadRequest, "bucket must be a positive integer (seconds)")
		return
	}
	if sc := q.Get("status_class"); sc != "" && !validStatusClass[sc] {
		writeError(w, http.StatusBadRequest, "status_class must be one of 2xx/4xx/5xx")
		return
	}
	groupBy := q.Get("group_by")
	if groupBy != "" && !validGroupBy[groupBy] {
		writeError(w, http.StatusBadRequest, "group_by must be one of model/configuration/backend/gateway/protocol")
		return
	}

	stats, err := h.store.QueryEventStats(r.Context(), configdb.EventStatsParams{
		From:          from,
		To:            to,
		BucketSeconds: bucket,
		Filter:        parseFilter(q),
		GroupBy:       groupBy,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query stats")
		return
	}
	writeJSON(w, http.StatusOK, toStatsResponse(from, to, bucket, stats))
}

func toStatsResponse(from, to time.Time, bucket int, s configdb.EventStats) statsResponse {
	out := statsResponse{
		From:          from,
		To:            to,
		BucketSeconds: bucket,
		Totals: statTotalsView{
			Requests:     s.Totals.Requests,
			TokensIn:     s.Totals.TokensIn,
			TokensOut:    s.Totals.TokensOut,
			Errors:       s.Totals.Errors,
			ErrorRate:    s.Totals.ErrorRate,
			AvgLatencyMs: s.Totals.AvgLatencyMs,
			P95LatencyMs: s.Totals.P95LatencyMs,
		},
		Series: make([]statBucketView, 0, len(s.Series)),
	}
	for _, b := range s.Series {
		out.Series = append(out.Series, statBucketView{
			Ts:           b.Ts,
			Requests:     b.Requests,
			TokensIn:     b.TokensIn,
			TokensOut:    b.TokensOut,
			AvgLatencyMs: b.AvgLatencyMs,
			P95LatencyMs: b.P95LatencyMs,
			Status2xx:    b.Status2xx,
			Status4xx:    b.Status4xx,
			Status5xx:    b.Status5xx,
		})
	}
	for _, g := range s.Top {
		out.Top = append(out.Top, statGroupView{
			Key:          g.Key,
			Requests:     g.Requests,
			TokensIn:     g.TokensIn,
			TokensOut:    g.TokensOut,
			Errors:       g.Errors,
			ErrorRate:    g.ErrorRate,
			AvgLatencyMs: g.AvgLatencyMs,
		})
	}
	return out
}

// parseFilter pulls the shared dimension filters off the query string.
func parseFilter(q url.Values) configdb.EventFilter {
	return configdb.EventFilter{
		Configuration: q.Get("configuration"),
		Gateway:       q.Get("gateway"),
		Model:         q.Get("model"),
		Backend:       q.Get("backend"),
		Protocol:      q.Get("protocol"),
		StatusClass:   q.Get("status_class"),
	}
}

// parseTimeRange parses the from/to RFC3339 params. When required, both must be
// present and parse, else it writes a 400 and returns ok=false. When optional, a
// missing bound yields the zero time (no bound) but a present-yet-malformed
// bound is still a 400.
func parseTimeRange(w http.ResponseWriter, q url.Values, required bool) (from, to time.Time, ok bool) {
	var err error
	if from, err = parseRFC3339(q.Get("from"), required); err != nil {
		writeError(w, http.StatusBadRequest, "from must be RFC3339")
		return time.Time{}, time.Time{}, false
	}
	if to, err = parseRFC3339(q.Get("to"), required); err != nil {
		writeError(w, http.StatusBadRequest, "to must be RFC3339")
		return time.Time{}, time.Time{}, false
	}
	if !from.IsZero() && !to.IsZero() && !to.After(from) {
		writeError(w, http.StatusBadRequest, "to must be after from")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func parseRFC3339(s string, required bool) (time.Time, error) {
	if s == "" {
		if required {
			return time.Time{}, errors.New("missing")
		}
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
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
