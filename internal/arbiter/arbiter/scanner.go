package arbiter

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
	"github.com/andyjmorgan/slipspace-gateway/internal/safego"
)

// Runtime tunables. The per-call budget and retry schedule implement ADR-008;
// the lease outlives the budget so a task is not reclaimed mid-flight.
const (
	schemaVersion           = "v1"
	detectTimeout           = 240 * time.Second
	detectLease             = 300 // seconds; > detectTimeout so an in-flight task is not reclaimed
	maxAttempts             = 5
	claimBatch              = 32
	defaultDispatchInterval = 1 * time.Second
	defaultReduceInterval   = 2 * time.Second
	reduceBatch             = 64
)

// backoffSchedule is the retry delay by attempt index (ADR-008: 5m, 20m, 60m,
// 90m, 2h). The last entry is reused once attempts run past the slice.
var backoffSchedule = []time.Duration{
	5 * time.Minute, 20 * time.Minute, 60 * time.Minute, 90 * time.Minute, 2 * time.Hour,
}

// Store is the slice of the telemetry store the scanner runtime needs. *store.Store
// satisfies it; an interface keeps the runtime unit-testable with a fake.
type Store interface {
	GetRequestEvent(ctx context.Context, correlationID string) (store.RequestEvent, error)
	UpsertRequestEventWithChecks(ctx context.Context, e store.RequestEvent, checks []store.CheckTask) error
	ClaimCheckTasks(ctx context.Context, limit, leaseSeconds int) ([]store.CheckTask, error)
	CompleteCheckTask(ctx context.Context, correlationID, unitID, checkType, status string, result json.RawMessage) error
	RetryCheckTask(ctx context.Context, correlationID, unitID, checkType string, nextAttemptAt time.Time) error
	InsertFinding(ctx context.Context, f store.Finding) error
	InsertEvidence(ctx context.Context, ev store.Evidence) error
	InsertScanAudit(ctx context.Context, e store.ScanAuditEntry) error
	ListFindings(ctx context.Context, correlationID string) ([]store.Finding, error)
	InconclusiveCheckTypes(ctx context.Context, correlationID string) ([]string, error)
	CorrelationsReadyForVerdict(ctx context.Context, limit int) ([]string, error)
	UpsertVerdict(ctx context.Context, v store.Verdict) error
}

// detector is one configured detector endpoint, resolved from config.
type detector struct {
	checkType string
	family    string
	endpoint  string
	threshold float32
}

// Scanner is the Arbiter scanner: it explodes spans into check tasks at ingest
// (Explode) and, once Run is started, drains the outbox, calls detectors, and
// reduces findings to verdicts. Construct with New; inert until Run.
type Scanner struct {
	detectors  map[string]detector
	checkTypes []string // configured check types, sorted, stable
	client     *detectorClient
	enc        *evidenceCipher
	emitter    verdictEmitter // enriched-span emit (no-op unless configured)
	store      Store          // set by Run
	logger     *slog.Logger

	// scan-scoping + result filtering (compiled from config; zero values inert).
	tagSelector    tagSelector        // span-level scan_tags gate, applied in Explode
	findingExclude []string           // categories dropped before persist (globs)
	suppressor     findingSuppressor  // (category, offending-text) drop rules
	severity       severityClassifier // category→level for surviving findings

	// tunables (defaulted in New; tests may shrink them in white-box).
	workers          int
	leaseSeconds     int
	detectTimeout    time.Duration
	maxAttempts      int
	backoff          []time.Duration
	retention        time.Duration
	dispatchInterval time.Duration
	reduceInterval   time.Duration
}

