package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CheckTask is one row of the Arbiter outbox (migration v13): a single
// (correlation_id, unit_id, check_type) scan to perform. The explode step
// inserts these in the same tx as the span upsert; the dispatcher claims them
// with SELECT ... FOR UPDATE SKIP LOCKED.
type CheckTask struct {
	// CorrelationID is the span's golden thread.
	CorrelationID string
	// UnitID identifies the content block within the span (e.g. "in:0:1").
	UnitID string
	// CheckType is the logical check: "injection", "toxicity", "pii".
	CheckType string
	// Attempt is how many times this task has been claimed.
	Attempt int
	// Stage is the in-span ordering gate (ADR-010); v1 always 0.
	Stage int
	// UnitLocator is the JSON locator (section, message/part index, kind, role,
	// tool name/id) used to re-attach a finding to its source part.
	UnitLocator json.RawMessage
}

// Finding is one detector hit (migration v13 finding table).
type Finding struct {
	CorrelationID   string
	UnitID          string
	CheckType       string
	Category        string
	Score           float32
	RawLabel        string
	SpanStart       *int
	SpanEnd         *int
	SpanBasis       string
	Localization    string
	DetectorID      string
	DetectorVersion string
	// OffendingText is the flagged text, denormalized onto the finding so the
	// Security report can show it directly (migration v14): the exact span
	// substring for localized hits, the whole unit otherwise. Plaintext.
	OffendingText string
	// Severity is the operator-assigned level for this finding's category
	// ("info"/"warning"/"error"), resolved from scanner.severity at scan time
	// (migration v17). Empty on rows written before the column existed.
	Severity string
}

// Evidence is one offending-field record (migration v13 evidence table),
// stored as application-side ciphertext — the store never sees the plaintext
// (ADR-018).
type Evidence struct {
	CorrelationID string
	UnitID        string
	CheckType     string
	Ciphertext    []byte
	Nonce         []byte
	KeyID         string
	ExpiresAt     *time.Time
}

// Verdict is the reduced per-span outcome (migration v13 verdict table).
type Verdict struct {
	CorrelationID string
	State         string
	MaxScore      float32
	TopCategory   string
	FindingCount  int
	Inconclusive  []string
	// Severity is the highest finding severity on the span ("info"/"warning"/
	// "error"), rolled up by the reduce step (migration v17). Empty when the
	// span has no findings.
	Severity   string
	Provenance json.RawMessage
}

// ErrVerdictNotFound is returned when no verdict matches a correlation id.
var ErrVerdictNotFound = errors.New("store: verdict not found")

const insertCheckTaskSQL = `
INSERT INTO check_tasks (correlation_id, unit_id, check_type, stage, unit_locator)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (correlation_id, unit_id, check_type) DO NOTHING`

// UpsertRequestEventWithChecks upserts the span entity and explodes its check
// tasks in ONE transaction (ADR-004 transactional outbox): if the pod dies
// after this commit the tasks already exist and the dispatcher resumes them; if
// it dies before, nothing is half-written. check_tasks insert is idempotent
// (ON CONFLICT DO NOTHING) so a re-ingested span does not re-scan.
func (s *Store) UpsertRequestEventWithChecks(ctx context.Context, e RequestEvent, checks []CheckTask) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin event+checks: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	span := e.SpanEvent
	if len(span) == 0 {
		span = []byte("{}")
	}
	tags := e.Tags
	if tags == nil {
		tags = []string{}
	}
	if _, err := tx.Exec(ctx, insertEventSQL,
		e.CorrelationID, nullTime(e.ObservedAt), e.SessionID, e.ConversationID, e.ParentConversationID,
		e.AgentID, e.UserID,
		e.Provider, e.Model, e.Configuration, e.Protocol, e.StatusCode, e.TokensIn, e.TokensOut, tags, span); err != nil {
		return fmt.Errorf("store: upsert event (with checks): %w", err)
	}
	for _, c := range checks {
		locator := c.UnitLocator
		if len(locator) == 0 {
			locator = []byte("{}")
		}
		if _, err := tx.Exec(ctx, insertCheckTaskSQL,
			c.CorrelationID, c.UnitID, c.CheckType, c.Stage, locator); err != nil {
			return fmt.Errorf("store: insert check task: %w", err)
		}
	}
	return commit(ctx, tx)
}

