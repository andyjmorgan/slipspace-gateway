package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidCursor is returned when a pagination cursor cannot be decoded — a
// truncated, tampered, or hand-typed cursor, never a value this package minted.
var ErrInvalidCursor = errors.New("store: invalid cursor")

// EventFilter is the set of equality + status-class predicates the dashboard
// and the message browser share. A zero field means "no filter on this
// dimension". StatusClass is one of "2xx"/"4xx"/"5xx" (empty = any).
type EventFilter struct {
	Configuration string
	Gateway       string
	Model         string
	Provider      string
	Protocol      string
	StatusClass   string
	// SessionID and CorrelationID are exact-match lookups for the message
	// browser's two id search boxes; empty means no predicate.
	SessionID     string
	CorrelationID string
	// ConversationID and ParentConversationID are exact-match lookups for
	// drilling into a specific subagent thread or the children of a parent;
	// empty means no predicate.
	ConversationID       string
	ParentConversationID string
	// AgentID is an exact-match lookup for the message browser's agent search
	// box; empty means no predicate.
	AgentID string
	// UserID is an exact-match lookup for the message browser's user search
	// box; empty means no predicate.
	UserID string
	// Tags narrows to events whose post-rule tag set contains ALL listed tags
	// (JSONB containment). Empty/nil means no predicate.
	Tags []string
}

// appendFilter grows the WHERE fragments + args for the equality and
// status-class predicates, binding every user value as a parameter ($N). It
// never interpolates a user string into SQL — only column names, which come
// from the package-internal allowlist.
func appendFilter(where []string, args []any, f EventFilter) ([]string, []any) {
	eq := []struct{ col, val string }{
		{"configuration", f.Configuration},
		{"model", f.Model},
		{"provider", f.Provider},
		{"protocol", f.Protocol},
		{"session_id", f.SessionID},
		{"correlation_id", f.CorrelationID},
		{"conversation_id", f.ConversationID},
		{"parent_conversation_id", f.ParentConversationID},
		{"agent_id", f.AgentID},
		{"user_id", f.UserID},
	}
	for _, e := range eq {
		if e.val == "" {
			continue
		}
		args = append(args, e.val)
		where = append(where, fmt.Sprintf("%s = $%d", e.col, len(args)))
	}
	// gateway_id has no promoted column (single-writer design lifts it from the
	// resource attribute into the blob); filter via the JSONB path. Rare enough
	// that an index isn't warranted yet — promote on demand.
	if f.Gateway != "" {
		args = append(args, f.Gateway)
		where = append(where, fmt.Sprintf("span_event->>'gateway_id' = $%d", len(args)))
	}
	// Tags is an AND containment: the event's span_event->'tags' array must
	// contain every requested tag. We bind the requested set as a single jsonb
	// array param so the @> operator can use the GIN index on
	// (span_event->'tags').
	if len(f.Tags) > 0 {
		tagsJSON, _ := json.Marshal(f.Tags)
		args = append(args, string(tagsJSON))
		where = append(where, fmt.Sprintf("span_event->'tags' @> $%d::jsonb", len(args)))
	}
	if lo, hi, ok := statusClassBounds(f.StatusClass); ok {
		args = append(args, lo)
		loIdx := len(args)
		if hi == 0 {
			where = append(where, fmt.Sprintf("status_code >= $%d", loIdx))
		} else {
			args = append(args, hi)
			where = append(where, fmt.Sprintf("status_code BETWEEN $%d AND $%d", loIdx, len(args)))
		}
	}
	return where, args
}

// statusClassBounds maps a status class to its inclusive [lo, hi] bounds; hi==0
// means "lo and up" (the 5xx open upper bound). ok is false for an empty or
// unrecognized class.
func statusClassBounds(class string) (lo, hi int, ok bool) {
	switch class {
	case "2xx":
		return 200, 299, true
	case "4xx":
		return 400, 499, true
	case "5xx":
		return 500, 0, true
	default:
		return 0, 0, false
	}
}

func rate(num, den int64) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// EventListParams is the input to ListEventsFiltered: an optional time window,
// the shared filters, an opaque keyset Cursor, and a Limit. A zero From/To omits
// that bound; Limit <= 0 defaults to 100 and is capped at 500.
type EventListParams struct {
	From   time.Time
	To     time.Time
	Filter EventFilter
	Cursor string
	Limit  int
}

// eventCursor is the keyset position encoded into next_cursor: the last row's
// (observed_at, correlation_id), the tuple the DESC ordering seeks past.
type eventCursor struct {
	ObservedAt  time.Time `json:"o"`
	Correlation string    `json:"c"`
}

