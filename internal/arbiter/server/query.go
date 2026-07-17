package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/stitch"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

// Queries is the read slice of the store the console API serves. *store.Store
// satisfies it; narrowed to an interface so the API is testable with a fake.
type Queries interface {
	QueryDashboardSummary(ctx context.Context, p store.DashboardParams) (store.DashboardSummary, error)
	QueryDashboardSeries(ctx context.Context, p store.DashboardSeriesParams) ([]store.DashboardSeriesBucket, error)
	// QueryDashboardSecurity is the Arbiter posture + finding breakdown over a
	// [from, to) window (verdict/finding tables, window-only).
	QueryDashboardSecurity(ctx context.Context, from, to time.Time) (store.DashboardSecurity, error)
	// ListScanAudit is the append-only operational-scan-failure log over a
	// [from, to) window, newest first, capped at limit (scan_audit table).
	ListScanAudit(ctx context.Context, from, to time.Time, limit int) ([]store.ScanAuditEntry, error)
	ListEventsFiltered(ctx context.Context, p store.EventListParams) ([]store.RequestEvent, string, error)
	CountEventsFiltered(ctx context.Context, p store.EventCountParams) (int64, error)
	GetRequestEvent(ctx context.Context, correlationID string) (store.RequestEvent, error)
	GetRecordBody(ctx context.Context, correlationID string) ([]byte, error)
	ListSessions(ctx context.Context, p store.SessionListParams) ([]store.SessionSummary, string, error)
	CountSessions(ctx context.Context, p store.SessionCountParams) (int64, error)
	// EventsBySessionRollup is the whole-session scan in the NARROW projection
	// (span_event stripped of gen_ai_content) — bounded per row, safe to hold
	// for a session. EventsBySessionPage is the only full-blob session read and
	// is keyset-paged + callback-streamed so a caller never holds more than one
	// full blob at a time.
	EventsBySessionRollup(ctx context.Context, sessionID string) ([]store.RequestEvent, error)
	EventsBySessionPage(ctx context.Context, p store.SessionPageParams, fn func(store.RequestEvent) error) (string, error)
	Facets(ctx context.Context, from, to time.Time) (store.Facets, error)
	// Arbiter security surface — the verdict + findings for one request.
	GetVerdict(ctx context.Context, correlationID string) (store.Verdict, error)
	ListFindings(ctx context.Context, correlationID string) ([]store.Finding, error)
	// ListRecentFindings + ListFindingsBySession back the operator Security view:
	// recent findings across all sessions, and all findings in one session — each
	// joined to its source request facts.
	ListRecentFindings(ctx context.Context, from, to time.Time, limit int) ([]store.FindingRow, error)
	ListFindingsBySession(ctx context.Context, sessionID string) ([]store.FindingRow, error)
	// Tool-Call Index audit surface — the searchable per-call list, a single
	// call by id, and the distinct tool-name facet.
	ListToolCalls(ctx context.Context, p store.ToolCallListParams) ([]store.ToolCall, string, error)
	GetToolCall(ctx context.Context, id string) (store.ToolCall, error)
	ToolNames(ctx context.Context) ([]string, error)
	// Advise audit surface — the judgement log (advise_audit table), newest
	// first, and the measured spend of down-ranked conversations plus the
	// judge's own overhead (keyed on its named-agent id).
	ListAdviseAudit(ctx context.Context, limit int, before time.Time) ([]store.AdviseAuditEntry, error)
	AdviseSavings(ctx context.Context, since time.Time, judgeAgentID string) ([]store.AdviseSavingsRow, float64, error)
}

// registerQueryRoutes mounts the Basic-auth-gated, DB-backed console API.
func (s *Server) registerQueryRoutes(mux *http.ServeMux) {
	if s.queries == nil {
		return
	}
	// gzip sits inside the auth gate so only authenticated JSON pays the
	// compression negotiation; the console's biggest payloads (span pages,
	// event lists) compress >10x.
	gated := func(h http.HandlerFunc) http.Handler { return s.basicAuth(withGzip(h)) }
	// Parity surface — emits contracts/admin shapes so the shared SPA
	// observability components decode without translation.
	mux.Handle("GET /api/v1/dashboard/summary", gated(s.handleObsSummary))
	mux.Handle("GET /api/v1/dashboard/timeseries", gated(s.handleObsTimeseries))
	// Arbiter security posture + finding breakdown for the dashboard's security
	// rows. Returns enabled=false (and skips the query) when the scanner is off.
	mux.Handle("GET /api/v1/dashboard/security", gated(s.handleObsSecurity))
	// Append-only log of operational scan failures (timeout / unreachable /
	// detector_error / no_detector / unit_missing) for the dashboard's scan-
	// failures panel. Like /security, returns empty (and skips the query) when
	// the scanner is off.
	mux.Handle("GET /api/v1/dashboard/security/audit", gated(s.handleObsSecurityAudit))
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
	// Un-scoped single-span DTO — the message browser's inspector renders the
	// same SessionSpan element as the lifecycle modal but reaches it by
	// correlation id alone (its rows aren't guaranteed a session id).
	mux.Handle("GET /api/v1/events/{id}/span", gated(s.handleEventSpan))
	// Arbiter security verdict + findings for one request (correlation id).
	mux.Handle("GET /api/v1/verdict/{id}", gated(s.handleVerdict))
	// Operator Security view — recent findings across all sessions, or all
	// findings in one session (?session=<id>). Backs the reusable FindingsTable
	// the top-level Security page and the session view's Security tab share.
	mux.Handle("GET /api/v1/findings", gated(s.handleFindings))
	mux.Handle("GET /api/v1/sessions", gated(s.handleSessions))
	mux.Handle("GET /api/v1/sessions/{id}", gated(s.handleSession))
	// Session lifecycle feed — the SessionSpansDTO v1 projection the lifecycle
	// page renders from (sessionspans.go): the paged list (?include=structure
	// for the envelope-only dashboard pages) plus the single full span the
	// inspector modal lazy-fetches.
	mux.Handle("GET /api/v1/sessions/{id}/spans", gated(s.handleSessionSpans))
	mux.Handle("GET /api/v1/sessions/{id}/spans/{cid}", gated(s.handleSessionSpan))
	// Tool-Call Index audit surface — searchable per-call list, single call by
	// id, and the distinct tool-name facet. The literal /facets route is more
	// specific than /{id}, so ServeMux routes it first regardless of order.
	mux.Handle("GET /api/v1/tool-calls", gated(s.handleToolCalls))
	mux.Handle("GET /api/v1/tool-calls/facets", gated(s.handleToolNames))
	mux.Handle("GET /api/v1/tool-calls/{id}", gated(s.handleToolCall))
	// Advise audit surface — every routing judgement (payload + verdict), and
	// the savings attribution for down-ranked traffic (advise_audit.go).
	mux.Handle("GET /api/v1/advise/audit", gated(s.handleAdviseAudit))
	mux.Handle("GET /api/v1/advise/audit/savings", gated(s.handleAdviseSavings))
}

