package rules_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

// TestAllConditions_MarshalRoundTrip exercises MarshalJSON on every concrete
// condition type and confirms a JSON round-trip reproduces the same shape
// after Unmarshal.
func TestAllConditions_MarshalRoundTrip(t *testing.T) {
	cases := []rules.Condition{
		&rules.ProviderCondition{Type: "provider", Operator: rules.EnumEquals, ExpectedProvider: "openai", Not: true},
		&rules.EndpointCondition{Type: "endpoint", Operator: rules.EnumEquals, ExpectedEndpoint: "messages"},
		&rules.ModelNameCondition{Type: "modelName", Operator: rules.StringContains, ExpectedModelName: "gpt", CaseInsensitive: true},
		&rules.HeaderCondition{Type: "header", KeyOperator: rules.StringEquals, KeyPattern: "X-Tenant"},
		&rules.UnknownCondition{Type: "newcond"},
	}
	for _, c := range cases {
		t.Run(c.ConditionType(), func(t *testing.T) {
			raw, err := json.Marshal(c)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			back, err := rules.UnmarshalCondition(raw)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if back.ConditionType() != c.ConditionType() {
				t.Errorf("ConditionType diverged: got %q, want %q", back.ConditionType(), c.ConditionType())
			}
			out, err := json.Marshal(back)
			if err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			var a, b map[string]json.RawMessage
			_ = json.Unmarshal(raw, &a)
			_ = json.Unmarshal(out, &b)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("round-trip diverged.\n raw=%s\nout=%s", raw, out)
			}
		})
	}
}

// TestAllActions_MarshalRoundTrip exercises MarshalJSON on every concrete
// action type.
func TestAllActions_MarshalRoundTrip(t *testing.T) {
	hv := rules.StringEquals
	_ = hv
	cases := []rules.Action{
		&rules.ChangeProviderAction{Type: "changeProvider", NewProvider: "anthropic"},
		&rules.ChangeModelNameAction{Type: "changeModelName", NewModelName: "gpt-4o-mini"},
		&rules.ChangeUrlAction{Type: "changeUrl", NewURL: "https://api.example.com"},
		&rules.ChangeApiKeyAction{Type: "changeApiKey", APIKey: "k", UseSluiceKey: true},
		&rules.SetHeaderAction{Type: "setHeader", HeaderName: "X-Foo", HeaderAction: rules.HeaderSet, HeaderValue: "v"},
		&rules.AppendQueryStringAction{Type: "appendQueryString", Key: "tenant", Value: "acme"},
		&rules.ReturnStatusCodeAction{Type: "returnStatusCode", StatusCode: 200, Body: "ok", BodyType: rules.StatusBodyText},
		&rules.LlmImpersonationAction{Type: "llmImpersonation", Message: "hi"},
		&rules.UnknownAction{Type: "newaction"},
	}
	for _, a := range cases {
		t.Run(a.ActionType(), func(t *testing.T) {
			raw, err := json.Marshal(a)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			back, err := rules.UnmarshalAction(raw)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if back.ActionType() != a.ActionType() {
				t.Errorf("ActionType diverged: got %q, want %q", back.ActionType(), a.ActionType())
			}
			out, err := json.Marshal(back)
			if err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			var x, y map[string]json.RawMessage
			_ = json.Unmarshal(raw, &x)
			_ = json.Unmarshal(out, &y)
			if !reflect.DeepEqual(x, y) {
				t.Errorf("round-trip diverged.\n raw=%s\nout=%s", raw, out)
			}
		})
	}
}

// TestRuleGroup_MarshalRoundTrip exercises RuleGroup.MarshalJSON via a parent
// round-trip with nested children.
func TestRuleGroup_MarshalRoundTrip(t *testing.T) {
	g := &rules.RuleGroup{
		Type:            "group",
		LogicalOperator: rules.LogicalOr,
		Children: []rules.Condition{
			&rules.ProviderCondition{Type: "provider", Operator: rules.EnumEquals, ExpectedProvider: "openai"},
		},
		Not: true,
	}
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	back, err := rules.UnmarshalCondition(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	bg, ok := back.(*rules.RuleGroup)
	if !ok {
		t.Fatalf("type = %T", back)
	}
	if bg.LogicalOperator != rules.LogicalOr || !bg.Not || len(bg.Children) != 1 {
		t.Errorf("round-trip diverged: %+v", bg)
	}
}
