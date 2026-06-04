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
}

// defaultWindow is the look-back applied when a request omits ?from.
const defaultWindow = time.Hour

// registerQueryRoutes mounts the Basic-auth-gated, DB-backed console API.
func (s *Server) registerQueryRoutes(mux *http.ServeMux) {
	if s.queries == nil {
		return
	}
	gated := func(h http.HandlerFunc) http.Handler { return s.basicAuth(h) }
	mux.Handle("GET /api/v1/dashboard/summary", gated(s.handleDashboardSummary))
	mux.Handle("GET /api/v1/dashboard/series", gated(s.handleDashboardSeries))
	mux.Handle("GET /api/v1/events", gated(s.handleEvents))
	mux.Handle("GET /api/v1/events/{id}", gated(s.handleEventInspector))
	mux.Handle("GET /api/v1/events/{id}/body", gated(s.handleEventBody))
	mux.Handle("GET /api/v1/sessions/{id}", gated(s.handleSession))
}

// timeWindow parses ?from / ?to (RFC3339), defaulting to [now-defaultWindow, now)
// when absent. now is taken from the request deadline-free; callers pass it so
// tests are deterministic.
func timeWindow(r *http.Request, now time.Time) (from, to time.Time, err error) {
	to = now
	from = now.Add(-defaultWindow)
	if v := r.URL.Query().Get("from"); v != "" {
		if from, err = time.Parse(time.RFC3339, v); err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid from")
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if to, err = time.Parse(time.RFC3339, v); err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid to")
		}
	}
	return from, to, nil
}

// filterFromQuery reads the shared equality/status filters from the query string.
func filterFromQuery(r *http.Request) store.EventFilter {
	q := r.URL.Query()
	return store.EventFilter{
		Configuration: q.Get("configuration"),
		Gateway:       q.Get("gateway"),
		Model:         q.Get("model"),
		Backend:       q.Get("backend"),
		Protocol:      q.Get("protocol"),
		StatusClass:   q.Get("status_class"),
	}
}

func (s *Server) handleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	from, to, err := timeWindow(r, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	summary, err := s.queries.QueryDashboardSummary(r.Context(), store.DashboardParams{
		From:       from,
		To:         to,
		RecentFrom: to.Add(-5 * time.Minute),
		Filter:     filterFromQuery(r),
	})
	if err != nil {
		s.queryError(w, "dashboard summary", err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleDashboardSeries(w http.ResponseWriter, r *http.Request) {
	from, to, err := timeWindow(r, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	bucket := intParam(r, "bucket_seconds", 60)
	if bucket <= 0 {
		writeError(w, http.StatusBadRequest, "bucket_seconds must be positive")
		return
	}
	series, err := s.queries.QueryDashboardSeries(r.Context(), store.DashboardSeriesParams{
		From: from, To: to, BucketSeconds: bucket, Filter: filterFromQuery(r),
	})
	if err != nil {
		s.queryError(w, "dashboard series", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	// Events list omits the default window unless from/to are given, so the
	// browser can page across all history; only an explicit ?from bounds it.
	var from, to time.Time
	q := r.URL.Query()
	if v := q.Get("from"); v != "" {
		t, perr := time.Parse(time.RFC3339, v)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid from")
			return
		}
		from = t
	}
	if v := q.Get("to"); v != "" {
		t, perr := time.Parse(time.RFC3339, v)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid to")
			return
		}
		to = t
	}
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
	writeJSON(w, http.StatusOK, stitch.BuildSessionView(id, events))
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
