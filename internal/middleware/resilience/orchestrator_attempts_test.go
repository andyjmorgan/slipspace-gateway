package resilience

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/contracts/events"
	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/proxy"
)

// attemptCaseNext is a stub downstream handler that consumes a queue
// of canned per-attempt outcomes AND, on every invocation, installs a
// terminal-publish closure on the AttemptBuffer attached to the
// request context. Mirrors what the real reporter does at OnComplete.
type attemptCaseNext struct {
	outcomes []attemptCaseOutcome
	idx      int
	captured *capturedTerminal
}

type attemptCaseOutcome struct {
	status int
	err    error
}

type capturedTerminal struct {
	policyRef string
	records   []events.AttemptRecord
	duration  int64
	status    int
	calls     int
}

func (m *attemptCaseNext) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m.idx >= len(m.outcomes) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	out := m.outcomes[m.idx]
	m.idx++
	if out.status == 0 {
		if buf, ok := w.(*proxy.BufferingResponseWriter); ok {
			buf.SetTransportError(out.err)
		}
	} else {
		w.WriteHeader(out.status)
	}

	if abuf := AttemptBufferFromContext(r.Context()); abuf != nil {
		abuf.SetTerminalPublish(func(durationMs int64, finalStatus int) {
			m.captured.calls++
			m.captured.policyRef = abuf.PolicyRef()
			m.captured.records = abuf.Drain()
			m.captured.duration = durationMs
			m.captured.status = finalStatus
		})
	}
}

func runOrchestrator(t *testing.T, pol *contractsres.ResilienceConfig, next *attemptCaseNext) *capturedTerminal {
	t.Helper()
	captured := &capturedTerminal{}
	next.captured = captured

	lookup := func(name string) *contractsres.ResilienceConfig {
		if name == pol.Name {
			return pol
		}
		return nil
	}

	handler := HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: pol.Name})
	handler.ServeHTTP(rec, req.WithContext(ctx))

	return captured
}

func TestFailover_RecordsAttempts_PrimaryFails_BackupSucceeds(t *testing.T) {
	pol := &contractsres.ResilienceConfig{
		Name:               "ha",
		Mode:               contractsres.ModeFailover,
		FailureStatusCodes: []int{503},
		Targets: []contractsres.ResilienceTarget{
			{Name: "primary", Provider: "openai", Order: 1},
			{Name: "backup", Provider: "anthropic", Order: 2},
		},
	}
	next := &attemptCaseNext{outcomes: []attemptCaseOutcome{
		{status: http.StatusServiceUnavailable},
		{status: http.StatusOK},
	}}

	captured := runOrchestrator(t, pol, next)

	if captured.calls != 1 {
		t.Fatalf("terminal closure fired %d times; want exactly 1", captured.calls)
	}
	if captured.policyRef != "ha" {
		t.Errorf("PolicyRef = %q; want ha", captured.policyRef)
	}
	if captured.status != http.StatusOK {
		t.Errorf("finalStatus = %d; want 200", captured.status)
	}
	if len(captured.records) != 2 {
		t.Fatalf("records = %d; want 2", len(captured.records))
	}
	if captured.records[0].Target != "primary" || captured.records[0].Outcome != attemptOutcomeFailureStatus {
		t.Errorf("record[0] = %+v; want target=primary outcome=failure_status", captured.records[0])
	}
	if captured.records[1].Target != "backup" || captured.records[1].Outcome != attemptOutcomeSuccess {
		t.Errorf("record[1] = %+v; want target=backup outcome=success", captured.records[1])
	}
}

func TestFailover_RecordsAttempts_TransportError_ThenSuccess(t *testing.T) {
	pol := &contractsres.ResilienceConfig{
		Name: "ha",
		Mode: contractsres.ModeFailover,
		Targets: []contractsres.ResilienceTarget{
			{Name: "primary", Provider: "openai", Order: 1},
			{Name: "backup", Provider: "anthropic", Order: 2},
		},
	}
	next := &attemptCaseNext{outcomes: []attemptCaseOutcome{
		{err: errors.New("EOF")},
		{status: http.StatusOK},
	}}

	captured := runOrchestrator(t, pol, next)

	if len(captured.records) != 2 {
		t.Fatalf("records = %d; want 2", len(captured.records))
	}
	if captured.records[0].Outcome != attemptOutcomeTransportError {
		t.Errorf("record[0].Outcome = %q; want transport_error", captured.records[0].Outcome)
	}
	if captured.records[0].Error == "" {
		t.Errorf("record[0].Error empty; want non-empty for transport error")
	}
	if captured.records[1].Outcome != attemptOutcomeSuccess {
		t.Errorf("record[1].Outcome = %q; want success", captured.records[1].Outcome)
	}
}

