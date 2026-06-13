package arbiter

import (
	"context"
	"errors"
	"testing"
	"time"

	detectv1 "github.com/andyjmorgan/sluice-gateway/gen/slipspace/detect/v1"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// extend errStore (new_test.go) with more fault toggles.
func (e errStore) ClaimCheckTasks(ctx context.Context, limit, lease int) ([]store.CheckTask, error) {
	if e.failClaim {
		return nil, errors.New("claim boom")
	}
	return e.fakeStore.ClaimCheckTasks(ctx, limit, lease)
}

func (e errStore) CorrelationsReadyForVerdict(ctx context.Context, limit int) ([]string, error) {
	if e.failReady {
		return nil, errors.New("ready boom")
	}
	return e.fakeStore.CorrelationsReadyForVerdict(ctx, limit)
}

func (e errStore) InconclusiveCheckTypes(ctx context.Context, c string) ([]string, error) {
	if e.failInconclusive {
		return nil, errors.New("inconclusive boom")
	}
	return e.fakeStore.InconclusiveCheckTypes(ctx, c)
}

func TestBackoffFor_Clamps(t *testing.T) {
	s := &Scanner{backoff: []time.Duration{time.Second, 2 * time.Second}}
	if d := s.backoffFor(0); d < time.Second { // attempt 0 clamps to index 0
		t.Errorf("attempt 0 -> %v", d)
	}
	if d := s.backoffFor(99); d < 2*time.Second { // clamps to last entry
		t.Errorf("attempt 99 -> %v", d)
	}
}

func TestProcess_NoDetectorForCheckType(t *testing.T) {
	ctx := context.Background()
	fake := newFakeStore()
	s := testScanner(fake, "") // only injection configured
	k := key("c", "u", "toxicity")
	fake.tasks[k] = &taskRow{status: "processing", attempt: 1}
	s.process(ctx, store.CheckTask{CorrelationID: "c", UnitID: "u", CheckType: "toxicity"})
	if fake.tasks[k].status != statusFailed {
		t.Errorf("status = %q, want failed", fake.tasks[k].status)
	}
}

func TestProcess_SpanGone(t *testing.T) {
	ctx := context.Background()
	fake := newFakeStore()
	s := testScanner(fake, "")
	k := key("c", "u", "injection")
	fake.tasks[k] = &taskRow{status: "processing", attempt: 1}
	// no event stored => resolveUnit fails => inconclusive (could not scan).
	s.process(ctx, store.CheckTask{CorrelationID: "c", UnitID: "u", CheckType: "injection"})
	if fake.tasks[k].status != statusInconclusive {
		t.Errorf("status = %q, want inconclusive", fake.tasks[k].status)
	}
}

func TestProcess_UnitIDNotInSpan(t *testing.T) {
	ctx := context.Background()
	fake := newFakeStore()
	s := testScanner(fake, "")
	e := spanWith("c", map[string]any{"input_messages": []map[string]any{msg("user", textPart("hi"))}})
	fake.events["c"] = e
	k := key("c", "in:9:9", "injection")
	fake.tasks[k] = &taskRow{status: "processing", attempt: 1}
	s.process(ctx, store.CheckTask{CorrelationID: "c", UnitID: "in:9:9", CheckType: "injection"})
	if fake.tasks[k].status != statusInconclusive {
		t.Errorf("status = %q, want inconclusive", fake.tasks[k].status)
	}
}

func TestReduce_InconclusiveError(t *testing.T) {
	fake := newFakeStore()
	s := testScanner(fake, "")
	s.store = errStore{fakeStore: fake, failInconclusive: true}
	s.reduce(context.Background(), "x")
	if _, ok := fake.verdict("x"); ok {
		t.Error("verdict written despite inconclusive error")
	}
}

func TestDispatchLoop_ClaimErrorThenCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := newFakeStore()
	s := testScanner(fake, "")
	s.store = errStore{fakeStore: fake, failClaim: true}
	s.dispatchInterval = time.Millisecond
	ch := make(chan store.CheckTask, 1)
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()
	s.dispatchLoop(ctx, ch) // exercises the claim-error branch, returns on cancel
}

func TestReduceLoop_ReadyErrorThenCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := newFakeStore()
	s := testScanner(fake, "")
	s.store = errStore{fakeStore: fake, failReady: true}
	s.reduceInterval = time.Millisecond
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()
	s.reduceLoop(ctx) // exercises the ready-error branch, returns on cancel
}

