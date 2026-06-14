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
	Provenance    json.RawMessage
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
		   span_start, span_end, span_basis, localization, detector_id, detector_version)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		f.CorrelationID, f.UnitID, f.CheckType, f.Category, f.Score, f.RawLabel,
		f.SpanStart, f.SpanEnd, f.SpanBasis, f.Localization, f.DetectorID, f.DetectorVersion)
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
		   span_start, span_end, span_basis, localization, detector_id, detector_version
		 FROM finding WHERE correlation_id=$1`, correlationID)
	if err != nil {
		return nil, fmt.Errorf("store: list findings: %w", err)
	}
	defer rows.Close()

	var out []Finding
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.CorrelationID, &f.UnitID, &f.CheckType, &f.Category, &f.Score, &f.RawLabel,
			&f.SpanStart, &f.SpanEnd, &f.SpanBasis, &f.Localization, &f.DetectorID, &f.DetectorVersion); err != nil {
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
INSERT INTO verdict (correlation_id, state, max_score, top_category, finding_count, inconclusive, provenance)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (correlation_id) DO UPDATE SET
  state = EXCLUDED.state,
  max_score = EXCLUDED.max_score,
  top_category = EXCLUDED.top_category,
  finding_count = EXCLUDED.finding_count,
  inconclusive = EXCLUDED.inconclusive,
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
		v.CorrelationID, v.State, v.MaxScore, v.TopCategory, v.FindingCount, inconclusive, provenance)
	if err != nil {
		return fmt.Errorf("store: upsert verdict: %w", err)
	}
	return nil
}

// GetVerdict returns the verdict for a correlation id, or ErrVerdictNotFound.
func (s *Store) GetVerdict(ctx context.Context, correlationID string) (Verdict, error) {
	row := s.db.QueryRow(ctx,
		`SELECT correlation_id, state, max_score, top_category, finding_count, inconclusive, provenance
		 FROM verdict WHERE correlation_id=$1`, correlationID)
	var v Verdict
	var provenance []byte
	if err := row.Scan(&v.CorrelationID, &v.State, &v.MaxScore, &v.TopCategory, &v.FindingCount, &v.Inconclusive, &provenance); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Verdict{}, ErrVerdictNotFound
		}
		return Verdict{}, fmt.Errorf("store: get verdict: %w", err)
	}
	v.Provenance = provenance
	return v, nil
}