func encodeCursor(c eventCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (eventCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return eventCursor{}, ErrInvalidCursor
	}
	var c eventCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return eventCursor{}, ErrInvalidCursor
	}
	return c, nil
}

const (
	eventListPageDefault = 100
	eventListPageMax     = 500
)

// ListEventsFiltered returns a page of request events ordered by
// (observed_at DESC, correlation_id DESC), narrowed by an optional time window
// and the shared filters. Pagination is keyset: it fetches Limit+1 rows to learn
// whether a further page exists; when it does, nextCursor encodes the last
// returned row's position. An empty nextCursor means the last page. A malformed
// Cursor is ErrInvalidCursor.
func (s *Store) ListEventsFiltered(ctx context.Context, p EventListParams) ([]RequestEvent, string, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = eventListPageDefault
	}
	if limit > eventListPageMax {
		limit = eventListPageMax
	}

	var where []string
	var args []any
	if !p.From.IsZero() {
		args = append(args, p.From)
		where = append(where, fmt.Sprintf("observed_at >= $%d", len(args)))
	}
	if !p.To.IsZero() {
		args = append(args, p.To)
		where = append(where, fmt.Sprintf("observed_at < $%d", len(args)))
	}
	where, args = appendFilter(where, args, p.Filter)

	if p.Cursor != "" {
		cur, err := decodeCursor(p.Cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, cur.ObservedAt)
		oIdx := len(args)
		args = append(args, cur.Correlation)
		where = append(where, fmt.Sprintf("(observed_at, correlation_id) < ($%d, $%d)", oIdx, len(args)))
	}

	q := `SELECT ` + requestEventColumns + ` FROM request_events`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	args = append(args, limit+1)
	q += fmt.Sprintf(` ORDER BY observed_at DESC, correlation_id DESC LIMIT $%d`, len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("store: list events filtered: %w", err)
	}
	defer rows.Close()

	var out []RequestEvent
	for rows.Next() {
		e, serr := scanRequestEvent(rows)
		if serr != nil {
			return nil, "", serr
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	next := ""
	if len(out) > limit {
		last := out[limit-1]
		out = out[:limit]
		next = encodeCursor(eventCursor{ObservedAt: last.ObservedAt, Correlation: last.CorrelationID})
	}
	return out, next, nil
}

// SessionSummary is one row of the session list: a session's id plus the
// rollup the discovery table renders. The aggregates cover only the rows that
// match SessionListParams.Filter (the predicates are applied before the
// GROUP BY), so a configuration/tag filter narrows both membership and the
// displayed counts to the matching subset of the session.
type SessionSummary struct {
	// SessionID is the conversation/bundle id the requests share.
	SessionID string
	// Messages is the number of (matching) requests in the session.
	Messages int
	// Subagents is the number of distinct CHILD conversation threads in the
	// session — distinct conversation_id values that differ from the session
	// root. 0 for a plain single-agent session; N when N subagents spawned.
	// Rows predating the conversation columns (empty conversation_id) never
	// count, so old sessions read 0.
	Subagents int
	// TotalTokens is the summed tokens_in+tokens_out across those requests.
	TotalTokens int64
	// Models is the distinct requested model names, empty strings excluded.
	Models []string
	// Started / LastAt are the first and last observed_at across the matching
	// rows — the session's real bounds, which may fall outside the query window.
	Started time.Time
	LastAt  time.Time
}

// SessionListParams is the input to ListSessions: an optional time window (the
// session's span must overlap it), the shared row Filter applied before
// aggregation, an opaque keyset Cursor, and a Limit. A zero From/To omits that
// bound; Limit <= 0 defaults to eventListPageDefault and is capped at
// eventListPageMax.
type SessionListParams struct {
	From   time.Time
	To     time.Time
	Filter EventFilter
	Cursor string
	Limit  int
}

// sessionCursor is the keyset position encoded into a session-list next_cursor:
// the last row's (last_at, session_id), the tuple the DESC ordering seeks past.
type sessionCursor struct {
	LastAt    time.Time `json:"l"`
	SessionID string    `json:"s"`
}

func encodeSessionCursor(c sessionCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeSessionCursor(s string) (sessionCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return sessionCursor{}, ErrInvalidCursor
	}
	var c sessionCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return sessionCursor{}, ErrInvalidCursor
	}
	return c, nil
}

