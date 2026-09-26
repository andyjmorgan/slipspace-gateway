package resilience_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	resiliencemw "github.com/andyjmorgan/slipspace-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
	"github.com/andyjmorgan/slipspace-gateway/internal/selection"
)

// These cases pin the precedence fix for GitHub issue #294: a rule that ran
// changeProvider (state.ProviderOverridden) beats the binding-derived policy.
// The orchestrator must collapse to one attempt on the rule's provider and
// never re-apply the binding's own provider switch over it.

// twoTargetGroup is the issue's shape: a load-balance / failover group over
// two providers, each target carrying the internal provider switch selection
// synthesises.
func twoTargetGroup(mode contractsres.ResilienceMode) *contractsres.ResilienceConfig {
	return &contractsres.ResilienceConfig{
		Name:               "qwen-load-balance",
		Mode:               mode,
		FailureStatusCodes: []int{502, 503, 504, 404},
		Targets: []contractsres.ResilienceTarget{
			{Name: "qwen-ollama", Provider: "qwen-ollama", Order: 1, Weight: 1, Actions: changeTo("qwen-ollama")},
			{Name: "qwen-ollama-standalone", Provider: "qwen-ollama-standalone", Order: 2, Weight: 1, Actions: changeTo("qwen-ollama-standalone")},
		},
	}
}

// ruleOverriddenState is the post-rules baseline after a changeProvider →
// gpt-oss fired on a group-bound request: selection seeded Provider="" and
// PolicyRef=<group>, the rule wrote both Provider and the flag.
func ruleOverriddenState(policy string) *rules.MutableState {
	return &rules.MutableState{PolicyRef: policy, Provider: "gpt-oss", ProviderOverridden: true}
}

func resilienceMeters(t *testing.T) (*observability.Meters, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	m, err := observability.NewMeters(mp.Meter(observability.MeterName))
	if err != nil {
		t.Fatalf("NewMeters: %v", err)
	}
	return m, reader
}

// counterSum returns the summed data points of the named Int64 counter whose
// attribute set carries every want attribute, and whether the metric was
// emitted at all.
func counterSum(t *testing.T, reader *sdkmetric.ManualReader, name string, want ...attribute.KeyValue) (int64, bool) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var total int64
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			found = true
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s: data is %T, want Sum[int64]", name, m.Data)
			}
		points:
			for _, dp := range sum.DataPoints {
				for _, kv := range want {
					if v, ok := dp.Attributes.Value(kv.Key); !ok || v != kv.Value {
						continue points
					}
				}
				total += dp.Value
			}
		}
	}
	return total, found
}

func runOverride(t *testing.T, pol *contractsres.ResilienceConfig, state *rules.MutableState, meters *observability.Meters, next http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	h := resiliencemw.HTTPHandler(nil, nil, meters, next)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), state)
	ctx = resiliencemw.WithResilienceConfig(ctx, pol)
	h.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestRuleOverride_FailoverGroup_CollapsesToRuleProvider(t *testing.T) {
	t.Parallel()
	pol := twoTargetGroup(contractsres.ModeFailover)
	meters, reader := resilienceMeters(t)
	// A 404 is in the group's failure set: had the group run, the
	// orchestrator would have failed over to the second target.
	next := &mockNext{outcomes: []attemptOutcome{
		{status: http.StatusNotFound, body: `{"error":"model not found"}`},
		{status: http.StatusOK, body: `{"ok":true}`},
	}}
	state := ruleOverriddenState(pol.Name)

	rec := runOverride(t, pol, state, meters, next)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want the single attempt's 404 surfaced (no failover)", rec.Code)
	}
	if len(next.seen) != 1 || next.seen[0] != "gpt-oss" {
		t.Fatalf("providers seen = %v; want exactly one attempt on the rule's provider gpt-oss", next.seen)
	}
	if state.PolicyRef != selection.RuleOverridePolicyPrefix+"gpt-oss" {
		t.Errorf("PolicyRef = %q; want the collapsed rule:gpt-oss handle", state.PolicyRef)
	}
	got, found := counterSum(t, reader, observability.MetricResilienceOutcomeTotal,
		attribute.String("policy", "qwen-load-balance"),
		attribute.String("outcome", "rule_override"),
	)
	if !found || got != 1 {
		t.Errorf("outcome.total{policy=qwen-load-balance,outcome=rule_override} = %d (found=%v); want 1", got, found)
	}
	if _, found := counterSum(t, reader, observability.MetricResilienceAttemptsTotal); found {
		t.Error("attempts.total was emitted; the bypassed group must record no attempt")
	}
}