const claimCheckTasksSQL = `
WITH claimed AS (
  SELECT correlation_id, unit_id, check_type
  FROM check_tasks
  WHERE (status = 'pending' OR (status = 'processing' AND locked_until < now()))
    AND next_attempt_at <= now()
  ORDER BY next_attempt_at
  FOR UPDATE SKIP LOCKED
  LIMIT $1
)
UPDATE check_tasks t
SET status = 'processing',
    locked_until = now() + make_interval(secs => $2),
    attempt = t.attempt + 1,
    updated_at = now()
FROM claimed c
WHERE t.correlation_id = c.correlation_id AND t.unit_id = c.unit_id AND t.check_type = c.check_type
RETURNING t.correlation_id, t.unit_id, t.check_type, t.attempt, t.stage, t.unit_locator`

// ClaimCheckTasks atomically claims up to limit due tasks for this pod, setting
// each to 'processing' with a lease of leaseSeconds. SKIP LOCKED lets many pods
// drain the same table without blocking or double-grabbing; an expired lease
// makes a crashed pod's in-flight task reclaimable.
func (s *Store) ClaimCheckTasks(ctx context.Context, limit, leaseSeconds int) ([]CheckTask, error) {
	rows, err := s.db.Query(ctx, claimCheckTasksSQL, limit, leaseSeconds)
	if err != nil {
		return nil, fmt.Errorf("store: claim check tasks: %w", err)
	}
	defer rows.Close()

	var out []CheckTask
	for rows.Next() {
		var c CheckTask
		var locator []byte
		if err := rows.Scan(&c.CorrelationID, &c.UnitID, &c.CheckType, &c.Attempt, &c.Stage, &locator); err != nil {
			return nil, fmt.Errorf("store: scan claimed task: %w", err)
		}
		c.UnitLocator = locator
		out = append(out, c)
	}
	return out, rows.Err()
}

// CompleteCheckTask marks a claimed task terminal (status one of completed,
// inconclusive, failed), recording the detector result and clearing the lease.
func (s *Store) CompleteCheckTask(ctx context.Context, correlationID, unitID, checkType, status string, result json.RawMessage) error {
	if len(result) == 0 {
		result = []byte("null")
	}
	_, err := s.db.Exec(ctx,
		`UPDATE check_tasks SET status=$4, result=$5, locked_until=NULL, updated_at=now()
		 WHERE correlation_id=$1 AND unit_id=$2 AND check_type=$3`,
		correlationID, unitID, checkType, status, result)
	if err != nil {
		return fmt.Errorf("store: complete check task: %w", err)
	}
	return nil
}

// RetryCheckTask returns a task to 'pending' with a future next_attempt_at
// (backoff) and clears the lease, so the dispatcher re-claims it when due.
func (s *Store) RetryCheckTask(ctx context.Context, correlationID, unitID, checkType string, nextAttemptAt time.Time) error {
	_, err := s.db.Exec(ctx,
		`UPDATE check_tasks SET status='pending', next_attempt_at=$4, locked_until=NULL, updated_at=now()
		 WHERE correlation_id=$1 AND unit_id=$2 AND check_type=$3`,
		correlationID, unitID, checkType, nextAttemptAt)
	if err != nil {
		return fmt.Errorf("store: retry check task: %w", err)
	}
	return nil
}

// InsertFinding stores one detector hit.
func (s *Store) InsertFinding(ctx context.Context, f Finding) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO finding (correlation_id, unit_id, check_type, category, score, raw_label,
		   span_start, span_end, span_basis, localization, detector_id, detector_version, offending_text, severity)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		f.CorrelationID, f.UnitID, f.CheckType, f.Category, f.Score, f.RawLabel,
		f.SpanStart, f.SpanEnd, f.SpanBasis, f.Localization, f.DetectorID, f.DetectorVersion, f.OffendingText, f.Severity)
	if err != nil {
		return fmt.Errorf("store: insert finding: %w", err)
	}
	return nil
}

// InsertEvidence stores one encrypted offending-field record.
func (s *Store) InsertEvidence(ctx context.Context, ev Evidence) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO evidence (correlation_id, unit_id, check_type, ciphertext, nonce, key_id, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		ev.CorrelationID, ev.UnitID, ev.CheckType, ev.Ciphertext, ev.Nonce, ev.KeyID, ev.ExpiresAt)
	if err != nil {
		return fmt.Errorf("store: insert evidence: %w", err)
	}
	return nil
}

