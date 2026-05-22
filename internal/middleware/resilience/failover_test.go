package resilience_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	contractsres "github.com/andyjmorgan/sluice-gateway/contracts/resilience"
	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	resiliencemw "github.com/andyjmorgan/sluice-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
)

// attemptOutcome is the canned response a mockNext returns on a
// single attempt. status==0 + err means "simulate a transport error
// — no status, SetTransportError on the wrapping buffer."
type attemptOutcome struct {
	status int
	err    error
	body   string
}

// mockNext is a stub downstream handler that consumes a queue of
// canned outcomes, recording each attempt's observed Provider so
// tests can assert ordering.
type mockNext struct {
	outcomes []attemptOutcome
	idx      int
	seen     []string
}

func (m *mockNext) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	state := rules.MutableStateFromContext(r.Context())
	if state != nil {
		m.seen = append(m.seen, state.Provider)
	}
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
		return
	}
	w.WriteHeader(out.status)
	if out.body != "" {
		_, _ = w.Write([]byte(out.body))
	}
}

func failoverPolicy(targets ...contractsres.ResilienceTarget) *contractsres.ResilienceConfig {
	return &contractsres.ResilienceConfig{
		Name:               "ha",
		Mode:               contractsres.ModeFailover,
		FailureStatusCodes: []int{500, 502, 503, 504},
		Targets:            targets,
	}
}

func changeTo(provider string) []contractsrules.Action {
	return []contractsrules.Action{&contractsrules.ChangeProviderAction{NewProvider: provider}}
}

func TestFailover_PrimaryReturns503_BackupServes200(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "openai", Provider: "openai", Order: 1, Actions: changeTo("openai")},
		contractsres.ResilienceTarget{Name: "anthropic", Provider: "anthropic", Order: 2, Actions: changeTo("anthropic")},
	)
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{outcomes: []attemptOutcome{
		{status: http.StatusServiceUnavailable, body: "down"},
		{status: http.StatusOK, body: `{"ok":true}`},
	}}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha", Provider: "openai"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Errorf("client status = %d; want 200", rec.Code)
	}
	if rec.Body.String() != `{"ok":true}` {
		t.Errorf("client body = %q", rec.Body.String())
	}
	if len(next.seen) != 2 {
		t.Errorf("attempts = %d; want 2 (seen=%v)", len(next.seen), next.seen)
	}
	if next.seen[0] != "openai" || next.seen[1] != "anthropic" {
		t.Errorf("attempt order = %v; want [openai anthropic]", next.seen)
	}
}

func TestFailover_AllTargetsReturn503_ClientSeesLastStatus(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "openai", Provider: "openai", Order: 1, Actions: changeTo("openai")},
		contractsres.ResilienceTarget{Name: "anthropic", Provider: "anthropic", Order: 2, Actions: changeTo("anthropic")},
	)
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{outcomes: []attemptOutcome{
		{status: http.StatusServiceUnavailable},
		{status: http.StatusBadGateway},
	}}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("client status = %d; want last attempt's 502", rec.Code)
	}
	if len(next.seen) != 2 {
		t.Errorf("attempts = %d; want 2", len(next.seen))
	}
}

func TestFailover_4xxNotInRetrySet_NotRetried(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "openai", Provider: "openai", Order: 1, Actions: changeTo("openai")},
		contractsres.ResilienceTarget{Name: "anthropic", Provider: "anthropic", Order: 2, Actions: changeTo("anthropic")},
	)
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{outcomes: []attemptOutcome{
		{status: http.StatusForbidden, body: "denied"},
	}}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusForbidden {
		t.Errorf("client status = %d; want 403 (not retried)", rec.Code)
	}
	if len(next.seen) != 1 {
		t.Errorf("attempts = %d; want 1 (4xx not in retry set)", len(next.seen))
	}
}

func TestFailover_TransportErrorOnPrimary_BackupSucceeds(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "openai", Provider: "openai", Order: 1, Actions: changeTo("openai")},
		contractsres.ResilienceTarget{Name: "anthropic", Provider: "anthropic", Order: 2, Actions: changeTo("anthropic")},
	)
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{outcomes: []attemptOutcome{
		{status: 0, err: errors.New("dial tcp: connection refused")},
		{status: http.StatusOK, body: `{"ok":true}`},
	}}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Errorf("client status = %d; want 200 (recovered)", rec.Code)
	}
	if len(next.seen) != 2 {
		t.Errorf("attempts = %d; want 2", len(next.seen))
	}
}

func TestFailover_AllTransportErrors_ClientSees502(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "openai", Provider: "openai", Order: 1, Actions: changeTo("openai")},
		contractsres.ResilienceTarget{Name: "anthropic", Provider: "anthropic", Order: 2, Actions: changeTo("anthropic")},
	)
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{outcomes: []attemptOutcome{
		{status: 0, err: errors.New("connection refused")},
		{status: 0, err: errors.New("connection refused")},
	}}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("client status = %d; want 502 (all transport errors → fallback 502)", rec.Code)
	}
}

