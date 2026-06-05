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

// EventDetail is the structured envelope stored in request_events.detail: the
// post-rule tags and the names of the rules that fired, so the message view can
// render them as lists.
type EventDetail struct {
	// Tags is the post-rule tag set.
	Tags []string `json:"tags,omitempty"`
	// RulesFired names the rules that matched.
	RulesFired []string `json:"rules_fired,omitempty"`
}

const requestEventColumns = `correlation_id, gateway_id, configuration, provider, model, protocol, method, status_code, upstream_status, latency_ms, tokens_in, tokens_out, tokens_cached, tokens_cache_creation, session_id, session_id_source, api_key_name, policy_ref, streaming, gen_ai_content, detail, observed_at`

// UpsertRequestEvent inserts or refines a request event, keyed by
// correlation_id, so re-delivered or two-phase (request then response)
// telemetry converges on one row. Existing gen_ai_content / detail are
// preserved when an update omits them. A zero ObservedAt defaults to now()
// server-side; a non-zero ObservedAt is honored verbatim.
func (s *Store) UpsertRequestEvent(ctx context.Context, e RequestEvent) error {
	_, err := s.db.Exec(ctx, `
INSERT INTO request_events (correlation_id, gateway_id, configuration, provider, model, protocol, method, status_code, upstream_status, latency_ms, tokens_in, tokens_out, tokens_cached, tokens_cache_creation, session_id, session_id_source, api_key_name, policy_ref, streaming, gen_ai_content, detail, observed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,COALESCE($22, now()))
ON CONFLICT (correlation_id) DO UPDATE SET
  gateway_id            = EXCLUDED.gateway_id,
  configuration         = EXCLUDED.configuration,
  provider               = EXCLUDED.provider,
  model                 = EXCLUDED.model,
  protocol              = EXCLUDED.protocol,
  method                = EXCLUDED.method,
  status_code           = EXCLUDED.status_code,
  upstream_status       = EXCLUDED.upstream_status,
  latency_ms            = EXCLUDED.latency_ms,
  tokens_in             = EXCLUDED.tokens_in,
  tokens_out            = EXCLUDED.tokens_out,
  tokens_cached         = EXCLUDED.tokens_cached,
  tokens_cache_creation = EXCLUDED.tokens_cache_creation,
  session_id            = EXCLUDED.session_id,
  session_id_source     = EXCLUDED.session_id_source,
  api_key_name          = EXCLUDED.api_key_name,
  policy_ref            = EXCLUDED.policy_ref,
  streaming             = EXCLUDED.streaming,
  gen_ai_content        = COALESCE(EXCLUDED.gen_ai_content, request_events.gen_ai_content),
  detail                = COALESCE(EXCLUDED.detail, request_events.detail)`,
		e.CorrelationID, e.GatewayID, e.Configuration, e.Provider, e.Model, e.Protocol, e.Method,
		e.StatusCode, e.UpstreamStatus, e.LatencyMs, e.TokensIn, e.TokensOut, e.TokensCached, e.TokensCacheCreation,
		e.SessionID, e.SessionIDSource, e.APIKeyName, e.PolicyRef, e.Streaming, nullJSON(e.GenAIContent), nullJSON(e.Detail), nullTime(e.ObservedAt))
	if err != nil {
		return fmt.Errorf("store: upsert request event: %w", err)
	}
	return nil
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
