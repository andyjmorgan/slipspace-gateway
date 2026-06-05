package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrRequestEventNotFound is returned when no event matches a correlation id.
var ErrRequestEventNotFound = errors.New("store: request event not found")

// RequestEvent is one request's lean, queryable telemetry row: post-rule
// labels, outcome, token counts, and (optionally) bounded gen_ai.* content.
// Both the gen_ai and sluice OTLP feeds upsert into it (keyed correlation_id);
// it is the recent-history + drill-down surface, joined to the heavy payloads
// (request_payloads) by correlation_id and grouped into sessions by session_id.
type RequestEvent struct {
	// CorrelationID is the per-request join key.
	CorrelationID string
	// GatewayID names the appliance that produced the event.
	GatewayID string
	// Configuration is the resolved policy bundle name.
	Configuration string
	// Provider is the post-rule upstream provider (falls back to provider name).
	Provider string
	// Model is the requested model.
	Model string
	// Protocol is the post-rule wire protocol / endpoint.
	Protocol string
	// Method is the inbound client HTTP verb, distinguishing a model-list GET
	// from a completion POST.
	Method string
	// StatusCode is the client-facing HTTP status (may be rule-overridden).
	StatusCode int
	// UpstreamStatus is the provider-reported status, kept distinct from a
	// synthetic/overridden client status. Zero when the upstream reported none.
	UpstreamStatus int
	// LatencyMs is the request wall time in milliseconds.
	LatencyMs int64
	// TokensIn / TokensOut are the prompt / completion token counts.
	TokensIn  int64
	TokensOut int64
	// TokensCached / TokensCacheCreation are the prompt-cache read / write token
	// counts the provider billed.
	TokensCached        int64
	TokensCacheCreation int64
	// SessionID is the resolved session/conversation bundle id; SessionIDSource
	// names the header it was bundled from.
	SessionID       string
	SessionIDSource string
	// APIKeyName is the resolved Sluice API-key name (managed mode), empty in
	// passthrough.
	APIKeyName string
	// PolicyRef is the resilience policy the rules engine bound, empty for
	// single-shot requests.
	PolicyRef string
	// Streaming is true iff the upstream response was an SSE stream.
	Streaming bool
	// GenAIContent is bounded gen_ai.* content as JSON, or nil when none was
	// captured. Stored JSONB (queryable); need not be byte-exact.
	GenAIContent []byte
	// Detail is the JSONB envelope of structured fleet detail — post-rule tags
	// and fired-rule names ({tags:[], rules_fired:[]}) — or nil when none.
	Detail []byte
	// ObservedAt is when the event was observed; zero defaults to now() server
	// side so every replica shares one clock.
	ObservedAt time.Time
}

// EventDetail is the structured envelope stored in request_events.detail,
// sourced from the Record feed: the post-rule tags, the names of the rules that
// fired (a flat list for quick rendering), the full per-rule chain, and the
// resilience attempts — everything the per-request message inspector renders
// that has no GenAI semconv home. The aggregate "rules fired" / "tags fired"
// dashboard panels do NOT read this — they read the sluice.* meter rollups
// (invariant #4: dashboards read meters, never record scans). The detail is the
// inspector's source, the meters are the dashboard's.
type EventDetail struct {
	// Tags is the post-rule tag set.
	Tags []string `json:"tags,omitempty"`
	// RulesFired names the rules that matched (flat list; the per-rule
	// detail is in RuleChain).
	RulesFired []string `json:"rules_fired,omitempty"`
	// RuleChain is the ordered per-rule detail (actions applied, whether the
	// rule terminated evaluation, any apply-time error).
	RuleChain []RuleChainEntry `json:"rule_chain,omitempty"`
	// Attempts is the per-attempt resilience outcome record, present only for
	// requests that ran under a resilience policy.
	Attempts []AttemptDetail `json:"attempts,omitempty"`
}

