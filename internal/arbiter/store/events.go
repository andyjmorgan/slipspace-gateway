package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrRequestEventNotFound is returned when no event matches a correlation id.
var ErrRequestEventNotFound = errors.New("store: request event not found")

// RequestEvent is one request's row in the single-writer entity. The OTel span
// feed is the sole writer (keyed correlation_id). The scalar fields are a
// materialized index PROJECTED from SpanEvent at ingest — the immutable blob is
// the source of truth, the columns are backfillable from it. List/filter/facet
// queries read the columns and never SELECT span_event; drill-down selects the
// blob and decodes it into SpanFields.
type RequestEvent struct {
	// CorrelationID is the per-request primary key.
	CorrelationID string
	// ObservedAt is the gateway request-start time (the span START time), NOT
	// ingest now() — load-bearing for ordering, pagination, and retention.
	ObservedAt time.Time
	// SessionID is the resolved session bundle root — the stable top-down
	// grouping key; projected from the span's sluice.session_id (falling back
	// to gen_ai.conversation.id for spans predating that attribute).
	SessionID string
	// ConversationID is the resolved conversation/thread id (subagent thread
	// when active, else the session); projected from gen_ai.conversation.id.
	ConversationID string
	// ParentConversationID links a subagent thread back toward its session;
	// projected from sluice.parent_conversation_id. Empty for a main agent.
	ParentConversationID string
	// AgentID is the resolved id of a genuinely named agent; projected from
	// gen_ai.agent.id. A subagent thread is NOT an agent — it rides
	// ConversationID.
	AgentID string
	// UserID is the resolved end-user id on whose behalf the request was made;
	// projected from enduser.id.
	UserID string
	// Provider is the post-rule upstream provider; projected from
	// gen_ai.provider.name.
	Provider string
	// Model is the requested model; projected from gen_ai.request.model.
	Model string
	// Configuration is the resolved policy bundle name; projected from
	// sluice.configuration.
	Configuration string
	// Protocol is the post-rule wire protocol / endpoint; projected from
	// sluice.protocol.
	Protocol string
	// StatusCode is the client-facing HTTP status; projected from
	// http.response.status_code.
	StatusCode int
	// TokensIn / TokensOut are the usage counts projected from
	// gen_ai.usage.input_tokens / .output_tokens — the same values SpanFields
	// decodes from the blob, promoted to columns so the session-list aggregate
	// sums them without detoasting span_event (#318). Zero when the response
	// carried no usage block. The cached / cache-creation counts stay blob-only
	// (no aggregate reads them yet).
	TokensIn  int64
	TokensOut int64
	// Tags is the post-rule tag set (AddTagAction) projected from sluice.tags —
	// the same values SpanFields decodes from the blob, promoted to a GIN-indexed
	// text[] column so the facet enumeration, the AND-filter, and the session-list
	// rollup never detoast span_event (v12). Write-only on the entity: read paths
	// (MessageEntry) still decode tags from the blob, so scanRequestEvent does not
	// project this column. Nil/empty when no rule tagged the request.
	Tags []string
	// SpanEvent is the COMPLETE span as received — every attribute plus the
	// gen_ai content — stored verbatim and never stripped. The columns above are
	// projections of it. nil only on a freshly built event before ingest sets it.
	SpanEvent []byte
}

