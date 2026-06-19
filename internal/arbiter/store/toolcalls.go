package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrToolCallNotFound is returned when no tool_call row matches an id.
var ErrToolCallNotFound = errors.New("store: tool call not found")

// ToolObservationKind discriminates the two halves of a tool invocation that
// land on different spans: the CALL (name + arguments, from an assistant
// response) and the RESPONSE (result, from the next request). Each is upserted
// independently, joined on the tool_call_id; whichever arrives first creates the
// row and the other fills in its half (never clobbering the present half).
type ToolObservationKind int

const (
	// ToolCallHalf carries the proposing side: tool_name, arguments, and the
	// call span's facts (provider/configuration/protocol/model).
	ToolCallHalf ToolObservationKind = iota
	// ToolResultHalf carries the returning side: the result text.
	ToolResultHalf
)

// ToolCallObservation is one half of a tool invocation extracted from a span,
// the unit ingest hands to UpsertToolCalls. Only the fields relevant to Kind are
// populated; the rest stay zero. ObservedAt is the source span's start time —
// the called_at / responded_at of the respective half, and (on first insert) the
// row's stable keyset axis.
type ToolCallObservation struct {
	// Kind selects which half this observation fills.
	Kind ToolObservationKind
	// ToolCallID is the provider-assigned id (toolu_… / call_… / Gemini id) the
	// two halves join on. Always non-empty (ingest skips id-less parts).
	ToolCallID string
	// SessionID is the source span's session bundle root — the same on both
	// halves (the call and its result share a session).
	SessionID string
	// CorrelationID is the source span: the call span for ToolCallHalf, the
	// response span for ToolResultHalf.
	CorrelationID string
	// ObservedAt is the source span's start time.
	ObservedAt time.Time
	// Executor marks who runs the tool ("server"/"client"); present on either
	// half, empty when unknown.
	Executor string

	// Call-half fields (Kind == ToolCallHalf).

	// ToolName is the function name the model called — the audit search target.
	ToolName string
	// Arguments is the raw JSON argument object, or nil when the call had none.
	Arguments []byte
	// ArgumentsChars is the true (uncapped) rune size of Arguments.
	ArgumentsChars int
	// Provider / Configuration / Protocol / Model are the call span's routing
	// facts, copied so the audit list can filter without joining request_events.
	Provider      string
	Configuration string
	Protocol      string
	Model         string

	// Response-half fields (Kind == ToolResultHalf).

	// Result is the tool output text, capped by ingest; ResultChars is the true
	// size.
	Result      string
	ResultChars int
}

// ToolCall is one tool-call row: a single invocation joined across its call and
// response spans. CalledAt / RespondedAt are nil until the respective half has
// been ingested; a nil RespondedAt is the "pending" state.
type ToolCall struct {
	ToolCallID            string
	ToolName              string
	SessionID             string
	Executor              string
	Provider              string
	Configuration         string
	Protocol              string
	Model                 string
	Arguments             []byte
	ArgumentsChars        int
	Result                string
	ResultChars           int
	CallCorrelationID     string
	ResponseCorrelationID string
	CalledAt              *time.Time
	RespondedAt           *time.Time
	ObservedAt            time.Time
}

// Resolved reports whether the response half has been ingested — the row's
// "pending" (false) vs "resolved" (true) status.
func (t ToolCall) Resolved() bool { return t.RespondedAt != nil }

// insertToolCallSQL upserts the CALL half. observed_at is set to the call time
// on first insert (COALESCE to now() only when the span carried no start) and
// never updated on conflict, so it stays the stable first-seen keyset axis. The
// session_id / executor updates keep an already-present value when this half
// carries none, so a response half that landed first is not blanked.
const insertToolCallSQL = `
INSERT INTO tool_call (tool_call_id, tool_name, session_id, executor, provider, configuration, protocol, model, arguments, arguments_chars, call_correlation_id, called_at, observed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, COALESCE($12, now()))
ON CONFLICT (tool_call_id) DO UPDATE SET
  tool_name           = EXCLUDED.tool_name,
  session_id          = CASE WHEN EXCLUDED.session_id <> '' THEN EXCLUDED.session_id ELSE tool_call.session_id END,
  executor            = CASE WHEN EXCLUDED.executor   <> '' THEN EXCLUDED.executor   ELSE tool_call.executor   END,
  provider            = EXCLUDED.provider,
  configuration       = EXCLUDED.configuration,
  protocol            = EXCLUDED.protocol,
  model               = EXCLUDED.model,
  arguments           = EXCLUDED.arguments,
  arguments_chars     = EXCLUDED.arguments_chars,
  call_correlation_id = EXCLUDED.call_correlation_id,
  called_at           = EXCLUDED.called_at`