func (e errStore) RetryCheckTask(ctx context.Context, c, u, ck string, ts time.Time) error {
	if e.failRetry {
		return errors.New("retry boom")
	}
	return e.fakeStore.RetryCheckTask(ctx, c, u, ck, ts)
}

func (e errStore) InsertFinding(ctx context.Context, f store.Finding) error {
	if e.failFinding {
		return errors.New("finding boom")
	}
	return e.fakeStore.InsertFinding(ctx, f)
}

func (e errStore) InsertEvidence(ctx context.Context, ev store.Evidence) error {
	if e.failEvidence {
		return errors.New("evidence boom")
	}
	return e.fakeStore.InsertEvidence(ctx, ev)
}

// detector returning a finding with an explicit span — covers the span-mapping
// branch of storeFindings.
func spanFindingServer(t *testing.T) string {
	t.Helper()
	srv := detectorServer(t, func(req *dv1Req) (*dv1Resp, int) {
		return &dv1Resp{
			CorrelationId: req.GetCorrelationId(),
			Status:        dv1OK,
			Findings: []*dv1Finding{{
				Category: "pii.email", Score: 0.9,
				Span: &dv1Span{Start: 2, End: 7, Basis: dv1ByteBasis},
			}},
		}, 200
	})
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestStoreFindings_WithSpan(t *testing.T) {
	ctx := context.Background()
	fake := newFakeStore()
	s := testScanner(fake, spanFindingServer(t))
	e := spanWith("sp", map[string]any{"input_messages": []map[string]any{msg("user", textPart("email me at a@b.com"))}})
	_ = fake.UpsertRequestEventWithChecks(ctx, e, s.Explode(e))
	drain(t, ctx, s, fake)
	if len(fake.findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(fake.findings))
	}
	f := fake.findings[0]
	if f.SpanStart == nil || *f.SpanStart != 2 || f.SpanEnd == nil || *f.SpanEnd != 7 || f.SpanBasis == "" {
		t.Errorf("span not mapped: %+v", f)
	}
}

func TestStoreFindings_InsertErrorsLogged(t *testing.T) {
	ctx := context.Background()
	fake := newFakeStore()
	s := testScanner(fake, spanFindingServer(t))
	s.store = errStore{fakeStore: fake, failFinding: true, failEvidence: true}
	e := spanWith("sp2", map[string]any{"input_messages": []map[string]any{msg("user", textPart("a@b.com"))}})
	_ = fake.UpsertRequestEventWithChecks(ctx, e, s.Explode(e))
	claimed, _ := fake.ClaimCheckTasks(ctx, 10, 120)
	for _, task := range claimed {
		s.process(ctx, task) // insert errors are logged, must not panic
	}
}

func TestHandleFailure_RetryErrorLogged(t *testing.T) {
	ctx := context.Background()
	down := detectorServer(t, func(req *dv1Req) (*dv1Resp, int) { return nil, 500 })
	defer down.Close()
	fake := newFakeStore()
	s := testScanner(fake, down.URL)
	s.store = errStore{fakeStore: fake, failRetry: true}
	e := spanWith("rf", map[string]any{"input_messages": []map[string]any{msg("user", textPart("hi"))}})
	fake.events["rf"] = e
	// Attempt 1 < maxAttempts(2) => retry path; RetryCheckTask errors and is logged.
	s.process(ctx, store.CheckTask{CorrelationID: "rf", UnitID: "in:0:0", CheckType: CheckInjection, Attempt: 1})
}

func TestDetect_BuildRequestError(t *testing.T) {
	c := newDetectorClient(nil)
	if _, err := c.Detect(context.Background(), "http://%zz", &dv1Req{}); err == nil {
		t.Error("expected error for malformed endpoint")
	}
}

// short aliases to keep the helper signatures readable.
type (
	dv1Req     = detectv1.DetectRequest
	dv1Resp    = detectv1.DetectResponse
	dv1Finding = detectv1.Finding
	dv1Span    = detectv1.Span
)

const (
	dv1OK        = detectv1.Status_STATUS_OK
	dv1ByteBasis = detectv1.OffsetBasis_OFFSET_BASIS_UTF8_BYTE
)
