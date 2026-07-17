package store

import (
	"context"
	"fmt"
	"time"
)

// The advise-audit log reads the advise_audit table directly — the same
// invariant-#4-safe exception scan_audit takes (see scan_audit.go): it is a
// compact derived table on the control-plane advisory feed, one row per
// routing judgement, written post-response by the advise handler. It never
// touches the S3/spool Record channel.

// AdviseAuditEntry is one row of the advise_audit log (migration v21): a
// single agent-routing judgement — the payload the judge saw and the verdict
// it returned. ReceivedAt is output-only — on insert the DB defaults it to
// now(); reads populate it.
type AdviseAuditEntry struct {
	// ReceivedAt is when the advisory request was decided (DB default on insert).
	ReceivedAt time.Time
	// GatewayID is the HMAC-verified gateway that asked.
	GatewayID string
	// ConversationID is the subagent conversation the verdict decides for.
	ConversationID string
	// SessionID is the root session the conversation belongs to, when sent.
	SessionID string
	// Configuration is the gateway configuration the conversation runs on.
	Configuration string
	// Protocol is the wire protocol of the judged conversation (e.g. messages).
	Protocol string
	// Provider is the upstream provider of the judged conversation.
	Provider string
	// RequestedModel is the model the conversation asked for before any pin.
	RequestedModel string
	// AgentFamily is the identified agent family (e.g. claude-code).
	AgentFamily string
	// Entrypoint is the agent entrypoint (e.g. cli, sdk-cli).
	Entrypoint string
	// IsSubagent is the header-derived subagent flag.
	IsSubagent bool
	// ToolNames is the judged request's declared tool list.
	ToolNames []string
	// SystemPrefix is the system-prompt excerpt the judge saw (truncated).
	SystemPrefix string
	// FirstUserMessage is the task text the judge saw (truncated).
	FirstUserMessage string
	// VerdictSwitch reports whether the judge recommended switching model.
	VerdictSwitch bool
	// VerdictModel is the recommended model when switching (empty otherwise).
	VerdictModel string
	// VerdictReason is the judge's short justification.
	VerdictReason string
	// VerdictConfidence is the judge's self-reported confidence (0..1).
	VerdictConfidence float64
	// CacheHit reports the verdict was served from the template cache (no
	// judge call; JudgeLatencyMs is 0).
	CacheHit bool
	// JudgeLatencyMs is the wall-clock cost of the judge call for a fresh
	// judgement (0 on cache hits and failures that never reached the judge).
	JudgeLatencyMs int
	// Error is the judge failure text when no verdict was produced (empty on
	// decided rows).
	Error string
}

// InsertAdviseAudit appends one judgement row. received_at is left to the DB
// default (now()) — the handler never backdates an audit event. The table is
// append-only; there is no update or delete path. gateway_id and
// conversation_id must be non-empty (mirrors InsertScanAudit's validation
// posture: a row with no source or no conversation is meaningless).
func (s *Store) InsertAdviseAudit(ctx context.Context, e AdviseAuditEntry) error {
	if e.GatewayID == "" || e.ConversationID == "" {
		return fmt.Errorf("store: insert advise audit: gateway_id and conversation_id are required")
	}
	// pgx encodes a nil slice as SQL NULL; the column is NOT NULL DEFAULT '{}'.
	tools := e.ToolNames
	if tools == nil {
		tools = []string{}
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO advise_audit (
		    gateway_id, conversation_id, session_id, configuration, protocol,
		    provider, requested_model, agent_family, entrypoint, is_subagent,
		    tool_names, system_prefix, first_user_message,
		    verdict_switch, verdict_model, verdict_reason, verdict_confidence,
		    cache_hit, judge_latency_ms, error)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		e.GatewayID, e.ConversationID, e.SessionID, e.Configuration, e.Protocol,
		e.Provider, e.RequestedModel, e.AgentFamily, e.Entrypoint, e.IsSubagent,
		tools, e.SystemPrefix, e.FirstUserMessage,
		e.VerdictSwitch, e.VerdictModel, e.VerdictReason, e.VerdictConfidence,
		e.CacheHit, e.JudgeLatencyMs, e.Error)
	if err != nil {
		return fmt.Errorf("store: insert advise audit: %w", err)
	}
	return nil
}

// maxAdviseAuditPage caps one ListAdviseAudit page.
const maxAdviseAuditPage = 200