// insertToolResultSQL upserts the RESPONSE half. It touches only the response
// columns; tool_name / arguments / call_correlation_id are left to the call half
// (which may arrive later, creating a stub here meanwhile). observed_at is the
// response time only when this half inserts the row first, and is never updated
// on conflict.
const insertToolResultSQL = `
INSERT INTO tool_call (tool_call_id, session_id, executor, result, result_chars, response_correlation_id, responded_at, observed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($7, now()))
ON CONFLICT (tool_call_id) DO UPDATE SET
  session_id              = CASE WHEN tool_call.session_id <> '' THEN tool_call.session_id ELSE EXCLUDED.session_id END,
  executor                = CASE WHEN tool_call.executor   <> '' THEN tool_call.executor   ELSE EXCLUDED.executor   END,
  result                  = EXCLUDED.result,
  result_chars            = EXCLUDED.result_chars,
  response_correlation_id = EXCLUDED.response_correlation_id,
  responded_at            = EXCLUDED.responded_at`

// UpsertToolCalls writes a span's tool-call observations in ONE transaction,
// dispatching each by Kind to the call-half or response-half upsert. The upserts
// are idempotent and half-merging (each touches only its own columns), so a
// re-ingested span is a no-op and the two halves of an invocation converge in
// either order. An empty slice is a no-op (no tx).
func (s *Store) UpsertToolCalls(ctx context.Context, obs []ToolCallObservation) error {
	if len(obs) == 0 {
		return nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin tool calls: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, o := range obs {
		var execErr error
		switch o.Kind {
		case ToolResultHalf:
			_, execErr = tx.Exec(ctx, insertToolResultSQL,
				o.ToolCallID, o.SessionID, o.Executor, o.Result, o.ResultChars,
				o.CorrelationID, nullTime(o.ObservedAt))
		default: // ToolCallHalf
			_, execErr = tx.Exec(ctx, insertToolCallSQL,
				o.ToolCallID, o.ToolName, o.SessionID, o.Executor, o.Provider, o.Configuration,
				o.Protocol, o.Model, nullJSON(o.Arguments), o.ArgumentsChars,
				o.CorrelationID, nullTime(o.ObservedAt))
		}
		if execErr != nil {
			return fmt.Errorf("store: upsert tool call: %w", execErr)
		}
	}
	return commit(ctx, tx)
}

// toolCallColumns is the projection every tool_call read uses, in scan order.
const toolCallColumns = `tool_call_id, tool_name, session_id, executor, provider, configuration, protocol, model, arguments, arguments_chars, result, result_chars, call_correlation_id, response_correlation_id, called_at, responded_at, observed_at`

// scanToolCall scans one tool_call row in toolCallColumns order. Nullable
// timestamps land in *time.Time (nil on SQL NULL); a null arguments JSONB scans
// to a nil byte slice.
func scanToolCall(sc rowScanner) (ToolCall, error) {
	var t ToolCall
	if err := sc.Scan(&t.ToolCallID, &t.ToolName, &t.SessionID, &t.Executor, &t.Provider,
		&t.Configuration, &t.Protocol, &t.Model, &t.Arguments, &t.ArgumentsChars,
		&t.Result, &t.ResultChars, &t.CallCorrelationID, &t.ResponseCorrelationID,
		&t.CalledAt, &t.RespondedAt, &t.ObservedAt); err != nil {
		return ToolCall{}, fmt.Errorf("store: scan tool call: %w", err)
	}
	return t, nil
}

// ToolCallListParams is the input to ListToolCalls: an optional time window (on
// observed_at), the audit filters, an opaque keyset Cursor, and a Limit (<= 0
// defaults to 100, capped at 500). A zero field means no predicate on that
// dimension.
type ToolCallListParams struct {
	From          time.Time
	To            time.Time
	Name          string
	SessionID     string
	Provider      string
	Configuration string
	Protocol      string
	Model         string
	// Status narrows to "pending" (no result yet) or "resolved" (result present);
	// empty means either.
	Status string
	Cursor string
	Limit  int
}

// toolCallCursor is the keyset position: the last row's (observed_at,
// tool_call_id), the tuple the (observed_at DESC, tool_call_id DESC) ordering
// seeks strictly past.
type toolCallCursor struct {
	ObservedAt time.Time `json:"o"`
	ID         string    `json:"i"`
}

func encodeToolCallCursor(c toolCallCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeToolCallCursor(s string) (toolCallCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return toolCallCursor{}, ErrInvalidCursor
	}
	var c toolCallCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return toolCallCursor{}, ErrInvalidCursor
	}
	return c, nil
}