// SpanFields is the typed view of the values the console reads out of the
// span_event blob that are NOT promoted to a filter column: the source headers
// for the identity ids, the inbound method, the outcome measurements, the
// streaming flag, the post-rule tags + fired-rule lifecycle, the resilience
// attempts, and the bounded gen_ai content. Decoded lazily by SpanEvent.Decode
// when a drill-down (message entry, session rollup, inspector) needs them; the
// list/filter/facet paths never touch it.
//
// The JSON keys mirror the OTel/Record attribute names the gateway emits onto
// the span, so the blob round-trips the wire shape (invariant: nothing on the
// span is discarded). Numeric usage is read leniently (int or numeric string).
type SpanFields struct {
	SessionIDSource      string           `json:"sluice.session_id_source,omitempty"`
	ConversationIDSource string           `json:"sluice.conversation_id_source,omitempty"`
	AgentIDSource        string           `json:"sluice.agent_id_source,omitempty"`
	UserIDSource         string           `json:"sluice.user_id_source,omitempty"`
	GatewayID            string           `json:"gateway_id,omitempty"`
	Method               string           `json:"slipspace.method,omitempty"`
	APIKeyName           string           `json:"slipspace.api_key_name,omitempty"`
	PolicyRef            string           `json:"sluice.policy_ref,omitempty"`
	UpstreamStatus       int              `json:"slipspace.upstream_status,omitempty"`
	LatencyMs            int64            `json:"slipspace.latency_ms,omitempty"`
	TokensIn             int64            `json:"gen_ai.usage.input_tokens,omitempty"`
	TokensOut            int64            `json:"gen_ai.usage.output_tokens,omitempty"`
	TokensCached         int64            `json:"gen_ai.usage.cache_read.input_tokens,omitempty"`
	TokensCacheCreation  int64            `json:"gen_ai.usage.cache_creation.input_tokens,omitempty"`
	Streaming            bool             `json:"gen_ai.request.stream,omitempty"`
	Tags                 []string         `json:"tags,omitempty"`
	RulesFired           []string         `json:"rules_fired,omitempty"`
	RuleChain            []RuleChainEntry `json:"rule_chain,omitempty"`
	Attempts             []AttemptDetail  `json:"attempts,omitempty"`
	AssemblyPartial      bool             `json:"assembly_partial,omitempty"`
	// GenAIContent is the bounded gen_ai.* content object ({input_messages,
	// output_messages, tool_definitions, system_instructions}), nil when none
	// was captured.
	GenAIContent json.RawMessage `json:"gen_ai_content,omitempty"`
}

// DecodeSpanFields decodes the blob's drill-down view. A nil/empty blob or a
// decode error yields a zero SpanFields — the inspector then simply renders the
// scalar columns with empty content rather than failing the request.
func (e RequestEvent) DecodeSpanFields() SpanFields {
	if len(e.SpanEvent) == 0 {
		return SpanFields{}
	}
	var f SpanFields
	if json.Unmarshal(e.SpanEvent, &f) != nil {
		return SpanFields{}
	}
	return f
}

// RuleChainEntry is one rule's entry in SpanFields.RuleChain — the inspector's
// view of a fired rule. Mirrors contracts/connector.RuleFired.
type RuleChainEntry struct {
	Name           string   `json:"name"`
	ActionsApplied []string `json:"actions_applied,omitempty"`
	Terminated     bool     `json:"terminated,omitempty"`
	ErrorMessage   string   `json:"error_message,omitempty"`
}

// AttemptDetail is one resilience attempt in SpanFields.Attempts. Mirrors
// contracts/connector.Attempt.
type AttemptDetail struct {
	Target      string `json:"target"`
	StartedAtNs int64  `json:"started_at_ns,omitempty"`
	DurationMs  int64  `json:"duration_ms,omitempty"`
	StatusCode  int    `json:"status_code,omitempty"`
	Error       string `json:"error,omitempty"`
	Outcome     string `json:"outcome"`
}

// requestEventColumns is the projection every event read uses: the scalar
// filter columns plus the span_event blob, which the MessageEntry mapper
// decodes for the rich fields (tags, rule chain, tokens, latency, content) that
// no longer have dedicated columns. span_event is last so the scan order
// matches scanRequestEvent.
//
// The design's "list/filter/facet queries never SELECT span_event" rule binds
// the pure-aggregate scans (dashboard rollups, facet distinct-unnest) — those
// touch columns only. A list PAGE returns rendered rows whose contract
// (MessageEntry) carries blob-only fields, so it must read the blob; the cost
// is bounded by the page LIMIT, not the whole table.
const requestEventColumns = `correlation_id, observed_at, session_id, conversation_id, parent_conversation_id, agent_id, user_id, provider, model, configuration, protocol, status_code, tokens_in, tokens_out, span_event`