// ListSessions returns a page of session summaries ordered by
// (last_at DESC, session_id DESC), narrowed to sessions whose span overlaps the
// optional [From, To) window. The Filter predicates are applied to the rows in
// the aggregating CTE — so a session is included when it has matching requests
// overlapping the window and the rollup reflects only those rows. Pagination is
// keyset: it fetches Limit+1 aggregated rows to learn whether a further page
// exists; nextCursor encodes the last returned row's position, empty on the
// last page. A malformed Cursor is ErrInvalidCursor.
//
// Like Facets, the CTE is a whole-table grouped scan; the result is bounded by
// the page Limit but the aggregate spans every session, so this is a heavier
// query than the keyset event list — callers default it to a recent window.
func (s *Store) ListSessions(ctx context.Context, p SessionListParams) ([]SessionSummary, string, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = eventListPageDefault
	}
	if limit > eventListPageMax {
		limit = eventListPageMax
	}

	// Filter predicates bind first ($1..$k) and live in the CTE WHERE; the
	// window + keyset predicates bind after and live in the outer WHERE, reading
	// the aggregated started/last_at. The args slice is built in the same order
	// the $N placeholders appear in the SQL text.
	inner := []string{"session_id <> ''"}
	inner, args := appendFilter(inner, nil, p.Filter)

	var outer []string
	if !p.From.IsZero() {
		args = append(args, p.From)
		outer = append(outer, fmt.Sprintf("last_at >= $%d", len(args)))
	}
	if !p.To.IsZero() {
		args = append(args, p.To)
		outer = append(outer, fmt.Sprintf("started < $%d", len(args)))
	}
	if p.Cursor != "" {
		cur, err := decodeSessionCursor(p.Cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, cur.LastAt)
		lIdx := len(args)
		args = append(args, cur.SessionID)
		outer = append(outer, fmt.Sprintf("(last_at, session_id) < ($%d, $%d)", lIdx, len(args)))
	}

	// total_tokens sums the promoted tokens_in/tokens_out columns (#318) — NOT
	// span_event. The pre-#318 form summed the usage out of the JSONB blob, which
	// detoasted every ~95 kB span per row (~3.8 s); reading the columns keeps the
	// whole aggregate off the blob so it never detoasts.
	q := `WITH agg AS (
  SELECT session_id,
         COUNT(*) AS messages,
         COUNT(DISTINCT conversation_id) FILTER (
           WHERE conversation_id <> '' AND conversation_id <> session_id
         ) AS subagents,
         COALESCE(SUM(tokens_in + tokens_out), 0) AS total_tokens,
         MIN(observed_at) AS started,
         MAX(observed_at) AS last_at,
         COALESCE(array_agg(DISTINCT model) FILTER (WHERE model <> ''), '{}') AS models
  FROM request_events
  WHERE ` + strings.Join(inner, " AND ") + `
  GROUP BY session_id
)
SELECT session_id, messages, subagents, total_tokens, started, last_at, models FROM agg`
	if len(outer) > 0 {
		q += ` WHERE ` + strings.Join(outer, " AND ")
	}
	args = append(args, limit+1)
	q += fmt.Sprintf(` ORDER BY last_at DESC, session_id DESC LIMIT $%d`, len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("store: list sessions: %w", err)
	}
	defer rows.Close()

	var out []SessionSummary
	for rows.Next() {
		var sum SessionSummary
		if serr := rows.Scan(&sum.SessionID, &sum.Messages, &sum.Subagents, &sum.TotalTokens, &sum.Started, &sum.LastAt, &sum.Models); serr != nil {
			return nil, "", fmt.Errorf("store: scan session summary: %w", serr)
		}
		out = append(out, sum)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	next := ""
	if len(out) > limit {
		last := out[limit-1]
		out = out[:limit]
		next = encodeSessionCursor(sessionCursor{LastAt: last.LastAt, SessionID: last.SessionID})
	}
	return out, next, nil
}

// sessionRollupColumns is the narrow projection the session rollup reads: the
// scalar columns plus span_event with the gen_ai_content sub-object REMOVED.
// The rollup (SessionView / MessageEntry) needs the span's small attributes
// (tags, rule chain, attempts, sources, usage) but never the captured content
// — which is the only unbounded key on the blob (~hundreds of KB per row when
// ingest caps are off). Stripping it in SQL keeps a whole-session scan to the
// few-KB residue per row instead of materializing every full blob in memory
// (the 2026-06-10 prod OOM: 688 rows x ~410 KB ≈ 280 MB per request).
const sessionRollupColumns = `correlation_id, observed_at, session_id, conversation_id, parent_conversation_id, agent_id, user_id, provider, model, configuration, protocol, status_code, tokens_in, tokens_out, span_event - 'gen_ai_content' AS span_event`

