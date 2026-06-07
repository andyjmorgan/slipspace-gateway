package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/stitch"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// Queries is the read slice of the store the console API serves. *store.Store
// satisfies it; narrowed to an interface so the API is testable with a fake.
type Queries interface {
	QueryDashboardSummary(ctx context.Context, p store.DashboardParams) (store.DashboardSummary, error)
	QueryDashboardSeries(ctx context.Context, p store.DashboardSeriesParams) ([]store.DashboardSeriesBucket, error)
	ListEventsFiltered(ctx context.Context, p store.EventListParams) ([]store.RequestEvent, string, error)
	GetRequestEvent(ctx context.Context, correlationID string) (store.RequestEvent, error)
	ListPayloads(ctx context.Context, correlationID string) ([]store.Payload, error)
	EventsBySession(ctx context.Context, sessionID string) ([]store.RequestEvent, error)
	Facets(ctx context.Context) (store.Facets, error)
}

// registerQueryRoutes mounts the Basic-auth-gated, DB-backed console API.
func (s *Server) registerQueryRoutes(mux *http.ServeMux) {
	if s.queries == nil {
		return
	}
	gated := func(h http.HandlerFunc) http.Handler { return s.basicAuth(h) }
	// Parity surface — emits contracts/admin shapes so the shared SPA
	// observability components decode without translation.
	mux.Handle("GET /api/v1/dashboard/summary", gated(s.handleObsSummary))
	mux.Handle("GET /api/v1/dashboard/timeseries", gated(s.handleObsTimeseries))
	mux.Handle("GET /api/v1/messages/recent", gated(s.handleObsMessagesRecent))
	mux.Handle("GET /api/v1/messages/{id}/body", gated(s.handleObsMessageBody))
	// Message browser — filtered + keyset-paged events in the rich MessageEntry
	// shape, plus the cached distinct-value facets that drive its dropdowns.
	mux.Handle("GET /api/v1/messages", gated(s.handleObsMessages))
	mux.Handle("GET /api/v1/facets", gated(s.handleFacets))
	// Telemetry-native extras (keyset event paging, stitched inspector, session
	// rollup) — used by the telemetry shell beyond the gateway parity surface.
	mux.Handle("GET /api/v1/events", gated(s.handleEvents))
	mux.Handle("GET /api/v1/events/{id}", gated(s.handleEventInspector))
	mux.Handle("GET /api/v1/events/{id}/body", gated(s.handleEventBody))
	mux.Handle("GET /api/v1/sessions/{id}", gated(s.handleSession))
}

// filterFromQuery reads the shared equality/status filters from the query
// string. The message browser adds exact session/correlation lookups and a
// repeated ?tags= multi-value param (AND containment).
func filterFromQuery(r *http.Request) store.EventFilter {
	q := r.URL.Query()
	return store.EventFilter{
		Configuration: q.Get("configuration"),
		Gateway:       q.Get("gateway"),
		Model:         q.Get("model"),
		Provider:      q.Get("provider"),
		Protocol:      q.Get("protocol"),
		StatusClass:   q.Get("status_class"),
		SessionID:     q.Get("session_id"),
		CorrelationID: q.Get("correlation_id"),
		AgentID:       q.Get("agent_id"),
		UserID:        q.Get("user_id"),
		Tags:          nonEmpty(q["tags"]),
	}
}

// nonEmpty drops blank entries so a stray ?tags= doesn't add an empty-string
// predicate that no event would match.
func nonEmpty(in []string) []string {
	var out []string
	for _, v := range in {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// parseWindowBounds reads optional RFC3339 ?from / ?to bounds. A bad value is a
// reported error; an absent bound is the zero time (no predicate), so the
// browser can page across all history unless explicitly bounded.
func parseWindowBounds(r *http.Request) (from, to time.Time, badParam string) {
	q := r.URL.Query()
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, "from"
		}
		from = t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, "to"
		}
		to = t
	}
	return from, to, ""
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	// Events list omits the default window unless from/to are given, so the
	// browser can page across all history; only an explicit ?from bounds it.
	from, to, bad := parseWindowBounds(r)
	if bad != "" {
		writeError(w, http.StatusBadRequest, "invalid "+bad)
		return
	}
	q := r.URL.Query()
	events, next, err := s.queries.ListEventsFiltered(r.Context(), store.EventListParams{
		From:   from,
		To:     to,
		Filter: filterFromQuery(r),
		Cursor: q.Get("cursor"),
		Limit:  intParam(r, "limit", 0),
	})
	if err != nil {
		if errors.Is(err, store.ErrInvalidCursor) {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		s.queryError(w, "list events", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "next_cursor": next})
}

func (s *Server) handleEventInspector(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	event, err := s.queries.GetRequestEvent(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrRequestEventNotFound) {
			writeError(w, http.StatusNotFound, "event not found")
			return
		}
		s.queryError(w, "get event", err)
		return
	}
	payloads, err := s.queries.ListPayloads(r.Context(), id)
	if err != nil && !errors.Is(err, store.ErrPayloadNotFound) {
		s.queryError(w, "list payloads", err)
		return
	}
	writeJSON(w, http.StatusOK, stitch.BuildRequestView(event, payloads))
}

func (s *Server) handleEventBody(w http.ResponseWriter, r *http.Request) {
	payloads, err := s.queries.ListPayloads(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrPayloadNotFound) {
			writeError(w, http.StatusNotFound, "no payloads")
			return
		}
		s.queryError(w, "list payloads", err)
		return
	}
	writeJSON(w, http.StatusOK, stitch.LatestPayloadsByKind(payloads))
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	events, err := s.queries.EventsBySession(r.Context(), id)
	if err != nil {
		s.queryError(w, "session", err)
		return
	}
	if len(events) == 0 {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, mapSession(id, events))
}

// queryError logs a query failure and returns 500 without leaking detail.
func (s *Server) queryError(w http.ResponseWriter, op string, err error) {
	s.log.Warn("query failed", "op", op, "err", err)
	writeError(w, http.StatusInternalServerError, op)
}

// intParam reads an integer query param, returning def when absent or invalid.
func intParam(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