// New builds a Scanner from the validated config; ctx scopes the enriched-export
// exporter setup. Returns an error only if the
// evidence key fails to load (Validate already checked it). The enabled check
// types are exactly those with a configured detector.
func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*Scanner, error) {
	key, err := cfg.EvidenceKeyBytes()
	if err != nil {
		return nil, err
	}
	enc, err := newEvidenceCipher(key)
	if err != nil {
		return nil, err
	}
	emitter, err := newEmitter(ctx, cfg.Scanner.EnrichedExport)
	if err != nil {
		return nil, err
	}
	suppressor, err := newFindingSuppressor(cfg.Scanner.FindingSuppress)
	if err != nil {
		return nil, err
	}
	dets := make(map[string]detector, len(cfg.Scanner.Detectors))
	cts := make([]string, 0, len(cfg.Scanner.Detectors))
	for _, d := range cfg.Scanner.Detectors {
		dets[d.CheckType] = detector{
			checkType: d.CheckType,
			family:    d.Family,
			endpoint:  d.Endpoint,
			threshold: d.Threshold,
		}
		cts = append(cts, d.CheckType)
	}
	sort.Strings(cts)
	return &Scanner{
		detectors:        dets,
		checkTypes:       cts,
		client:           newDetectorClient(nil),
		enc:              enc,
		emitter:          emitter,
		logger:           logger,
		workers:          cfg.ScannerWorkers(),
		leaseSeconds:     detectLease,
		detectTimeout:    detectTimeout,
		maxAttempts:      maxAttempts,
		backoff:          backoffSchedule,
		retention:        time.Duration(cfg.ScannerRetentionDays()) * 24 * time.Hour,
		dispatchInterval: defaultDispatchInterval,
		reduceInterval:   defaultReduceInterval,
		tagSelector:      tagSelector{include: cfg.Scanner.ScanTags.Include, exclude: cfg.Scanner.ScanTags.Exclude},
		findingExclude:   cfg.Scanner.FindingExclude,
		suppressor:       suppressor,
		severity:         newSeverityClassifier(cfg.Scanner.Severity),
	}, nil
}

// CheckTypes returns the configured check types (sorted).
func (s *Scanner) CheckTypes() []string { return s.checkTypes }

// Explode builds the check tasks for a request event: every scannable unit
// crossed with every configured check type the selection policy admits
// (ADR-018). The span-level scan_tags gate runs first — a span the tag policy
// excludes produces no tasks. Returns nil when there is nothing to scan, so the
// ingest path falls back to a plain upsert. Pure — no I/O — so the receiver can
// call it inside the upsert transaction.
func (s *Scanner) Explode(e store.RequestEvent) []store.CheckTask {
	// Span-level scan selection (scan_tags): a span the tag policy excludes is
	// not scanned at all — return nil so ingest falls back to a plain upsert,
	// exactly as if the scanner were disabled for it.
	if !s.tagSelector.admits(e.Tags) {
		return nil
	}
	units := BuildUnits(e)
	if len(units) == 0 {
		return nil
	}
	var tasks []store.CheckTask
	for _, u := range units {
		for _, ct := range s.checkTypes {
			if !appliesTo(ct, u) {
				continue
			}
			loc, err := json.Marshal(u.Locator)
			if err != nil {
				loc = []byte("{}")
			}
			tasks = append(tasks, store.CheckTask{
				CorrelationID: e.CorrelationID,
				UnitID:        u.ID,
				CheckType:     ct,
				Stage:         0,
				UnitLocator:   loc,
			})
		}
	}
	return tasks
}

// Run starts the scanner's background loops bound to ctx: a worker pool, a
// dispatcher that drains the outbox into it (SKIP LOCKED), and a reduce sweeper
// that writes verdicts at quiescence. Returns immediately; the loops exit on
// ctx cancellation.
func (s *Scanner) Run(ctx context.Context, st Store) {
	s.store = st
	tasks := make(chan store.CheckTask, s.workers*2)

	for i := 0; i < s.workers; i++ {
		safego.Go(ctx, "arbiter.worker", s.logger, nil, func() {
			for {
				select {
				case <-ctx.Done():
					return
				case t, ok := <-tasks:
					if !ok {
						return
					}
					s.process(ctx, t)
				}
			}
		})
	}
	safego.Go(ctx, "arbiter.dispatch", s.logger, nil, func() { s.dispatchLoop(ctx, tasks) })
	safego.Go(ctx, "arbiter.reduce", s.logger, nil, func() { s.reduceLoop(ctx) })
	safego.Go(ctx, "arbiter.emit.shutdown", s.logger, nil, func() {
		<-ctx.Done()
		// Detached from the cancelled run ctx on purpose — the shutdown budget
		// must outlive it to flush the exporter's batch processor.
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.emitter.shutdown(sctx) //nolint:contextcheck // intentional detached shutdown ctx (see above)
	})
}

// dispatchLoop claims due tasks and feeds them to the workers. It is the sole
// sender on tasks, so it owns closing the channel on shutdown.
func (s *Scanner) dispatchLoop(ctx context.Context, tasks chan<- store.CheckTask) {
	defer close(tasks)
	t := time.NewTicker(s.dispatchInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			claimed, err := s.store.ClaimCheckTasks(ctx, claimBatch, s.leaseSeconds)
			if err != nil {
				if ctx.Err() == nil {
					s.logger.Warn("arbiter: claim check tasks", "error", err)
				}
				continue
			}
			for _, c := range claimed {
				select {
				case tasks <- c:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}
