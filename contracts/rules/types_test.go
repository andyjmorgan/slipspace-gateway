package rules_test

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

const sampleYAML = `
id: route-gpt-4-to-anthropic
name: Route GPT-4 to Anthropic
priority: 10
behavior: exit
condition:
  type: group
  logicalOperator: And
  children:
    - type: provider
      operator: Equals
      expectedProvider: openai
    - type: modelName
      operator: StartsWith
      expectedModelName: gpt-4
      caseInsensitive: true
    - type: header
      keyOperator: Equals
      keyPattern: X-Tenant
      valueOperator: Regex
      valuePattern: ^acme-
actions:
  - type: changeProvider
    newProvider: anthropic
  - type: setHeader
    headerName: X-Routed-By
    headerAction: Set
    headerValue: sluice
  - type: returnStatusCode
    statusCode: 200
    body: "{}"
    bodyType: json
`

func TestRuleContract_YAMLRoundTrip(t *testing.T) {
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(sampleYAML), &r); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if r.ID != "route-gpt-4-to-anthropic" {
		t.Errorf("ID = %q", r.ID)
	}
	if r.Priority != 10 {
		t.Errorf("Priority = %d", r.Priority)
	}
	if r.Behavior != rules.BehaviorExit {
		t.Errorf("Behavior = %q", r.Behavior)
	}

	grp, ok := r.Condition.(*rules.RuleGroup)
	if !ok {
		t.Fatalf("condition type = %T", r.Condition)
	}
	if grp.LogicalOperator != rules.LogicalAnd || len(grp.Children) != 3 {
		t.Fatalf("group: %+v", grp)
	}
	pc, ok := grp.Children[0].(*rules.ProviderCondition)
	if !ok || pc.ExpectedProvider != "openai" {
		t.Errorf("child[0] = %+v", grp.Children[0])
	}
	mc, ok := grp.Children[1].(*rules.ModelNameCondition)
	if !ok || mc.ExpectedModelName != "gpt-4" || !mc.CaseInsensitive {
		t.Errorf("child[1] = %+v", grp.Children[1])
	}
	hc, ok := grp.Children[2].(*rules.HeaderCondition)
	if !ok || hc.KeyPattern != "X-Tenant" || hc.ValueOperator == nil || *hc.ValueOperator != rules.StringRegex {
		t.Errorf("child[2] = %+v", grp.Children[2])
	}

	if len(r.Actions) != 3 {
		t.Fatalf("actions len = %d", len(r.Actions))
	}
	if cp, ok := r.Actions[0].(*rules.ChangeProviderAction); !ok || cp.NewProvider != "anthropic" {
		t.Errorf("actions[0] = %+v", r.Actions[0])
	}
	if sh, ok := r.Actions[1].(*rules.SetHeaderAction); !ok || sh.HeaderName != "X-Routed-By" {
		t.Errorf("actions[1] = %+v", r.Actions[1])
	}
	if rs, ok := r.Actions[2].(*rules.ReturnStatusCodeAction); !ok || rs.StatusCode != 200 || rs.BodyType != rules.StatusBodyJSON {
		t.Errorf("actions[2] = %+v", r.Actions[2])
	}

	if err := r.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestRuleContract_YAML_UnknownConditionAndAction_FallBack(t *testing.T) {
	src := `
id: unknown-stuff
priority: 1
condition:
  type: futureCondition
  someField: x
actions:
  - type: futureAction
    extra: 7
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	uc, ok := r.Condition.(*rules.UnknownCondition)
	if !ok || uc.Type != "futureCondition" {
		t.Fatalf("condition = %T %+v", r.Condition, r.Condition)
	}
	if _, ok := uc.Extra["someField"]; !ok {
		t.Errorf("someField not preserved: %v", uc.Extra)
	}
	if len(r.Actions) != 1 {
		t.Fatalf("actions len = %d", len(r.Actions))
	}
	ua, ok := r.Actions[0].(*rules.UnknownAction)
	if !ok || ua.Type != "futureAction" {
		t.Fatalf("action = %T %+v", r.Actions[0], r.Actions[0])
	}
	if _, ok := ua.Extra["extra"]; !ok {
		t.Errorf("extra not preserved: %v", ua.Extra)
	}
}

func TestRuleContract_JSONRoundTrip(t *testing.T) {
	src := `{
        "id": "rule-json",
        "priority": 5,
        "behavior": "continue",
        "condition": {"type":"provider","operator":"Equals","expected_provider":"openai"},
        "actions": [
            {"type":"changeProvider","new_provider":"anthropic"}
        ]
    }`
	var r rules.RuleContract
	if err := json.Unmarshal([]byte(src), &r); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if r.ID != "rule-json" || r.Priority != 5 || r.Behavior != rules.BehaviorContinue {
		t.Errorf("top fields: %+v", r)
	}
	if _, ok := r.Condition.(*rules.ProviderCondition); !ok {
		t.Errorf("condition: %T", r.Condition)
	}
	if cp, ok := r.Actions[0].(*rules.ChangeProviderAction); !ok || cp.NewProvider != "anthropic" {
		t.Errorf("actions[0]: %+v", r.Actions[0])
	}
}

func TestRuleContract_JSON_NullCondition_OK(t *testing.T) {
	src := `{"id":"x","priority":1,"condition":null,"actions":[]}`
	var r rules.RuleContract
	if err := json.Unmarshal([]byte(src), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Condition != nil {
		t.Errorf("expected nil condition, got %+v", r.Condition)
	}
}

func TestRuleContract_JSON_BadCondition(t *testing.T) {
	src := `{"id":"x","priority":1,"condition":{"operator":"Equals"},"actions":[]}`
	var r rules.RuleContract
	if err := json.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

func TestRuleContract_JSON_BadAction(t *testing.T) {
	src := `{"id":"x","priority":1,"condition":{"type":"provider","operator":"Equals","expected_provider":"openai"},"actions":[{"oops":1}]}`
	var r rules.RuleContract
	if err := json.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

func TestRuleContract_JSON_Malformed(t *testing.T) {
	var r rules.RuleContract
	if err := json.Unmarshal([]byte(`not-json`), &r); err == nil {
		t.Fatalf("expected error")
	}
}

func TestRuleContract_YAML_NonMapping(t *testing.T) {
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(`- 1`), &r); err == nil {
		t.Fatalf("expected error")
	}
}

func TestRuleContract_YAML_BadActionsShape(t *testing.T) {
	src := `
id: x
priority: 1
condition:
  type: provider
  operator: Equals
  expectedProvider: openai
actions: not-a-list
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

func TestRuleContract_YAML_BadConditionType(t *testing.T) {
	src := `
id: x
priority: 1
condition: "not-a-mapping"
actions:
  - type: changeProvider
    newProvider: anthropic
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

func TestRuleContract_YAML_BadActionEntry(t *testing.T) {
	src := `
id: x
priority: 1
condition:
  type: provider
  operator: Equals
  expectedProvider: openai
actions:
  - "not-a-mapping"
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

func TestRuleContract_YAML_GroupBadChildren(t *testing.T) {
	src := `
id: x
priority: 1
condition:
  type: group
  logicalOperator: And
  children: "not-a-list"
actions:
  - type: changeProvider
    newProvider: anthropic
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

func TestRuleContract_YAML_GroupBadChildEntry(t *testing.T) {
	src := `
id: x
priority: 1
condition:
  type: group
  logicalOperator: And
  children:
    - "not-a-mapping"
actions:
  - type: changeProvider
    newProvider: anthropic
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

func TestRuleContract_OperatorEnums_Coverage(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"StringEquals", string(rules.StringEquals), "Equals"},
		{"StringStartsWith", string(rules.StringStartsWith), "StartsWith"},
		{"StringEndsWith", string(rules.StringEndsWith), "EndsWith"},
		{"StringContains", string(rules.StringContains), "Contains"},
		{"StringRegex", string(rules.StringRegex), "Regex"},
		{"EnumEquals", string(rules.EnumEquals), "Equals"},
		{"LogicalAnd", string(rules.LogicalAnd), "And"},
		{"LogicalOr", string(rules.LogicalOr), "Or"},
		{"HeaderSet", string(rules.HeaderSet), "Set"},
		{"HeaderAppend", string(rules.HeaderAppend), "Append"},
		{"HeaderPrepend", string(rules.HeaderPrepend), "Prepend"},
		{"HeaderRemove", string(rules.HeaderRemove), "Remove"},
		{"StatusBodyText", string(rules.StatusBodyText), "text"},
		{"StatusBodyJSON", string(rules.StatusBodyJSON), "json"},
		{"StatusBodyHTML", string(rules.StatusBodyHTML), "html"},
		{"BehaviorContinue", string(rules.BehaviorContinue), "continue"},
		{"BehaviorExit", string(rules.BehaviorExit), "exit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
			}
		})
	}
}