// filterFromQuery reads the shared equality/status filters from the query
// string. The message browser adds exact session/correlation lookups and a
// repeated ?tags= multi-value param (AND containment). The four categorical
// dimensions (configuration/model/provider/protocol) are also repeatable —
// many values OR together — parsed into the plural EventFilter slices; the
// scalar twins keep the first value for the dashboard breakdown path, which
// stays single-valued.
func filterFromQuery(r *http.Request) store.EventFilter {
	q := r.URL.Query()
	return store.EventFilter{
		Configuration:        q.Get("configuration"),
		Gateway:              q.Get("gateway"),
		Model:                q.Get("model"),
		Provider:             q.Get("provider"),
		Protocol:             q.Get("protocol"),
		Configurations:       nonEmpty(q["configuration"]),
		Models:               nonEmpty(q["model"]),
		Providers:            nonEmpty(q["provider"]),
		Protocols:            nonEmpty(q["protocol"]),
		StatusClass:          q.Get("status_class"),
		SessionID:            q.Get("session_id"),
		CorrelationID:        q.Get("correlation_id"),
		ConversationID:       q.Get("conversation_id"),
		ParentConversationID: q.Get("parent_conversation_id"),
		AgentID:              q.Get("agent_id"),
		UserID:               q.Get("user_id"),
		Tags:                 nonEmpty(q["tags"]),
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

// sortFromQuery reads the optional ?sort column key and ?order direction. The
// direction defaults to descending; order=asc flips it. The sort key is passed
// through verbatim — the store allowlists it and degrades an unknown key to its
// default ordering, so a stale/typo'd key never errors.
func sortFromQuery(r *http.Request) (sort string, asc bool) {
	q := r.URL.Query()
	return q.Get("sort"), q.Get("order") == "asc"
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
	sort, asc := sortFromQuery(r)
	events, next, err := s.queries.ListEventsFiltered(r.Context(), store.EventListParams{
		From:   from,
		To:     to,
		Filter: filterFromQuery(r),
		Cursor: q.Get("cursor"),
		Limit:  limitParam(r, 0),
		Sort:   sort,
		Asc:    asc,
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
	rec, err := s.recordFor(r.Context(), id)
	if err != nil {
		s.queryError(w, "get record", err)
		return
	}
	writeJSON(w, http.StatusOK, stitch.BuildRequestView(event, rec))
}

func (s *Server) handleEventBody(w http.ResponseWriter, r *http.Request) {
	rec, err := s.recordFor(r.Context(), r.PathValue("id"))
	if err != nil {
		s.queryError(w, "get record", err)
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "no record")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleSessions serves the session-discovery list: a keyset page of session
// summaries within an optional [from, to) window, narrowed by the shared
// equality/tag filters (filterFromQuery — only configuration + tags are
// surfaced in the console UI, but any dimension works). A session is included
// when its matching-row span overlaps the window.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	from, to, bad := parseWindowBounds(r)
	if bad != "" {
		writeError(w, http.StatusBadRequest, "invalid "+bad)
		return
	}
	sort, asc := sortFromQuery(r)
	filter := filterFromQuery(r)
	sessions, next, err := s.queries.ListSessions(r.Context(), store.SessionListParams{
		From:   from,
		To:     to,
		Filter: filter,
		Cursor: r.URL.Query().Get("cursor"),
		Limit:  limitParam(r, 0),
		Sort:   sort,
		Asc:    asc,
	})
	if err != nil {
		if errors.Is(err, store.ErrInvalidCursor) {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		s.queryError(w, "list sessions", err)
		return
	}
	// total is the full matching count (filter + window, page-independent) so
	// the pager can show "X–Y of N".
	total, err := s.queries.CountSessions(r.Context(), store.SessionCountParams{From: from, To: to, Filter: filter})
	if err != nil {
		s.queryError(w, "count sessions", err)
		return
	}
	out := mapSessionList(sessions, next)
	out.Total = total
	writeJSON(w, http.StatusOK, out)
}

// handleSession serves the session rollup. It reads the narrow rollup
// projection — the gen_ai content never leaves Postgres for this view — so a
// large session costs O(rows x stripped-blob), not O(rows x full-blob).
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	events, err := s.queries.EventsBySessionRollup(r.Context(), id)
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

// limitParam reads the ?limit page-size query param, returning def when absent
// or invalid. The list endpoints are the only paged surfaces, so limit is the
// only integer query param the API takes.
func limitParam(r *http.Request, def int) int {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
