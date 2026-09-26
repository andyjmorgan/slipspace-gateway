package resilience_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	resiliencemw "github.com/andyjmorgan/slipspace-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/proxy"
)

// slowThenFast is a downstream stub whose first attempt blocks until the
// attempt context ends (the forwarder shape: ReverseProxy's ErrorHandler then
// records the ctx error on the buffer) and whose later attempts commit 200.
// deadlines records the per-attempt context deadline so tests can assert the
// orchestrator stamped (or did not stamp) an attempt timeout.
type slowThenFast struct {
	seen      []string
	deadlines []time.Duration
	// recordErr mirrors the forwarder: when true the stub calls
	// SetTransportError with the ctx error, when false it returns quietly so
	// the orchestrator's own markAttemptTimeout has to do the accounting.
	recordErr bool
}

func (s *slowThenFast) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if state := rules.MutableStateFromContext(r.Context()); state != nil {
		s.seen = append(s.seen, state.Provider)
	}
	if dl, ok := r.Context().Deadline(); ok {
		s.deadlines = append(s.deadlines, time.Until(dl).Round(time.Second))
	} else {
		s.deadlines = append(s.deadlines, 0)
	}
	if len(s.seen) == 1 {
		<-r.Context().Done()
		if s.recordErr {
			if buf, ok := w.(*proxy.BufferingResponseWriter); ok {
				buf.SetTransportError(r.Context().Err())
			}
		}
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func timeoutTargets(primaryTimeout int) []contractsres.ResilienceTarget {
	return []contractsres.ResilienceTarget{
		{Name: "slow", Provider: "slow", Order: 1, TimeoutSeconds: primaryTimeout, Actions: changeTo("slow")},
		{Name: "fast", Provider: "fast", Order: 2, Actions: changeTo("fast")},
	}
}

func runPolicy(t *testing.T, pol *contractsres.ResilienceConfig, next http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{pol.Name: pol})
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: pol.Name})
	h.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestFailover_PerTargetTimeout_FailsOverToNextTarget(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(timeoutTargets(1)...)
	next := &slowThenFast{recordErr: true}

	start := time.Now()
	rec := runPolicy(t, pol, next)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 via the fast backup", rec.Code)
	}
	if len(next.seen) != 2 || next.seen[0] != "slow" || next.seen[1] != "fast" {
		t.Errorf("attempt order = %v; want [slow fast]", next.seen)
	}
	if elapsed < 900*time.Millisecond || elapsed > 5*time.Second {
		t.Errorf("elapsed = %s; the 1s per-target timeout should have bounded the slow primary", elapsed)
	}
	if next.deadlines[0] != time.Second {
		t.Errorf("primary attempt deadline = %s; want ~1s", next.deadlines[0])
	}
	if next.deadlines[1] != 0 {
		t.Errorf("backup attempt deadline = %s; want none (target and policy both unset)", next.deadlines[1])
	}
}

func TestFailover_TimeoutWithoutDownstreamError_StillCountsAsFailure(t *testing.T) {
	t.Parallel()
	// The downstream returns quietly on ctx.Done() without recording a
	// transport error. Without markAttemptTimeout the uncommitted, statusless
	// buffer would read as "committed nothing to retry" and the client would
	// see an empty 200 — the orchestrator must convert the deadline into a
	// retryable transport error itself.
	pol := failoverPolicy(timeoutTargets(1)...)
	next := &slowThenFast{recordErr: false}

	rec := runPolicy(t, pol, next)

	if rec.Code != http.StatusOK || rec.Body.String() != `{"ok":true}` {
		t.Fatalf("status/body = %d %q; want the backup's 200 body", rec.Code, rec.Body.String())
	}
	if len(next.seen) != 2 {
		t.Errorf("attempts = %d; want 2 (timed-out primary + backup)", len(next.seen))
	}
}

func TestFailover_PolicyTimeout_AppliesWhenTargetUnset(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(timeoutTargets(0)...)
	pol.TimeoutSeconds = 1
	next := &slowThenFast{recordErr: true}

	rec := runPolicy(t, pol, next)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if len(next.deadlines) != 2 || next.deadlines[0] != time.Second || next.deadlines[1] != time.Second {
		t.Errorf("deadlines = %v; want the 1s policy bound on every attempt", next.deadlines)
	}
}