// ListToolCalls returns a page of tool calls ordered by
// (observed_at DESC, tool_call_id DESC), narrowed by an optional time window and
// the audit filters. Pagination is keyset: it fetches Limit+1 rows to learn
// whether a further page exists; nextCursor encodes the last returned row's
// position, empty on the last page. A malformed Cursor is ErrInvalidCursor.
func (s *Store) ListToolCalls(ctx context.Context, p ToolCallListParams) ([]ToolCall, string, error) {
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
	// Equality filters: column names come from this package-internal allowlist,
	// only values are bound as parameters.
	eq := []struct{ col, val string }{
		{"tool_name", p.Name},
		{"session_id", p.SessionID},
		{"provider", p.Provider},
		{"configuration", p.Configuration},
		{"protocol", p.Protocol},
		{"model", p.Model},
	}
	for _, e := range eq {
		if e.val == "" {
			continue
		}
		args = append(args, e.val)
		where = append(where, fmt.Sprintf("%s = $%d", e.col, len(args)))
	}
	switch p.Status {
	case "pending":
		where = append(where, "responded_at IS NULL")
	case "resolved":
		where = append(where, "responded_at IS NOT NULL")
	}

	if p.Cursor != "" {
		cur, err := decodeToolCallCursor(p.Cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, cur.ObservedAt)
		oIdx := len(args)
		args = append(args, cur.ID)
		where = append(where, fmt.Sprintf("(observed_at, tool_call_id) < ($%d, $%d)", oIdx, len(args)))
	}

	q := `SELECT ` + toolCallColumns + ` FROM tool_call`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	args = append(args, limit+1)
	q += fmt.Sprintf(` ORDER BY observed_at DESC, tool_call_id DESC LIMIT $%d`, len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("store: list tool calls: %w", err)
	}
	defer rows.Close()

	var out []ToolCall
	for rows.Next() {
		t, serr := scanToolCall(rows)
		if serr != nil {
			return nil, "", serr
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	next := ""
	if len(out) > limit {
		last := out[limit-1]
		out = out[:limit]
		next = encodeToolCallCursor(toolCallCursor{ObservedAt: last.ObservedAt, ID: last.ToolCallID})
	}
	return out, next, nil
}

// GetToolCall returns one tool call by id, or ErrToolCallNotFound.
func (s *Store) GetToolCall(ctx context.Context, id string) (ToolCall, error) {
	row := s.db.QueryRow(ctx, `SELECT `+toolCallColumns+` FROM tool_call WHERE tool_call_id=$1`, id)
	t, err := scanToolCall(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ToolCall{}, ErrToolCallNotFound
	}
	return t, err
}

// ToolNames returns the distinct tool names seen, sorted ascending and excluding
// the empty string (a response half that arrived before its call). Backs the
// audit page's tool-name dropdown; index-only over tool_call_name_observed.
func (s *Store) ToolNames(ctx context.Context) ([]string, error) {
	names, err := s.distinctStrings(ctx,
		`SELECT DISTINCT tool_name FROM tool_call WHERE tool_name <> '' ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("store: tool names: %w", err)
	}
	return names, nil
}