func TestFailover_SortsByOrderAscending(t *testing.T) {
	t.Parallel()
	// Declaration order intentionally reversed; orchestrator should
	// attempt them in Order order (1 then 2 then 3), not declaration
	// order.
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "third", Provider: "third", Order: 3, Actions: changeTo("third")},
		contractsres.ResilienceTarget{Name: "first", Provider: "first", Order: 1, Actions: changeTo("first")},
		contractsres.ResilienceTarget{Name: "second", Provider: "second", Order: 2, Actions: changeTo("second")},
	)
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{outcomes: []attemptOutcome{
		{status: 503},
		{status: 503},
		{status: 200},
	}}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	want := []string{"first", "second", "third"}
	if len(next.seen) != 3 {
		t.Fatalf("attempts = %d; want 3", len(next.seen))
	}
	for i, w := range want {
		if next.seen[i] != w {
			t.Errorf("attempt %d = %q; want %q (full=%v)", i, next.seen[i], w, next.seen)
		}
	}
}

func TestFailover_PerTargetFailureStatusCodes_OverridesPolicy(t *testing.T) {
	t.Parallel()
	// Policy retries on 5xx; first target overrides to *also* retry
	// on 429. A 429 from the first target should fail over, even
	// though policy alone wouldn't.
	pol := &contractsres.ResilienceConfig{
		Name:               "tiered",
		Mode:               contractsres.ModeFailover,
		FailureStatusCodes: []int{503},
		Targets: []contractsres.ResilienceTarget{
			{
				Name:               "rate-limited-primary",
				Provider:           "openai",
				Order:              1,
				FailureStatusCodes: []int{429, 503},
				Actions:            changeTo("openai"),
			},
			{
				Name:     "backup",
				Provider: "anthropic",
				Order:    2,
				Actions:  changeTo("anthropic"),
			},
		},
	}
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"tiered": pol})
	next := &mockNext{outcomes: []attemptOutcome{
		{status: 429, body: "rate limited"},
		{status: 200, body: `{"ok":true}`},
	}}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "tiered"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Errorf("client status = %d; want 200 (target-override retried 429)", rec.Code)
	}
	if len(next.seen) != 2 {
		t.Errorf("attempts = %d; want 2", len(next.seen))
	}
}

func TestFailover_StateIsolationBetweenAttempts(t *testing.T) {
	t.Parallel()
	// Each attempt must see its OWN target's actions applied to a
	// CLONE of the baseline — not the prior attempt's mutated state.
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "a", Provider: "openai", Order: 1, Actions: changeTo("openai-redirect")},
		contractsres.ResilienceTarget{Name: "b", Provider: "anthropic", Order: 2, Actions: changeTo("anthropic-redirect")},
	)
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{outcomes: []attemptOutcome{
		{status: 503},
		{status: 200},
	}}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	baseline := &rules.MutableState{PolicyRef: "ha", Provider: "rules-set-this"}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), baseline)
	h.ServeHTTP(rec, req.WithContext(ctx))

	if next.seen[0] != "openai-redirect" || next.seen[1] != "anthropic-redirect" {
		t.Errorf("attempt providers = %v; want [openai-redirect anthropic-redirect]", next.seen)
	}
	// Original baseline must not have been mutated by either target.
	if baseline.Provider != "rules-set-this" {
		t.Errorf("baseline mutated through clone: Provider = %q", baseline.Provider)
	}
}

func TestFailover_ApplyActionErrorOnAttempt_Returns500(t *testing.T) {
	t.Parallel()
	// A broken action on one of the targets surfaces as a 500 to
	// the client — the orchestrator does not silently skip a
	// misconfigured target.
	pol := failoverPolicy(
		contractsres.ResilienceTarget{
			Name:     "broken",
			Provider: "openai",
			Order:    1,
			Actions:  []contractsrules.Action{&contractsrules.ChangeProviderAction{NewProvider: ""}},
		},
		contractsres.ResilienceTarget{Name: "backup", Provider: "anthropic", Order: 2, Actions: changeTo("anthropic")},
	)
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{outcomes: []attemptOutcome{{status: 200}}}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500 on apply failure", rec.Code)
	}
	if len(next.seen) != 0 {
		t.Errorf("downstream invoked despite apply failure: %v", next.seen)
	}
}

func TestFailover_NoBodyOnContext_DoesNotRestore(t *testing.T) {
	t.Parallel()
	// When bodycapture did not run (e.g., admin route), the
	// orchestrator must not panic on the nil raw-body path — it
	// just skips body restore and lets the underlying r.Body
	// flow through as-is.
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "primary", Provider: "openai", Order: 1, Actions: changeTo("openai")},
	)
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{outcomes: []attemptOutcome{{status: 200, body: "ok"}}}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 (no body to restore is not an error)", rec.Code)
	}
}