func TestFailover_TargetTimeout_OverridesPolicyTimeout(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(timeoutTargets(1)...)
	pol.TimeoutSeconds = 30
	next := &slowThenFast{recordErr: true}

	start := time.Now()
	rec := runPolicy(t, pol, next)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if elapsed > 5*time.Second {
		t.Errorf("elapsed = %s; the 1s target override should win over the 30s policy value", elapsed)
	}
	if len(next.deadlines) != 2 || next.deadlines[0] != time.Second || next.deadlines[1] != 30*time.Second {
		t.Errorf("deadlines = %v; want [1s 30s]", next.deadlines)
	}
}

func TestFailover_AllTargetsTimeOut_ClientSees502(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "a", Provider: "a", Order: 1, TimeoutSeconds: 1, Actions: changeTo("a")},
		contractsres.ResilienceTarget{Name: "b", Provider: "b", Order: 2, TimeoutSeconds: 1, Actions: changeTo("b")},
	)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})

	rec := runPolicy(t, pol, next)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d; want 502 (every attempt timed out → transport-error fallback)", rec.Code)
	}
}

func TestFailover_NoTimeoutConfigured_NoDeadlineStamped(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "openai", Provider: "openai", Order: 1, Actions: changeTo("openai")},
	)
	var hasDeadline bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasDeadline = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	})

	rec := runPolicy(t, pol, next)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if hasDeadline {
		t.Errorf("attempt context carries a deadline although neither target nor policy set timeout_seconds")
	}
}

func TestFailover_ClientCancelDuringAttempt_NotTreatedAsTimeout(t *testing.T) {
	t.Parallel()
	// The parent (request) context is cancelled while the attempt runs. The
	// attempt ctx reports Canceled, not DeadlineExceeded, so the orchestrator
	// must not fabricate a transport error and must not fail over to a second
	// target — the client has gone.
	pol := failoverPolicy(timeoutTargets(30)...)
	attempts := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		<-r.Context().Done()
	})
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{pol.Name: pol})
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx, cancel := context.WithCancel(rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: pol.Name}))
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	h.ServeHTTP(rec, req.WithContext(ctx))

	if attempts != 1 {
		t.Errorf("attempts = %d; want 1 (a departed client must not trigger failover)", attempts)
	}
}

func TestLoadBalance_PerTargetTimeout_RerollsOntoHealthyTarget(t *testing.T) {
	t.Parallel()
	pol := &contractsres.ResilienceConfig{
		Name: "lb",
		Mode: contractsres.ModeLoadBalance,
		Targets: []contractsres.ResilienceTarget{
			{Name: "a", Provider: "a", Weight: 1, TimeoutSeconds: 1, Actions: changeTo("a")},
			{Name: "b", Provider: "b", Weight: 1, TimeoutSeconds: 1, Actions: changeTo("b")},
		},
	}
	// Two attempts: the first (whichever the roll picks) blocks until its
	// 1s deadline by construction of the stub, so the pool shrinks and the
	// re-roll lands on the remaining target, which commits.
	next := &slowThenFast{recordErr: true}

	start := time.Now()
	rec := runPolicy(t, pol, next)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 after re-roll", rec.Code)
	}
	if len(next.seen) != 2 {
		t.Errorf("attempts = %d; want 2", len(next.seen))
	}
	if elapsed > 5*time.Second {
		t.Errorf("elapsed = %s; the slow attempt should have been bounded by its timeout", elapsed)
	}
}

func TestSingleTarget_Timeout_BoundsTheOneAttempt(t *testing.T) {
	t.Parallel()
	pol := &contractsres.ResilienceConfig{
		Name:           "single",
		Mode:           contractsres.ModeNone,
		TimeoutSeconds: 1,
		Targets:        []contractsres.ResilienceTarget{{Name: "only", Provider: "only", Order: 1}},
	}
	var deadline time.Duration
	var ctxErr error
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dl, ok := r.Context().Deadline(); ok {
			deadline = time.Until(dl).Round(time.Second)
		}
		<-r.Context().Done()
		ctxErr = r.Context().Err()
		w.WriteHeader(http.StatusGatewayTimeout)
	})

	rec := runPolicy(t, pol, next)

	if deadline != time.Second {
		t.Errorf("single-target attempt deadline = %s; want 1s from the policy", deadline)
	}
	if !errors.Is(ctxErr, context.DeadlineExceeded) {
		t.Errorf("ctx err = %v; want DeadlineExceeded", ctxErr)
	}
	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d; single-target has no failover, the downstream's own status must pass through", rec.Code)
	}
}