// Session-scoped read bounds. sessionScanBatch is the internal keyset batch
// the rollup pages the table with; sessionPageDefault / sessionPageMax bound
// the wire page of EventsBySessionPage (the /sessions/{id}/spans feed, whose
// rows carry the full blob and so get a smaller cap than the event list).
const (
	sessionScanBatch   = 200
	sessionPageDefault = 200
	sessionPageMax     = 500
)

// EventsBySessionRollup returns every event in a session, oldest first, in the
// narrow rollup projection (span_event stripped of gen_ai_content) so the
// session view can render the conversation in order without ever
// materializing the full blobs. The scan is internally keyset-batched
// (sessionScanBatch rows per query) and the composite
// (observed_at, correlation_id) keeps it deterministic.
func (s *Store) EventsBySessionRollup(ctx context.Context, sessionID string) ([]RequestEvent, error) {
	if sessionID == "" {
		return nil, nil
	}
	var out []RequestEvent
	var after *eventCursor
	for {
		page, err := s.sessionEventsAsc(ctx, sessionRollupColumns, sessionID, after, sessionScanBatch)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < sessionScanBatch {
			return out, nil
		}
		last := page[len(page)-1]
		after = &eventCursor{ObservedAt: last.ObservedAt, Correlation: last.CorrelationID}
	}
}

// sessionEventsAsc runs one ascending keyset batch over a session's events:
// rows after the cursor position (exclusive), ordered by
// (observed_at, correlation_id), capped at limit.
func (s *Store) sessionEventsAsc(ctx context.Context, columns, sessionID string, after *eventCursor, limit int) ([]RequestEvent, error) {
	args := []any{sessionID}
	q := `SELECT ` + columns + ` FROM request_events WHERE session_id=$1`
	if after != nil {
		args = append(args, after.ObservedAt)
		oIdx := len(args)
		args = append(args, after.Correlation)
		q += fmt.Sprintf(` AND (observed_at, correlation_id) > ($%d, $%d)`, oIdx, len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(` ORDER BY observed_at, correlation_id LIMIT $%d`, len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: events by session: %w", err)
	}
	defer rows.Close()

	var out []RequestEvent
	for rows.Next() {
		e, serr := scanRequestEvent(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SessionPageParams is the input to EventsBySessionPage: the session, an
// opaque keyset Cursor from a prior call (empty = first page), and a Limit
// (<= 0 defaults to sessionPageDefault, capped at sessionPageMax).
type SessionPageParams struct {
	SessionID string
	Cursor    string
	Limit     int
}

// EventsBySessionPage streams one keyset page of a session's events — full
// projection including the span_event blob — oldest first, invoking fn once
// per row. The callback shape is deliberate: each event (and its potentially
// huge blob) is only live for the duration of its fn call, so a caller that
// projects rows down to a bounded DTO holds O(1 full blobs) at a time, never
// O(page). It fetches Limit+1 rows to learn whether a further page exists;
// when it does, next encodes the last delivered row's position (same cursor
// encoding as the event list). An empty next means the last page; a malformed
// Cursor is ErrInvalidCursor; an fn error aborts the scan and is returned.
func (s *Store) EventsBySessionPage(ctx context.Context, p SessionPageParams, fn func(RequestEvent) error) (string, error) {
	if p.SessionID == "" {
		return "", nil
	}
	limit := p.Limit
	if limit <= 0 {
		limit = sessionPageDefault
	}
	if limit > sessionPageMax {
		limit = sessionPageMax
	}

	args := []any{p.SessionID}
	q := `SELECT ` + requestEventColumns + ` FROM request_events WHERE session_id=$1`
	if p.Cursor != "" {
		cur, err := decodeCursor(p.Cursor)
		if err != nil {
			return "", err
		}
		args = append(args, cur.ObservedAt)
		oIdx := len(args)
		args = append(args, cur.Correlation)
		q += fmt.Sprintf(` AND (observed_at, correlation_id) > ($%d, $%d)`, oIdx, len(args))
	}
	args = append(args, limit+1)
	q += fmt.Sprintf(` ORDER BY observed_at, correlation_id LIMIT $%d`, len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return "", fmt.Errorf("store: session events page: %w", err)
	}
	defer rows.Close()

	n := 0
	var last eventCursor
	more := false
	for rows.Next() {
		if n == limit {
			// The +1 sentinel row: a further page exists. Never scanned or
			// delivered — it belongs to the next page.
			more = true
			break
		}
		e, serr := scanRequestEvent(rows)
		if serr != nil {
			return "", serr
		}
		if err := fn(e); err != nil {
			return "", err
		}
		last = eventCursor{ObservedAt: e.ObservedAt, Correlation: e.CorrelationID}
		n++
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if more {
		return encodeCursor(last), nil
	}
	return "", nil
}
