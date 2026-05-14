package rules_test

import (
	"context"
	"net/http"
	"testing"

	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
)

func TestEvaluator_NoOp_AlwaysPassthrough(t *testing.T) {
	configured := []contractsrules.RuleContract{
		{
			Name:      "rule-1",
			Priority:  1,
			Condition: &contractsrules.ProviderCondition{Type: "provider", Operator: contractsrules.EnumEquals, ExpectedProvider: "openai"},
			Actions: []contractsrules.Action{
				&contractsrules.ReturnStatusCodeAction{Type: "returnStatusCode", StatusCode: 429, BodyType: contractsrules.StatusBodyText},
			},
			Behavior: contractsrules.BehaviorExit,
		},
	}

	tests := []struct {
		name string
		gw   rules.GatewayContext
	}{
		{
			name: "empty context",
			gw:   rules.GatewayContext{},
		},
		{
			name: "matching context",
			gw: rules.GatewayContext{
				Provider: "openai",
				Endpoint: "chat_completions",
				Model:    "gpt-4o",
				Headers:  http.Header{"X-Tenant": []string{"acme"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := rules.NewEvaluator(configured)
			out, err := e.Evaluate(context.Background(), tt.gw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Terminate {
				t.Errorf("v1.0 evaluator must not terminate; got %+v", out)
			}
			if out.Response != nil {
				t.Errorf("v1.0 evaluator must not populate Response; got %+v", out.Response)
			}
		})
	}
}

func TestEvaluator_NoOp_NoRules(t *testing.T) {
	e := rules.NewEvaluator(nil)
	out, err := e.Evaluate(context.Background(), rules.GatewayContext{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.Terminate {
		t.Errorf("expected passthrough, got %+v", out)
	}
}

func TestEvaluator_Rules_Accessor(t *testing.T) {
	configured := []contractsrules.RuleContract{
		{Name: "a"},
		{Name: "b"},
	}
	e := rules.NewEvaluator(configured)
	got := e.Rules()
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("Rules() = %+v", got)
	}
}