func TestRuleOverride_LoadBalanceGroup_CollapsesToRuleProvider(t *testing.T) {
	t.Parallel()
	pol := twoTargetGroup(contractsres.ModeLoadBalance)
	next := &mockNext{outcomes: []attemptOutcome{{status: http.StatusOK, body: `{"ok":true}`}}}

	rec := runOverride(t, pol, ruleOverriddenState(pol.Name), nil, next)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(next.seen) != 1 || next.seen[0] != "gpt-oss" {
		t.Errorf("providers seen = %v; want one attempt on gpt-oss, not a load-balance pick", next.seen)
	}
}

func TestRuleOverride_SingleBinding_RuleProviderWins(t *testing.T) {
	t.Parallel()
	// A single-provider binding with an alias: its ModeNone policy carries a
	// provider switch + changeModelName. The rule's provider must win and the
	// binding's alias must not be applied — it belongs to the abandoned target.
	pol := &contractsres.ResilienceConfig{
		Name: "binding:foundry",
		Mode: contractsres.ModeNone,
		Targets: []contractsres.ResilienceTarget{{
			Name: "foundry", Provider: "foundry", Order: 1,
			Actions: selection.ProviderSwitchActions("foundry", "deployment-x"),
		}},
	}
	next := &modelCapture{}
	state := &rules.MutableState{
		PolicyRef: pol.Name, Provider: "gpt-oss", ProviderOverridden: true,
		PathParams: map[string]string{"model": "client-model"},
	}
	var seenProvider string
	capture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenProvider = rules.MutableStateFromContext(r.Context()).Provider
		next.ServeHTTP(w, r)
	})

	rec := runOverride(t, pol, state, nil, capture)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if seenProvider != "gpt-oss" {
		t.Errorf("provider = %q; want the rule's gpt-oss, not the binding's foundry", seenProvider)
	}
	if len(next.models) != 1 || next.models[0] != "client-model" {
		t.Errorf("model = %v; the abandoned binding's alias must not be applied", next.models)
	}
}

func TestRuleOverride_ModelRewriteOnly_GroupStillRuns(t *testing.T) {
	t.Parallel()
	// changeModelName alone leaves ProviderOverridden clear: the group runs
	// as today, and a retryable first attempt fails over to the second target.
	pol := twoTargetGroup(contractsres.ModeFailover)
	next := &mockNext{outcomes: []attemptOutcome{
		{status: http.StatusNotFound, body: `{"error":"model not found"}`},
		{status: http.StatusOK, body: `{"ok":true}`},
	}}
	state := &rules.MutableState{PolicyRef: pol.Name, Provider: "", PathParams: map[string]string{"model": "gpt-oss:20b"}}

	rec := runOverride(t, pol, state, nil, next)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 via failover", rec.Code)
	}
	if len(next.seen) != 2 || next.seen[0] != "qwen-ollama" || next.seen[1] != "qwen-ollama-standalone" {
		t.Errorf("providers seen = %v; want the group's two targets in order", next.seen)
	}
	if state.PolicyRef != pol.Name {
		t.Errorf("PolicyRef = %q; want the group name untouched", state.PolicyRef)
	}
}

func TestRuleOverride_FlagWithoutProvider_GroupStillRuns(t *testing.T) {
	t.Parallel()
	// Defensive: the flag alone, with no provider to collapse onto, cannot
	// bypass the group — there is nothing to route to.
	pol := twoTargetGroup(contractsres.ModeFailover)
	next := &mockNext{outcomes: []attemptOutcome{{status: http.StatusOK, body: `{"ok":true}`}}}
	state := &rules.MutableState{PolicyRef: pol.Name, Provider: "", ProviderOverridden: true}

	rec := runOverride(t, pol, state, nil, next)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(next.seen) != 1 || next.seen[0] != "qwen-ollama" {
		t.Errorf("providers seen = %v; want the group's first target", next.seen)
	}
}

func TestRuleOverride_NilMeters_DoesNotPanic(t *testing.T) {
	t.Parallel()
	pol := twoTargetGroup(contractsres.ModeFailover)
	next := &mockNext{outcomes: []attemptOutcome{{status: http.StatusOK, body: `{"ok":true}`}}}

	rec := runOverride(t, pol, ruleOverriddenState(pol.Name), nil, next)

	if rec.Code != http.StatusOK || len(next.seen) != 1 || next.seen[0] != "gpt-oss" {
		t.Errorf("status=%d seen=%v; want 200 on gpt-oss with meters nil", rec.Code, next.seen)
	}
}