func TestFailover_RecordsAttempts_AllExhausted_FinalStatusFromOrchestrator(t *testing.T) {
	pol := &contractsres.ResilienceConfig{
		Name:               "ha",
		Mode:               contractsres.ModeFailover,
		FailureStatusCodes: []int{503},
		Targets: []contractsres.ResilienceTarget{
			{Name: "primary", Provider: "openai", Order: 1},
			{Name: "backup", Provider: "anthropic", Order: 2},
		},
	}
	next := &attemptCaseNext{outcomes: []attemptCaseOutcome{
		{status: http.StatusServiceUnavailable},
		{status: http.StatusServiceUnavailable},
	}}

	captured := runOrchestrator(t, pol, next)

	if len(captured.records) != 2 {
		t.Fatalf("records = %d; want 2", len(captured.records))
	}
	for i, rec := range captured.records {
		if rec.Outcome != attemptOutcomeFailureStatus {
			t.Errorf("record[%d].Outcome = %q; want failure_status", i, rec.Outcome)
		}
	}
	// finalStatus is the orchestrator's surfacing of the last status
	// (503 here) — the http.Error path writes this to the client.
	if captured.status != http.StatusServiceUnavailable {
		t.Errorf("finalStatus = %d; want 503", captured.status)
	}
}

func TestLoadBalance_RecordsAttempts_StrictWeights_NoReroll(t *testing.T) {
	pol := &contractsres.ResilienceConfig{
		Name:               "lb",
		Mode:               contractsres.ModeLoadBalance,
		FailureStatusCodes: []int{503},
		StrictWeights:      true,
		Targets: []contractsres.ResilienceTarget{
			{Name: "only", Provider: "openai", Weight: 100},
		},
	}
	next := &attemptCaseNext{outcomes: []attemptCaseOutcome{
		{status: http.StatusServiceUnavailable},
	}}

	captured := runOrchestrator(t, pol, next)

	if len(captured.records) != 1 {
		t.Fatalf("records = %d; want 1 (strict_weights = no re-roll)", len(captured.records))
	}
	if captured.records[0].Outcome != attemptOutcomeFailureStatus {
		t.Errorf("record[0].Outcome = %q; want failure_status", captured.records[0].Outcome)
	}
}

func TestLoadBalance_RecordsAttempts_LBWFFailoverThenSuccess(t *testing.T) {
	// Inject deterministic selection: first roll picks index 0, after
	// removal the only remaining target is at index 0 again.
	prev := randomIntN
	defer func() { randomIntN = prev }()
	randomIntN = func(int) int { return 0 }

	pol := &contractsres.ResilienceConfig{
		Name:               "lb",
		Mode:               contractsres.ModeLoadBalanceWithFailover,
		FailureStatusCodes: []int{503},
		Targets: []contractsres.ResilienceTarget{
			{Name: "a", Provider: "openai", Weight: 50},
			{Name: "b", Provider: "anthropic", Weight: 50},
		},
	}
	next := &attemptCaseNext{outcomes: []attemptCaseOutcome{
		{status: http.StatusServiceUnavailable},
		{status: http.StatusOK},
	}}

	captured := runOrchestrator(t, pol, next)

	if len(captured.records) != 2 {
		t.Fatalf("records = %d; want 2", len(captured.records))
	}
	if captured.records[0].Outcome != attemptOutcomeFailureStatus {
		t.Errorf("record[0].Outcome = %q; want failure_status", captured.records[0].Outcome)
	}
	if captured.records[1].Outcome != attemptOutcomeSuccess {
		t.Errorf("record[1].Outcome = %q; want success", captured.records[1].Outcome)
	}
}