// modelCapture records the PathParams["model"] the orchestrator's attempt
// state carries — changeModelName writes it even with no typed body, which is
// the observable the ModelRewrite tests key on.
type modelCapture struct {
	models []string
}

func (m *modelCapture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	state := rules.MutableStateFromContext(r.Context())
	if state != nil {
		m.models = append(m.models, state.PathParams["model"])
	}
	w.WriteHeader(http.StatusOK)
}

func TestModelRewrite_ScalarAppliedWhenNoActions(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "openai", Provider: "openai", Order: 1, ModelRewrite: "gpt-4o-mini-rewritten"},
	)
	next := &modelCapture{}

	rec := runPolicy(t, pol, next)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(next.models) != 1 || next.models[0] != "gpt-4o-mini-rewritten" {
		t.Errorf("model seen = %v; want the scalar model_rewrite applied", next.models)
	}
}

func TestModelRewrite_ScalarAppliedAlongsideNonModelActions(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "anthropic", Provider: "anthropic", Order: 1, ModelRewrite: "claude-rewritten", Actions: changeTo("anthropic")},
	)
	next := &modelCapture{}
	cap := &stateCapture{}
	both := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.handler().ServeHTTP(httptest.NewRecorder(), r)
		next.ServeHTTP(w, r)
	})

	rec := runPolicy(t, pol, both)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if cap.state == nil || cap.state.Provider != "anthropic" {
		t.Errorf("changeProvider action must still apply; state = %+v", cap.state)
	}
	if len(next.models) != 1 || next.models[0] != "claude-rewritten" {
		t.Errorf("model seen = %v; want the scalar applied when Actions has no changeModelName", next.models)
	}
}

func TestModelRewrite_ActionsWinWhenBothPresent(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(
		contractsres.ResilienceTarget{
			Name: "openai", Provider: "openai", Order: 1,
			ModelRewrite: "from-scalar",
			Actions: []contractsrules.Action{
				&contractsrules.ChangeProviderAction{NewProvider: "openai"},
				&contractsrules.ChangeModelNameAction{NewModelName: "from-actions"},
			},
		},
	)
	next := &modelCapture{}

	rec := runPolicy(t, pol, next)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(next.models) != 1 || next.models[0] != "from-actions" {
		t.Errorf("model seen = %v; Actions must win over the scalar for the field it covers", next.models)
	}
}

func TestModelRewrite_SingleTargetModeAppliesScalar(t *testing.T) {
	t.Parallel()
	pol := &contractsres.ResilienceConfig{
		Name:    "single",
		Mode:    contractsres.ModeNone,
		Targets: []contractsres.ResilienceTarget{{Name: "only", Provider: "only", Order: 1, ModelRewrite: "single-rewritten"}},
	}
	next := &modelCapture{}

	rec := runPolicy(t, pol, next)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(next.models) != 1 || next.models[0] != "single-rewritten" {
		t.Errorf("model seen = %v; ModeNone must honour the scalar too", next.models)
	}
}

func TestModelRewrite_DoesNotMutateTargetActions(t *testing.T) {
	t.Parallel()
	acts := changeTo("openai")
	target := contractsres.ResilienceTarget{Name: "openai", Provider: "openai", Order: 1, ModelRewrite: "x", Actions: acts}
	pol := failoverPolicy(target)
	next := &modelCapture{}

	_ = runPolicy(t, pol, next)

	if len(acts) != 1 || len(pol.Targets[0].Actions) != 1 {
		t.Errorf("effectiveTargetActions must not append onto the target's own Actions slice; len=%d/%d", len(acts), len(pol.Targets[0].Actions))
	}
}
