package resilience_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/resilience"
)

func TestNoneOrchestrator_FirstTarget(t *testing.T) {
	targets := []contractsres.ResilienceTarget{
		{Provider: "openai", Order: 1},
		{Provider: "anthropic", Order: 2},
	}

	var (
		invocations int
		gotTarget   contractsres.ResilienceTarget
	)
	exec := func(_ context.Context, target contractsres.ResilienceTarget) (*resilience.Result, error) {
		invocations++
		gotTarget = target
		return &resilience.Result{
			StatusCode: 200,
			Response:   io.NopCloser(strings.NewReader("ok")),
		}, nil
	}

	res, err := resilience.NoneOrchestrator{}.Execute(context.Background(), targets, exec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invocations != 1 {
		t.Errorf("invocations = %d, want 1", invocations)
	}
	if gotTarget.Provider != "openai" {
		t.Errorf("first target = %q, want openai", gotTarget.Provider)
	}
	if res == nil || res.StatusCode != 200 {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestNoneOrchestrator_NoTargets(t *testing.T) {
	var got contractsres.ResilienceTarget
	called := 0
	exec := func(_ context.Context, target contractsres.ResilienceTarget) (*resilience.Result, error) {
		called++
		got = target
		return &resilience.Result{StatusCode: 204}, nil
	}

	res, err := resilience.NoneOrchestrator{}.Execute(context.Background(), nil, exec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != 1 {
		t.Errorf("called = %d, want 1", called)
	}
	if !reflect.DeepEqual(got, contractsres.ResilienceTarget{}) {
		t.Errorf("expected zero-value target, got %+v", got)
	}
	if res == nil || res.StatusCode != 204 {
		t.Errorf("result = %+v", res)
	}
}

func TestNoneOrchestrator_PropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	exec := func(_ context.Context, _ contractsres.ResilienceTarget) (*resilience.Result, error) {
		return nil, sentinel
	}

	res, err := resilience.NoneOrchestrator{}.Execute(context.Background(), nil, exec)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want %v", err, sentinel)
	}
	if res != nil {
		t.Errorf("expected nil result, got %+v", res)
	}
}

func TestNoneOrchestrator_SatisfiesInterface(t *testing.T) {
	var _ resilience.Orchestrator = resilience.NoneOrchestrator{}
}
