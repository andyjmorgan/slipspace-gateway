package rules_test

import (
	"context"
	"net/http"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func metersBundle(t *testing.T) (*observability.Meters, *sdkmetric.ManualReader) {
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

func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]bool {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			got[m.Name] = true
		}
	}
	return got
}

func TestEvaluator_EmitsMatchAndDurationMeters(t *testing.T) {
	t.Parallel()
	meters, reader := metersBundle(t)

	r := &contractsrules.RuleContract{
		Name:      "tag",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Tagged", "yes")},
	}
	e := rules.NewEvaluator(map[string][]*contractsrules.RuleContract{"dev": {r}}, 8, meters)
	state := rules.NewMutableState("openai", "chat_completions", nil, http.Header{})
	ctx, _ := rules.WithMatchBuffer(context.Background())
	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	got := collect(t, reader)
	for _, name := range []string{
		observability.MetricRuleMatchesTotal,
		observability.MetricRuleEvaluationDuration,
	} {
		if !got[name] {
			t.Errorf("missing metric %q", name)
		}
	}
}

func TestEvaluator_EmitsErrorMeterOnActionFailure(t *testing.T) {
	t.Parallel()
	meters, reader := metersBundle(t)

	r := &contractsrules.RuleContract{
		Name:      "bad",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{&contractsrules.ChangeUrlAction{NewURL: "://broken"}},
	}
	e := rules.NewEvaluator(map[string][]*contractsrules.RuleContract{"dev": {r}}, 8, meters)
	state := rules.NewMutableState("openai", "chat_completions", nil, http.Header{})
	ctx, _ := rules.WithMatchBuffer(context.Background())
	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	got := collect(t, reader)
	if !got[observability.MetricRuleErrorsTotal] {
		t.Errorf("missing %q after action failure", observability.MetricRuleErrorsTotal)
	}
}

func TestEvaluator_EmitsErrorMeterOnGroupDepthExceeded(t *testing.T) {
	t.Parallel()
	meters, reader := metersBundle(t)

	inner := &contractsrules.RuleGroup{
		LogicalOperator: contractsrules.LogicalAnd,
		Children:        []contractsrules.Condition{providerCondition("openai")},
	}
	outer := &contractsrules.RuleGroup{
		LogicalOperator: contractsrules.LogicalAnd,
		Children:        []contractsrules.Condition{inner},
	}
	r := &contractsrules.RuleContract{
		Name:      "deep",
		Condition: outer,
		Actions:   []contractsrules.Action{setHeaderAction("X", "v")},
	}
	e := rules.NewEvaluator(map[string][]*contractsrules.RuleContract{"dev": {r}}, 1, meters)
	state := rules.NewMutableState("openai", "chat_completions", nil, http.Header{})
	ctx, _ := rules.WithMatchBuffer(context.Background())
	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	got := collect(t, reader)
	if !got[observability.MetricRuleErrorsTotal] {
		t.Errorf("missing %q after depth exceeded", observability.MetricRuleErrorsTotal)
	}
}