// ListFindings returns every finding for a correlation id (the hit set the
// reduce step aggregates).
func (s *Store) ListFindings(ctx context.Context, correlationID string) ([]Finding, error) {
	rows, err := s.db.Query(ctx,
		`SELECT correlation_id, unit_id, check_type, category, score, raw_label,
		   span_start, span_end, span_basis, localization, detector_id, detector_version, offending_text, severity
		 FROM finding WHERE correlation_id=$1`, correlationID)
	if err != nil {
		return nil, fmt.Errorf("store: list findings: %w", err)
	}
	defer rows.Close()

	var out []Finding
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.CorrelationID, &f.UnitID, &f.CheckType, &f.Category, &f.Score, &f.RawLabel,
			&f.SpanStart, &f.SpanEnd, &f.SpanBasis, &f.Localization, &f.DetectorID, &f.DetectorVersion, &f.OffendingText, &f.Severity); err != nil {
			return nil, fmt.Errorf("store: scan finding: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// InconclusiveCheckTypes returns the check types whose tasks ended inconclusive
// or failed for a correlation id — the set that raises the span to PARTIAL
// (ADR-017). Deduplicated.
func (s *Store) InconclusiveCheckTypes(ctx context.Context, correlationID string) ([]string, error) {
	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT check_type FROM check_tasks
		 WHERE correlation_id=$1 AND status IN ('inconclusive','failed') ORDER BY check_type`, correlationID)
	if err != nil {
		return nil, fmt.Errorf("store: inconclusive check types: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var ct string
		if err := rows.Scan(&ct); err != nil {
			return nil, fmt.Errorf("store: scan inconclusive: %w", err)
		}
		out = append(out, ct)
	}
	return out, rows.Err()
}

// CorrelationsReadyForVerdict returns up to limit correlation ids whose check
// tasks are all terminal (quiescence, ADR-017) and that have no verdict yet —
// the work list for the reduce step.
func (s *Store) CorrelationsReadyForVerdict(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.db.Query(ctx,
		`SELECT correlation_id FROM check_tasks
		 GROUP BY correlation_id
		 HAVING bool_and(status IN ('completed','inconclusive','failed'))
		    AND correlation_id NOT IN (SELECT correlation_id FROM verdict)
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: correlations ready for verdict: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: scan ready correlation: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

const upsertVerdictSQL = `
INSERT INTO verdict (correlation_id, state, max_score, top_category, finding_count, inconclusive, severity, provenance)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (correlation_id) DO UPDATE SET
  state = EXCLUDED.state,
  max_score = EXCLUDED.max_score,
  top_category = EXCLUDED.top_category,
  finding_count = EXCLUDED.finding_count,
  inconclusive = EXCLUDED.inconclusive,
  severity = EXCLUDED.severity,
  provenance = EXCLUDED.provenance,
  decided_at = now()`

// UpsertVerdict writes (or replaces) the reduced per-span verdict.
func (s *Store) UpsertVerdict(ctx context.Context, v Verdict) error {
	inconclusive := v.Inconclusive
	if inconclusive == nil {
		inconclusive = []string{}
	}
	provenance := v.Provenance
	if len(provenance) == 0 {
		provenance = []byte("{}")
	}
	_, err := s.db.Exec(ctx, upsertVerdictSQL,
		v.CorrelationID, v.State, v.MaxScore, v.TopCategory, v.FindingCount, inconclusive, v.Severity, provenance)
	if err != nil {
		return fmt.Errorf("store: upsert verdict: %w", err)
	}
	return nil
}

// FindingRow is one finding joined to its source request_events row (and the
// originating check task for the unit's kind/role) — the operator Security
// view's unit. It carries the finding itself plus the deep-link keys
// (CorrelationID → message inspector, SessionID → session view) and the request
// facts (ObservedAt, Model, Configuration) the operator triages by. The same
// row type backs both the recent-across-all-sessions list and the
// per-session list.
type FindingRow struct {
	// Category is the normalized taxonomy entry, e.g. "pii.email".
	Category string
	// CheckType is the check that produced the finding.
	CheckType string
	// Score is the detector confidence in [0,1].
	Score float32
	// RawLabel is the detector's native label.
	RawLabel string
	// UnitKind / UnitRole are the offending content block's kind/role, projected
	// from the check task's unit_locator JSONB; empty when the locator carried
	// none (or the task row has been pruned).
	UnitKind string
	UnitRole string
	// CorrelationID is the request the finding came from.
	CorrelationID string
	// SessionID / ObservedAt / Model / Configuration come from the joined
	// request_events row; empty when the join finds no matching event (a finding
	// can outlive its event under retention skew).
	SessionID     string
	ObservedAt    time.Time
	Model         string
	Configuration string
	// OffendingText is the flagged text the finding fired on (migration v14):
	// the exact span substring for localized hits, the whole unit otherwise.
	OffendingText string
	// Severity is the operator-assigned level for the finding's category
	// ("info"/"warning"/"error", migration v17); empty on pre-v17 rows.
	Severity string
}

// defaultFindingsListLimit is the safety cap ListRecentFindings applies when the
// caller passes a non-positive limit. It bounds a wide time-range scan (the
// Security page now selects by window, not row count) so an "All" preset can't
// pull unbounded rows; sized generously so a normal 24h window is never clipped.
const defaultFindingsListLimit = 500

// findingRowSelect is the shared SELECT for both findings-list queries: the
// finding fields, the unit kind/role pulled from the originating check task's
// locator, and the request facts from request_events. The LEFT JOINs keep a
// finding whose check task or event has aged out (those columns come back
// empty). Ordering by observed_at then id keeps it newest-first with a stable
// tie-break when many findings share a request.
const findingRowSelect = `
SELECT f.category, f.check_type, f.score, f.raw_label,
       COALESCE(t.unit_locator->>'kind', ''), COALESCE(t.unit_locator->>'role', ''),
       f.correlation_id,
       COALESCE(e.session_id, ''), e.observed_at,
       COALESCE(e.model, ''), COALESCE(e.configuration, ''), f.offending_text, f.severity
FROM finding f
LEFT JOIN check_tasks t
  ON t.correlation_id = f.correlation_id AND t.unit_id = f.unit_id AND t.check_type = f.check_type
LEFT JOIN request_events e ON e.correlation_id = f.correlation_id`

// scanFindingRows drains a findings-list query into FindingRow values. ObservedAt
// is nullable (the LEFT JOIN may miss), so it scans through *time.Time.
func scanFindingRows(rows rows) ([]FindingRow, error) {
	defer rows.Close()
	var out []FindingRow
	for rows.Next() {
		var f FindingRow
		var observed *time.Time
		if err := rows.Scan(&f.Category, &f.CheckType, &f.Score, &f.RawLabel,
			&f.UnitKind, &f.UnitRole, &f.CorrelationID,
			&f.SessionID, &observed, &f.Model, &f.Configuration, &f.OffendingText, &f.Severity); err != nil {
			return nil, fmt.Errorf("store: scan finding row: %w", err)
		}
		if observed != nil {
			f.ObservedAt = *observed
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListRecentFindings returns the most recent findings across all sessions,
// newest first — the top-level Security page's backing query. from/to bound the
// window on the request event's observed_at (zero = unbounded on that side), so
// the Security page can select by relative window like the messages/sessions
// browsers. limit is a safety cap (<= 0 applies the default). A set from bound
// excludes findings whose event has aged out (NULL observed_at fails the range
// predicate); the unbounded "All" preset preserves the prior behavior.
func (s *Store) ListRecentFindings(ctx context.Context, from, to time.Time, limit int) ([]FindingRow, error) {
	if limit <= 0 {
		limit = defaultFindingsListLimit
	}
	args := []any{}
	where := ""
	addBound := func(op string, t time.Time) {
		if t.IsZero() {
			return
		}
		args = append(args, t)
		if where == "" {
			where = "\nWHERE "
		} else {
			where += " AND "
		}
		where += fmt.Sprintf("e.observed_at %s $%d", op, len(args))
	}
	addBound(">=", from)
	addBound("<=", to)
	args = append(args, limit)
	q := findingRowSelect + where + fmt.Sprintf(`
ORDER BY e.observed_at DESC NULLS LAST, f.id DESC
LIMIT $%d`, len(args))
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list recent findings: %w", err)
	}
	return scanFindingRows(rows)
}

// ListFindingsBySession returns every finding whose source request belongs to
// the given session, newest first — the session view's Security tab. Scoped by
// the request_events.session_id join column, so a finding whose event has aged
// out (no session_id) never appears here.
func (s *Store) ListFindingsBySession(ctx context.Context, sessionID string) ([]FindingRow, error) {
	rows, err := s.db.Query(ctx, findingRowSelect+`
WHERE e.session_id = $1
ORDER BY e.observed_at DESC NULLS LAST, f.id DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: list findings by session: %w", err)
	}
	return scanFindingRows(rows)
}

// GetVerdict returns the verdict for a correlation id, or ErrVerdictNotFound.
func (s *Store) GetVerdict(ctx context.Context, correlationID string) (Verdict, error) {
	row := s.db.QueryRow(ctx,
		`SELECT correlation_id, state, max_score, top_category, finding_count, inconclusive, severity, provenance
		 FROM verdict WHERE correlation_id=$1`, correlationID)
	var v Verdict
	var provenance []byte
	if err := row.Scan(&v.CorrelationID, &v.State, &v.MaxScore, &v.TopCategory, &v.FindingCount, &v.Inconclusive, &v.Severity, &provenance); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Verdict{}, ErrVerdictNotFound
		}
		return Verdict{}, fmt.Errorf("store: get verdict: %w", err)
	}
	v.Provenance = provenance
	return v, nil
}
