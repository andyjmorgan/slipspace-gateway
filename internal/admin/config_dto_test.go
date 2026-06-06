package admin

import (
	"strings"
	"testing"

	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

func TestSummariseCondition_NilFallsBack(t *testing.T) {
	if got := summariseCondition(nil); got != "(no condition)" {
		t.Errorf("nil condition = %q", got)
	}
}

func TestSummariseCondition_AllConcreteTypes(t *testing.T) {
	cases := []struct {
		name    string
		input   rulescontract.Condition
		wantSub string
	}{
		{
			name:    "provider",
			input:   &rulescontract.ProviderCondition{Type: "provider", Operator: rulescontract.EnumEquals, ExpectedProvider: "openai"},
			wantSub: "openai",
		},
		{
			name:    "endpoint negated",
			input:   &rulescontract.ProtocolCondition{Type: "protocol", Operator: rulescontract.EnumEquals, ExpectedProtocol: "openai.chat_completions", Not: true},
			wantSub: "(negated)",
		},
		{
			name:    "modelName starts with",
			input:   &rulescontract.ModelNameCondition{Type: "modelName", Operator: rulescontract.StringStartsWith, ExpectedModelName: "claude-"},
			wantSub: "claude-",
		},
		{
			name:    "header with value",
			input:   headerWithValue(),
			wantSub: "X-Bar",
		},
		{
			name:    "group",
			input:   &rulescontract.RuleGroup{Type: "group", LogicalOperator: rulescontract.LogicalAnd, Children: []rulescontract.Condition{&rulescontract.ProviderCondition{Type: "provider"}}},
			wantSub: "group",
		},
		{
			name:    "unknown",
			input:   &rulescontract.UnknownCondition{Type: "mystery"},
			wantSub: "mystery",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summariseCondition(tc.input)
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("summariseCondition(%s) = %q, want substring %q", tc.name, got, tc.wantSub)
			}
		})
	}
}

func headerWithValue() *rulescontract.HeaderCondition {
	op := rulescontract.StringEquals
	return &rulescontract.HeaderCondition{
		Type:          "header",
		KeyOperator:   rulescontract.StringEquals,
		KeyPattern:    "X-Bar",
		ValueOperator: &op,
		ValuePattern:  "ok",
	}
}

func TestSummariseActionTypes_SkipsNilEntries(t *testing.T) {
	got := summariseActionTypes([]rulescontract.Action{
		&rulescontract.SetHeaderAction{Type: "setHeader"},
		nil,
		&rulescontract.ChangeProviderAction{Type: "changeProvider"},
	})
	if len(got) != 2 || got[0] != "setHeader" || got[1] != "changeProvider" {
		t.Errorf("got %v", got)
	}
}

func TestOpString_FallsBackToEquals(t *testing.T) {
	if got := opString(""); got != "Equals" {
		t.Errorf("opString(empty) = %q", got)
	}
	if got := opString("StartsWith"); got != "StartsWith" {
		t.Errorf("opString(StartsWith) = %q", got)
	}
}

func TestNotSuffix(t *testing.T) {
	if got := notSuffix(false); got != "" {
		t.Errorf("notSuffix(false) = %q", got)
	}
	if got := notSuffix(true); got != " (negated)" {
		t.Errorf("notSuffix(true) = %q", got)
	}
}

func TestPluralChildren(t *testing.T) {
	if got := pluralChildren(0); got != "0 children" {
		t.Errorf("pluralChildren(0) = %q", got)
	}
	if got := pluralChildren(1); got != "1 child" {
		t.Errorf("pluralChildren(1) = %q", got)
	}
	if got := pluralChildren(5); got != "5 children" {
		t.Errorf("pluralChildren(5) = %q", got)
	}
}