// insertEventSQL upserts the single-writer entity from the OTel span feed,
// keyed correlation_id. The whole span lands in span_event; the scalar columns
// are its projection. observed_at is the span start time (a zero time defaults
// to now() server-side only as a last resort). On conflict every column is
// overwritten from the latest span — there is one writer, so there is no
// cross-feed merge to preserve.
const insertEventSQL = `
INSERT INTO request_events (correlation_id, observed_at, session_id, conversation_id, parent_conversation_id, agent_id, user_id, provider, model, configuration, protocol, status_code, tokens_in, tokens_out, tags, span_event)
VALUES ($1, COALESCE($2, now()), $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (correlation_id) DO UPDATE SET
  observed_at            = COALESCE(EXCLUDED.observed_at, request_events.observed_at),
  session_id             = EXCLUDED.session_id,
  conversation_id        = EXCLUDED.conversation_id,
  parent_conversation_id = EXCLUDED.parent_conversation_id,
  agent_id               = EXCLUDED.agent_id,
  user_id                = EXCLUDED.user_id,
  provider               = EXCLUDED.provider,
  model                  = EXCLUDED.model,
  configuration          = EXCLUDED.configuration,
  protocol               = EXCLUDED.protocol,
  status_code            = EXCLUDED.status_code,
  tokens_in              = EXCLUDED.tokens_in,
  tokens_out             = EXCLUDED.tokens_out,
  tags                   = EXCLUDED.tags,
  span_event             = EXCLUDED.span_event`

// UpsertRequestEvent inserts or replaces a request event from the OTel span
// feed — the single writer of the entity. A zero ObservedAt defaults to now()
// server-side; a non-zero ObservedAt (the span start time) is honored verbatim.
func (s *Store) UpsertRequestEvent(ctx context.Context, e RequestEvent) error {
	span := e.SpanEvent
	if len(span) == 0 {
		span = []byte("{}")
	}
	// pgx encodes a nil slice as SQL NULL; the tags column is NOT NULL DEFAULT
	// '{}', so normalize an untagged request to an empty array.
	tags := e.Tags
	if tags == nil {
		tags = []string{}
	}
	_, err := s.db.Exec(ctx, insertEventSQL,
		e.CorrelationID, nullTime(e.ObservedAt), e.SessionID, e.ConversationID, e.ParentConversationID,
		e.AgentID, e.UserID,
		e.Provider, e.Model, e.Configuration, e.Protocol, e.StatusCode, e.TokensIn, e.TokensOut, tags, span)
	if err != nil {
		return fmt.Errorf("store: upsert request event: %w", err)
	}
	return nil
}

// GetRequestEvent returns one event (scalars + the span_event blob) by
// correlation_id, or ErrRequestEventNotFound.
func (s *Store) GetRequestEvent(ctx context.Context, correlationID string) (RequestEvent, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+requestEventColumns+` FROM request_events WHERE correlation_id=$1`, correlationID)
	e, err := scanRequestEvent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RequestEvent{}, ErrRequestEventNotFound
	}
	return e, err
}

// ListRecentRequestEvents returns the most recent events, newest first, capped
// at limit (defaulted to 100 when <= 0).
func (s *Store) ListRecentRequestEvents(ctx context.Context, limit int) ([]RequestEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx,
		`SELECT `+requestEventColumns+` FROM request_events ORDER BY observed_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list request events: %w", err)
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

// scanRequestEvent scans the full projection including span_event.
func scanRequestEvent(s rowScanner) (RequestEvent, error) {
	var e RequestEvent
	if err := s.Scan(&e.CorrelationID, &e.ObservedAt, &e.SessionID, &e.ConversationID, &e.ParentConversationID,
		&e.AgentID, &e.UserID,
		&e.Provider, &e.Model, &e.Configuration, &e.Protocol, &e.StatusCode, &e.TokensIn, &e.TokensOut, &e.SpanEvent); err != nil {
		return RequestEvent{}, fmt.Errorf("store: scan request event: %w", err)
	}
	return e, nil
}