// ListAdviseAudit returns judgement rows received before `before`, newest
// first, capped at limit (clamped to [1, 200]; a zero `before` means now).
// The received_at DESC index (migration v21) backs the scan; keyset paging is
// by passing the last row's ReceivedAt as the next call's `before`.
func (s *Store) ListAdviseAudit(ctx context.Context, limit int, before time.Time) ([]AdviseAuditEntry, error) {
	if limit <= 0 || limit > maxAdviseAuditPage {
		limit = maxAdviseAuditPage
	}
	if before.IsZero() {
		before = time.Now()
	}
	rows, err := s.db.Query(ctx,
		`SELECT received_at, gateway_id, conversation_id, session_id, configuration,
		        protocol, provider, requested_model, agent_family, entrypoint,
		        is_subagent, tool_names, system_prefix, first_user_message,
		        verdict_switch, verdict_model, verdict_reason, verdict_confidence,
		        cache_hit, judge_latency_ms, error
		 FROM advise_audit
		 WHERE received_at < $1
		 ORDER BY received_at DESC
		 LIMIT $2`, before, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list advise audit: %w", err)
	}
	defer rows.Close()

	var out []AdviseAuditEntry
	for rows.Next() {
		var e AdviseAuditEntry
		if err := rows.Scan(&e.ReceivedAt, &e.GatewayID, &e.ConversationID, &e.SessionID,
			&e.Configuration, &e.Protocol, &e.Provider, &e.RequestedModel,
			&e.AgentFamily, &e.Entrypoint, &e.IsSubagent, &e.ToolNames,
			&e.SystemPrefix, &e.FirstUserMessage,
			&e.VerdictSwitch, &e.VerdictModel, &e.VerdictReason, &e.VerdictConfidence,
			&e.CacheHit, &e.JudgeLatencyMs, &e.Error); err != nil {
			return nil, fmt.Errorf("store: scan advise-audit row: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AdviseSavingsRow is one down-ranked conversation's actual spend: the
// requests that ran on the pinned model (identified by the agent-route:<model>
// telemetry tag) and what they actually cost. The counterfactual is computed
// by the caller from operator-configured model rates — the store reports only
// measured facts.
type AdviseSavingsRow struct {
	// ConversationID is the pinned conversation.
	ConversationID string
	// RequestedModel is the model the conversation asked for before the pin.
	RequestedModel string
	// PinnedModel is the model the verdict pinned it to.
	PinnedModel string
	// PinnedRequests is how many requests ran under the pin.
	PinnedRequests int64
	// ActualUSD is the summed gateway-priced cost of those pinned requests.
	ActualUSD float64
}

// AdviseSavings reports, for every switch verdict received since `since`, the
// requests that actually ran pinned (request_events joined on conversation_id
// + the agent-route:<model> tag, at or after the verdict) and their summed
// cost, plus the judge's own overhead: the summed cost of every request the
// named judge agent made in the window. Duplicate verdicts for one
// conversation (a failure retry that later succeeded, a cache re-issue)
// collapse to the earliest, so pinned requests are never double-counted.
func (s *Store) AdviseSavings(ctx context.Context, since time.Time, judgeAgentID string) ([]AdviseSavingsRow, float64, error) {
	rows, err := s.db.Query(ctx,
		`WITH pinned AS (
		    SELECT conversation_id, requested_model, verdict_model,
		           min(received_at) AS pinned_at
		    FROM advise_audit
		    WHERE verdict_switch AND verdict_model <> '' AND received_at >= $1
		    GROUP BY conversation_id, requested_model, verdict_model
		)
		SELECT p.conversation_id, p.requested_model, p.verdict_model,
		       count(e.correlation_id), COALESCE(sum(e.cost_usd), 0)
		FROM pinned p
		LEFT JOIN request_events e
		  ON e.conversation_id = p.conversation_id
		 AND e.tags @> ARRAY['agent-route:' || p.verdict_model]
		 AND e.observed_at >= p.pinned_at
		GROUP BY p.conversation_id, p.requested_model, p.verdict_model
		ORDER BY 5 DESC`, since)
	if err != nil {
		return nil, 0, fmt.Errorf("store: advise savings: %w", err)
	}
	defer rows.Close()

	var out []AdviseSavingsRow
	for rows.Next() {
		var r AdviseSavingsRow
		if err := rows.Scan(&r.ConversationID, &r.RequestedModel, &r.PinnedModel,
			&r.PinnedRequests, &r.ActualUSD); err != nil {
			return nil, 0, fmt.Errorf("store: scan advise-savings row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	var judgeCost float64
	err = s.db.QueryRow(ctx,
		`SELECT COALESCE(sum(cost_usd), 0) FROM request_events
		 WHERE agent_id = $1 AND observed_at >= $2`, judgeAgentID, since).Scan(&judgeCost)
	if err != nil {
		return nil, 0, fmt.Errorf("store: advise judge cost: %w", err)
	}
	return out, judgeCost, nil
}