// RuleChainEntry is one rule's entry in EventDetail.RuleChain — the inspector's
// view of a fired rule. Mirrors contracts/connector.RuleFired.
type RuleChainEntry struct {
	Name           string   `json:"name"`
	ActionsApplied []string `json:"actions_applied,omitempty"`
	Terminated     bool     `json:"terminated,omitempty"`
	ErrorMessage   string   `json:"error_message,omitempty"`
}

// AttemptDetail is one resilience attempt in EventDetail.Attempts. Mirrors
// contracts/connector.Attempt.
type AttemptDetail struct {
	Target      string `json:"target"`
	StartedAtNs int64  `json:"started_at_ns,omitempty"`
	DurationMs  int64  `json:"duration_ms,omitempty"`
	StatusCode  int    `json:"status_code,omitempty"`
	Error       string `json:"error,omitempty"`
	Outcome     string `json:"outcome"`
}

const requestEventColumns = `correlation_id, gateway_id, configuration, provider, model, protocol, method, status_code, upstream_status, latency_ms, tokens_in, tokens_out, tokens_cached, tokens_cache_creation, session_id, session_id_source, api_key_name, policy_ref, streaming, gen_ai_content, detail, observed_at`

// insertEventSQL is the shared INSERT … ON CONFLICT prefix for the two feed
// upserts. Both insert the full row (so whichever feed creates the row stamps
// every column it knows); they differ only in the DO UPDATE SET clause, which
// names the columns that feed OWNS. A column absent from a feed's SET is left
// untouched on conflict, so the gen_ai feed never clobbers gateway columns and
// the Record feed never clobbers gen_ai columns — the two converge on one row
// in either arrival order.
const insertEventSQL = `
INSERT INTO request_events (correlation_id, gateway_id, configuration, provider, model, protocol, method, status_code, upstream_status, latency_ms, tokens_in, tokens_out, tokens_cached, tokens_cache_creation, session_id, session_id_source, api_key_name, policy_ref, streaming, gen_ai_content, detail, observed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,COALESCE($22, now()))
ON CONFLICT (correlation_id) DO UPDATE SET `

// genAISetClause names the columns the gen_ai OTLP feed owns: model / provider /
// usage / duration / streaming / session, plus the bounded gen_ai content. It
// does NOT touch the gateway columns (configuration, protocol, method, status,
// detail, …) so a gen_ai span arriving after the Record never wipes them.
// Shared string dims (provider/model/session) and content use COALESCE so an
// empty/absent value in one delivery never clobbers a value the other feed (or
// an earlier delivery) already set — the design's either-order COALESCE merge.
// Numeric usage/latency take the latest value (a gen_ai span always carries
// them meaningfully).
const genAISetClause = `
  provider              = COALESCE(NULLIF(EXCLUDED.provider, ''), request_events.provider),
  model                 = COALESCE(NULLIF(EXCLUDED.model, ''), request_events.model),
  latency_ms            = EXCLUDED.latency_ms,
  tokens_in             = EXCLUDED.tokens_in,
  tokens_out            = EXCLUDED.tokens_out,
  tokens_cached         = EXCLUDED.tokens_cached,
  tokens_cache_creation = EXCLUDED.tokens_cache_creation,
  session_id            = COALESCE(NULLIF(EXCLUDED.session_id, ''), request_events.session_id),
  streaming             = EXCLUDED.streaming,
  gen_ai_content        = COALESCE(EXCLUDED.gen_ai_content, request_events.gen_ai_content)`

