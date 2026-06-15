package arbiter

import (
	"context"
	"errors"
	"net/http"
	"testing"

	detectv1 "github.com/andyjmorgan/sluice-gateway/gen/slipspace/detect/v1"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// processOne ingests a span, claims its tasks, and processes them through the
// worker — the shortest path that exercises process/handleResponse/handleFailure
// and the audit hooks.
func processOne(t *testing.T, ctx context.Context, s *Scanner, fake *fakeStore, e store.RequestEvent) {
	t.Helper()
	if err := fake.UpsertRequestEventWithChecks(ctx, e, s.Explode(e)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	claimed, err := fake.ClaimCheckTasks(ctx, 100, s.leaseSeconds)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	for _, task := range claimed {
		s.process(ctx, task)
	}
}

func TestAudit_TimeoutWritesRow(t *testing.T) {
	ctx := context.Background()
	fake := newFakeStore()
	s := testScanner(fake, "http://detector")
	// A detector call that hit the per-call deadline surfaces as a wrapped
	// context.DeadlineExceeded — the worker must tag the audit row "timeout".
	s.client = newDetectorClient(stubDoer{err: context.DeadlineExceeded})

	processOne(t, ctx, s, fake, spanWith("to-1", map[string]any{"input_messages": []map[string]any{msg("user", textPart("hi"))}}))

	reasons := fake.auditReasons()
	if len(reasons) == 0 {
		t.Fatal("timeout produced no scan-audit row")
	}
	for _, r := range reasons {
		if r != store.ScanReasonTimeout {
			t.Errorf("audit reason = %q, want timeout", r)
		}
	}
}

func TestAudit_UnreachableWritesRow(t *testing.T) {
	ctx := context.Background()
	fake := newFakeStore()
	s := testScanner(fake, "http://detector")
	// A plain transport/dial error (not a deadline) → "unreachable".
	s.client = newDetectorClient(stubDoer{err: errors.New("dial tcp: connection refused")})

	processOne(t, ctx, s, fake, spanWith("ur-1", map[string]any{"input_messages": []map[string]any{msg("user", textPart("hi"))}}))

	for _, r := range fake.auditReasons() {
		if r != store.ScanReasonUnreachable {
			t.Errorf("audit reason = %q, want unreachable", r)
		}
	}
	if len(fake.auditReasons()) == 0 {
		t.Fatal("unreachable produced no scan-audit row")
	}
}

func TestAudit_DetectorErrorWritesRow(t *testing.T) {
	ctx := context.Background()
	srv := detectorServer(t, func(req *detectv1.DetectRequest) (*detectv1.DetectResponse, int) {
		// 200 with an ERROR status body — the operational detector_error path.
		return &detectv1.DetectResponse{CorrelationId: req.GetCorrelationId(), Status: detectv1.Status_STATUS_ERROR, Error: "model oom"}, http.StatusOK
	})
	defer srv.Close()
	fake := newFakeStore()
	s := testScanner(fake, srv.URL)

	processOne(t, ctx, s, fake, spanWith("de-1", map[string]any{"input_messages": []map[string]any{msg("user", textPart("hi"))}}))

	if r := fake.auditReasons(); len(r) != 1 || r[0] != store.ScanReasonDetectorError {
		t.Fatalf("audit reasons = %+v, want [detector_error]", r)
	}
}

func TestAudit_NoDetectorWritesRow(t *testing.T) {
	ctx := context.Background()
	fake := newFakeStore()
	s := testScanner(fake, "http://detector") // configures only injection
	// A task for an unconfigured check type takes the no-detector path.
	s.process(ctx, store.CheckTask{CorrelationID: "nd-1", UnitID: "in:0:0", CheckType: "pii"})

	if r := fake.auditReasons(); len(r) != 1 || r[0] != store.ScanReasonNoDetector {
		t.Fatalf("audit reasons = %+v, want [no_detector]", r)
	}
	if fake.audits[0].DetectorID != "" {
		t.Errorf("no-detector audit should carry no detector id, got %q", fake.audits[0].DetectorID)
	}
}

func TestAudit_UnitMissingWritesRow(t *testing.T) {
	ctx := context.Background()
	fake := newFakeStore()
	s := testScanner(fake, "http://detector")
	// The task references a unit on a span that was never ingested → resolveUnit
	// fails → unit_missing (inconclusive).
	s.process(ctx, store.CheckTask{CorrelationID: "um-1", UnitID: "in:0:0", CheckType: CheckInjection})

	if r := fake.auditReasons(); len(r) != 1 || r[0] != store.ScanReasonUnitMissing {
		t.Fatalf("audit reasons = %+v, want [unit_missing]", r)
	}
}

func TestAudit_CleanCompleteWritesNoRow(t *testing.T) {
	ctx := context.Background()
	srv := detectorServer(t, func(req *detectv1.DetectRequest) (*detectv1.DetectResponse, int) {
		return &detectv1.DetectResponse{CorrelationId: req.GetCorrelationId(), Status: detectv1.Status_STATUS_OK}, http.StatusOK
	})
	defer srv.Close()
	fake := newFakeStore()
	s := testScanner(fake, srv.URL)

	processOne(t, ctx, s, fake, spanWith("ok-1", map[string]any{"input_messages": []map[string]any{msg("user", textPart("benign text"))}}))

	if r := fake.auditReasons(); len(r) != 0 {
		t.Fatalf("clean completion should write no scan-audit rows, got %+v", r)
	}
}
