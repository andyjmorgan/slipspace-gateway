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
		{"gateway_id", f.Gateway},
		{"model", f.Model},
		{"provider", f.Provider},
		{"protocol", f.Protocol},
		{"session_id", f.SessionID},
		{"correlation_id", f.CorrelationID},
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
	// Tags is an AND containment: the event's detail->'tags' array must contain
	// every requested tag. We bind the requested set as a single jsonb array
	// param so the @> operator can use the GIN index on (detail->'tags').
	if len(f.Tags) > 0 {
		tagsJSON, _ := json.Marshal(f.Tags)
		args = append(args, string(tagsJSON))
		where = append(where, fmt.Sprintf("detail->'tags' @> $%d::jsonb", len(args)))
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

// EventsBySession returns every event in a session, oldest first, so the
// session view can render the conversation in order. The composite
// (observed_at, correlation_id) keeps it deterministic.
func (s *Store) EventsBySession(ctx context.Context, sessionID string) ([]RequestEvent, error) {
	if sessionID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT `+requestEventColumns+` FROM request_events WHERE session_id=$1 ORDER BY observed_at, correlation_id`, sessionID)
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