// gatewaySetClause names the columns the Record feed owns: the gateway-specific
// request facts OTel has no semconv for (configuration, protocol, method,
// status, api-key, policy, session source, tags + rule chain + attempts in
// detail), plus the post-rule provider/model/session the Record resolves
// authoritatively. It does NOT touch tokens / latency / gen_ai_content so a
// Record arriving after the gen_ai span never wipes the GenAI dims. Same
// COALESCE-non-empty rule for the string columns so an absent field in one
// Record delivery never clobbers a value already merged.
const gatewaySetClause = `
  gateway_id            = COALESCE(NULLIF(EXCLUDED.gateway_id, ''), request_events.gateway_id),
  configuration         = COALESCE(NULLIF(EXCLUDED.configuration, ''), request_events.configuration),
  provider              = COALESCE(NULLIF(EXCLUDED.provider, ''), request_events.provider),
  model                 = COALESCE(NULLIF(EXCLUDED.model, ''), request_events.model),
  protocol              = COALESCE(NULLIF(EXCLUDED.protocol, ''), request_events.protocol),
  method                = COALESCE(NULLIF(EXCLUDED.method, ''), request_events.method),
  status_code           = EXCLUDED.status_code,
  upstream_status       = EXCLUDED.upstream_status,
  session_id            = COALESCE(NULLIF(EXCLUDED.session_id, ''), request_events.session_id),
  session_id_source     = COALESCE(NULLIF(EXCLUDED.session_id_source, ''), request_events.session_id_source),
  api_key_name          = COALESCE(NULLIF(EXCLUDED.api_key_name, ''), request_events.api_key_name),
  policy_ref            = COALESCE(NULLIF(EXCLUDED.policy_ref, ''), request_events.policy_ref),
  detail                = COALESCE(EXCLUDED.detail, request_events.detail)`

// UpsertRequestEvent inserts or refines the gen_ai half of a request event,
// keyed by correlation_id, from the OTLP trace feed. It owns the GenAI columns
// only (see genAISetClause); the gateway columns are filled by
// UpsertGatewayRecord from the Record feed. A zero ObservedAt defaults to now()
// server-side; a non-zero ObservedAt is honored verbatim.
func (s *Store) UpsertRequestEvent(ctx context.Context, e RequestEvent) error {
	if err := s.execEventUpsert(ctx, e, genAISetClause); err != nil {
		return fmt.Errorf("store: upsert request event (gen_ai): %w", err)
	}
	return nil
}

// UpsertGatewayRecord inserts or refines the gateway half of a request event,
// keyed by correlation_id, from the Record feed. It owns the gateway columns
// only (see gatewaySetClause); the GenAI columns are filled by
// UpsertRequestEvent from the OTLP feed.
func (s *Store) UpsertGatewayRecord(ctx context.Context, e RequestEvent) error {
	if err := s.execEventUpsert(ctx, e, gatewaySetClause); err != nil {
		return fmt.Errorf("store: upsert request event (gateway): %w", err)
	}
	return nil
}

// execEventUpsert runs the shared INSERT with the given feed-owned SET clause.
func (s *Store) execEventUpsert(ctx context.Context, e RequestEvent, setClause string) error {
	_, err := s.db.Exec(ctx, insertEventSQL+setClause,
		e.CorrelationID, e.GatewayID, e.Configuration, e.Provider, e.Model, e.Protocol, e.Method,
		e.StatusCode, e.UpstreamStatus, e.LatencyMs, e.TokensIn, e.TokensOut, e.TokensCached, e.TokensCacheCreation,
		e.SessionID, e.SessionIDSource, e.APIKeyName, e.PolicyRef, e.Streaming, nullJSON(e.GenAIContent), nullJSON(e.Detail), nullTime(e.ObservedAt))
	return err
}

// GetRequestEvent returns one event by correlation_id, or
// ErrRequestEventNotFound.
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

func scanRequestEvent(s rowScanner) (RequestEvent, error) {
	var e RequestEvent
	if err := s.Scan(&e.CorrelationID, &e.GatewayID, &e.Configuration, &e.Provider, &e.Model, &e.Protocol, &e.Method,
		&e.StatusCode, &e.UpstreamStatus, &e.LatencyMs, &e.TokensIn, &e.TokensOut, &e.TokensCached, &e.TokensCacheCreation,
		&e.SessionID, &e.SessionIDSource, &e.APIKeyName, &e.PolicyRef, &e.Streaming, &e.GenAIContent, &e.Detail, &e.ObservedAt); err != nil {
		return RequestEvent{}, fmt.Errorf("store: scan request event: %w", err)
	}
	return e, nil
}
