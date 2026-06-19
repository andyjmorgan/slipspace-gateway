package rules_test

import (
	"encoding/json"
	"errors"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

const sampleYAML = `
id: 550e8400-e29b-41d4-a716-446655440000
name: route-gpt-4-to-anthropic
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
    headerValue: slipspace
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
	if r.ID == nil || r.ID.String() != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("ID = %v", r.ID)
	}
	if r.Name != "route-gpt-4-to-anthropic" {
		t.Errorf("Name = %q", r.Name)
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
name: unknown-stuff
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
        "name": "rule-json",
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
	if r.Name != "rule-json" || r.Behavior != rules.BehaviorContinue {
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
	src := `{"name":"x","priority":1,"condition":null,"actions":[]}`
	var r rules.RuleContract
	if err := json.Unmarshal([]byte(src), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Condition != nil {
		t.Errorf("expected nil condition, got %+v", r.Condition)
	}
}

func TestRuleContract_JSON_BadCondition(t *testing.T) {
	src := `{"name":"x","priority":1,"condition":{"operator":"Equals"},"actions":[]}`
	var r rules.RuleContract
	if err := json.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

func TestRuleContract_JSON_BadAction(t *testing.T) {
	src := `{"name":"x","priority":1,"condition":{"type":"provider","operator":"Equals","expected_provider":"openai"},"actions":[{"oops":1}]}`
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
name: x
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
name: x
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
name: x
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
name: x
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
name: x
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

// TestRuleContract_YAML_ID_AbsentLeavesNil confirms a rule with no `id` field
// unmarshals to a nil ID — the static-config default.
func TestRuleContract_YAML_ID_AbsentLeavesNil(t *testing.T) {
	src := `
name: r
priority: 1
condition:
  type: provider
  operator: Equals
  expectedProvider: openai
actions:
  - type: changeProvider
    newProvider: anthropic
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.ID != nil {
		t.Errorf("expected nil ID, got %v", r.ID)
	}
	if r.Name != "r" {
		t.Errorf("Name = %q", r.Name)
	}
}

// TestRuleContract_YAML_BadUUID confirms a malformed UUID is wrapped in
// ErrInvalidRuleID rather than surfacing as an opaque parse error.
func TestRuleContract_YAML_BadUUID(t *testing.T) {
	src := `
id: not-a-uuid
name: r
priority: 1
condition:
  type: provider
  operator: Equals
  expectedProvider: openai
actions:
  - type: changeProvider
    newProvider: anthropic
`
	var r rules.RuleContract
	err := yaml.Unmarshal([]byte(src), &r)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, rules.ErrInvalidRuleID) {
		t.Errorf("error chain: got %v, want ErrInvalidRuleID", err)
	}
}

// TestRuleContract_JSON_BadUUID confirms the JSON path also wraps malformed
// UUIDs in ErrInvalidRuleID.
func TestRuleContract_JSON_BadUUID(t *testing.T) {
	src := `{"id":"not-a-uuid","name":"r","priority":1,"condition":{"type":"provider","operator":"Equals","expected_provider":"openai"},"actions":[{"type":"changeProvider","new_provider":"anthropic"}]}`
	var r rules.RuleContract
	err := json.Unmarshal([]byte(src), &r)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, rules.ErrInvalidRuleID) {
		t.Errorf("error chain: got %v, want ErrInvalidRuleID", err)
	}
}

// TestRuleContract_JSON_ValidUUID confirms a well-formed UUID round-trips
// through the wire string and lands on the typed *uuid.UUID field.
func TestRuleContract_JSON_ValidUUID(t *testing.T) {
	src := `{"id":"550e8400-e29b-41d4-a716-446655440000","name":"r","priority":1,"condition":{"type":"provider","operator":"Equals","expected_provider":"openai"},"actions":[{"type":"changeProvider","new_provider":"anthropic"}]}`
	var r rules.RuleContract
	if err := json.Unmarshal([]byte(src), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.ID == nil || r.ID.String() != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("ID = %v", r.ID)
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