func TestFailover_CBOpenTarget_SkippedSilently(t *testing.T) {
	t.Parallel()
	// Pre-tripped store: t1 is Open, t2 is Closed. The orchestrator
	// must skip t1 without an attempt and serve from t2.
	store := resiliencemw.NewInMemoryBreakerStore(nil)
	cb := &contractsres.CircuitBreakerConfig{
		Enabled:                  true,
		FailureThreshold:         1,
		SamplingDurationSeconds:  60,
		CooldownSeconds:          60,
		MinimumThroughput:        1,
		HalfOpenSuccessThreshold: 1,
	}
	store.RecordFailure("ha", "t1", cb)

	pol := &contractsres.ResilienceConfig{
		Name:               "ha",
		Mode:               contractsres.ModeFailover,
		FailureStatusCodes: []int{503},
		CircuitBreaker:     cb,
		Targets: []contractsres.ResilienceTarget{
			{Name: "t1", Provider: "openai", Order: 1, Actions: changeTo("openai")},
			{Name: "t2", Provider: "anthropic", Order: 2, Actions: changeTo("anthropic")},
		},
	}
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{outcomes: []attemptOutcome{{status: http.StatusOK, body: "ok"}}}
	h := resiliencemw.HTTPHandler(lookup, store, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 (CB-blocked t1, serve from t2)", rec.Code)
	}
	if len(next.seen) != 1 || next.seen[0] != "anthropic" {
		t.Errorf("attempts seen = %v; want [anthropic] (t1 skipped)", next.seen)
	}
}

func TestFailover_AllCBOpen_ReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()
	// Every target's breaker is Open → orchestrator writes 503.
	store := resiliencemw.NewInMemoryBreakerStore(nil)
	cb := &contractsres.CircuitBreakerConfig{
		Enabled:           true,
		FailureThreshold:  1,
		MinimumThroughput: 1,
		CooldownSeconds:   60,
	}
	store.RecordFailure("ha", "t1", cb)
	store.RecordFailure("ha", "t2", cb)

	pol := &contractsres.ResilienceConfig{
		Name:           "ha",
		Mode:           contractsres.ModeFailover,
		CircuitBreaker: cb,
		Targets: []contractsres.ResilienceTarget{
			{Name: "t1", Provider: "openai", Order: 1, Actions: changeTo("openai")},
			{Name: "t2", Provider: "anthropic", Order: 2, Actions: changeTo("anthropic")},
		},
	}
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{}
	h := resiliencemw.HTTPHandler(lookup, store, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 (every target CB-blocked)", rec.Code)
	}
	if len(next.seen) != 0 {
		t.Errorf("attempts seen = %v; want none (all CB-blocked)", next.seen)
	}
}

func TestFailover_RecordsCBSuccessOnCommit(t *testing.T) {
	t.Parallel()
	// A committed 200 records a success, keeping the breaker Closed.
	store := resiliencemw.NewInMemoryBreakerStore(nil)
	cb := &contractsres.CircuitBreakerConfig{
		Enabled:                 true,
		FailureThreshold:        3,
		SamplingDurationSeconds: 60,
		MinimumThroughput:       3,
	}
	pol := &contractsres.ResilienceConfig{
		Name:           "ha",
		Mode:           contractsres.ModeFailover,
		CircuitBreaker: cb,
		Targets: []contractsres.ResilienceTarget{
			{Name: "t1", Provider: "openai", Order: 1, Actions: changeTo("openai")},
		},
	}
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{outcomes: []attemptOutcome{{status: http.StatusOK, body: "ok"}}}
	h := resiliencemw.HTTPHandler(lookup, store, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if store.State("ha", "t1") != resiliencemw.StateClosed {
		t.Errorf("state after success = %v; want Closed", store.State("ha", "t1"))
	}
}

func TestFailover_PolicyDefaultRetrySet_500_502_503_504(t *testing.T) {
	t.Parallel()
	// When neither policy nor target declares failure_status_codes,
	// the orchestrator defaults to 5xx — 500/502/503/504. 501 is
	// 5xx but not in the default; verify it's NOT retried by
	// default.
	pol := &contractsres.ResilienceConfig{
		Name: "ha",
		Mode: contractsres.ModeFailover,
		Targets: []contractsres.ResilienceTarget{
			{Name: "a", Provider: "a", Order: 1, Actions: changeTo("a")},
			{Name: "b", Provider: "b", Order: 2, Actions: changeTo("b")},
		},
	}
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{outcomes: []attemptOutcome{
		{status: http.StatusNotImplemented, body: "501"},
	}}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("client status = %d; want 501 (not in default retry set, no retry)", rec.Code)
	}
	if len(next.seen) != 1 {
		t.Errorf("attempts = %d; want 1 (501 not retried)", len(next.seen))
	}
}
