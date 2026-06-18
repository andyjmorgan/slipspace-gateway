package rules_test

import (
	"errors"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

func TestRuleContract_Validate(t *testing.T) {
	cond := &rules.ProviderCondition{Type: "provider", Operator: rules.EnumEquals, ExpectedProvider: "openai"}
	act := &rules.ChangeModelNameAction{Type: "changeModelName", NewModelName: "gpt-4o"}

	tests := []struct {
		name    string
		rule    rules.RuleContract
		wantErr error
	}{
		{
			name: "valid minimal",
			rule: rules.RuleContract{
				Name:      "rule-1",
				Condition: cond,
				Actions:   []rules.Action{act},
			},
		},
		{
			name: "valid with continue behavior",
			rule: rules.RuleContract{
				Name:      "rule-2",
				Condition: cond,
				Actions:   []rules.Action{act},
				Behavior:  rules.BehaviorContinue,
			},
		},
		{
			name: "valid with exit behavior",
			rule: rules.RuleContract{
				Name:      "rule-3",
				Condition: cond,
				Actions:   []rules.Action{act},
				Behavior:  rules.BehaviorExit,
			},
		},
		{
			name:    "empty name",
			rule:    rules.RuleContract{Condition: cond, Actions: []rules.Action{act}},
			wantErr: rules.ErrEmptyRuleName,
		},
		{
			name:    "no condition",
			rule:    rules.RuleContract{Name: "x", Actions: []rules.Action{act}},
			wantErr: rules.ErrNoCondition,
		},
		{
			name:    "no actions",
			rule:    rules.RuleContract{Name: "x", Condition: cond},
			wantErr: rules.ErrNoActions,
		},
		{
			name:    "empty actions slice",
			rule:    rules.RuleContract{Name: "x", Condition: cond, Actions: []rules.Action{}},
			wantErr: rules.ErrNoActions,
		},
		{
			name: "unknown behavior",
			rule: rules.RuleContract{
				Name:      "x",
				Condition: cond,
				Actions:   []rules.Action{act},
				Behavior:  "loop-forever",
			},
			wantErr: rules.ErrUnknownBehavior,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rule.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %v, got nil", tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error chain: got %v, want %v", err, tt.wantErr)
			}
		})
	}
}
