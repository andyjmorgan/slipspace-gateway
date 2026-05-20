package rules_test

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

// TestRuleGroup_Marshal_WithExtra covers the Extra-iteration branch of
// RuleGroup.MarshalJSON.
func TestRuleGroup_Marshal_WithExtra(t *testing.T) {
	src := `{"type":"group","logical_operator":"And","children":[],"audit":"x"}`
	cond, err := rules.UnmarshalCondition([]byte(src))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(cond)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if string(got["audit"]) != `"x"` {
		t.Errorf("audit not preserved: %s", out)
	}
}

// TestRuleGroup_UnmarshalYAML_NotMapping covers the non-mapping error branch.
func TestRuleGroup_UnmarshalYAML_NotMapping(t *testing.T) {
	src := `
name: r
priority: 1
condition: "not-a-mapping-for-group-either"
actions:
  - type: changeProvider
    newProvider: anthropic
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

// TestRuleGroup_UnmarshalYAML_BadAliasDecode covers the alias-decode error
// branch by feeding a non-string scalar where a string is expected.
func TestRuleGroup_UnmarshalYAML_BadAliasDecode(t *testing.T) {
	src := `
name: r
priority: 1
condition:
  type: group
  logicalOperator:
    nested: not-a-string
  children: []
actions:
  - type: changeProvider
    newProvider: anthropic
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

// TestDecodeConditionNode_MissingDiscriminator covers the
// peekYAMLDiscriminator missing-field path.
func TestDecodeConditionNode_MissingDiscriminator(t *testing.T) {
	src := `
name: r
priority: 1
condition:
  operator: Equals
actions:
  - type: changeProvider
    newProvider: anthropic
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

// TestDecodeConditionNode_NonScalarDiscriminator covers the non-scalar
// discriminator branch of peekYAMLDiscriminator.
func TestDecodeConditionNode_NonScalarDiscriminator(t *testing.T) {
	src := `
name: r
priority: 1
condition:
  type: [a, b]
  operator: Equals
actions:
  - type: changeProvider
    newProvider: anthropic
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

// TestDecodeActionNode_NonMapping covers the non-mapping branch of
// decodeActionNode.
func TestDecodeActionNode_NonMapping(t *testing.T) {
	src := `
name: r
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

// TestDecodeActionNode_MissingDiscriminator covers the missing-type branch.
func TestDecodeActionNode_MissingDiscriminator(t *testing.T) {
	src := `
name: r
priority: 1
condition:
  type: provider
  operator: Equals
  expectedProvider: openai
actions:
  - newProvider: anthropic
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

// TestDecodeActionNode_BadFactoryDecode covers the node.Decode error branch.
// Feeding a non-int value where an int is expected (StatusCode) triggers a
// decode error inside the typed factory path.
func TestDecodeActionNode_BadFactoryDecode(t *testing.T) {
	src := `
name: r
priority: 1
condition:
  type: provider
  operator: Equals
  expectedProvider: openai
actions:
  - type: returnStatusCode
    statusCode: not-an-int
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

// TestDecodeConditionNode_BadFactoryDecode triggers node.Decode failure on a
// typed condition factory.
func TestDecodeConditionNode_BadFactoryDecode(t *testing.T) {
	src := `
name: r
priority: 1
condition:
  type: modelName
  caseInsensitive: not-a-bool
  expectedModelName: x
actions:
  - type: changeProvider
    newProvider: anthropic
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

// TestRuleContract_UnmarshalJSON_MalformedTop covers the top-level JSON
// unmarshal error in RuleContract.UnmarshalJSON.
func TestRuleContract_UnmarshalJSON_MalformedTop(t *testing.T) {
	var r rules.RuleContract
	if err := json.Unmarshal([]byte(`["not","a","rule"]`), &r); err == nil {
		t.Fatalf("expected error")
	}
}

// TestRuleContract_UnmarshalYAML_BadScalarAlias triggers the scalarAlias
// decode failure by providing a non-scalar behavior — yaml.v3 cannot
// decode a sequence into a string-typed RuleBehavior.
func TestRuleContract_UnmarshalYAML_BadScalarAlias(t *testing.T) {
	src := `
name: r
behavior: [continue, exit]
condition:
  type: provider
  operator: Equals
  expectedProvider: openai
actions:
  - type: changeProvider
    newProvider: anthropic
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err == nil {
		t.Fatalf("expected error")
	}
}

// TestDecodeUnknownYAMLNode_DecodeError feeds an alias YAML node that fails
// to decode to a plain interface{}. yaml.v3 generally decodes anything, so
// we exercise this path indirectly via unknown-condition with a nested map.
func TestDecodeUnknownYAMLNode_NestedMap(t *testing.T) {
	src := `
name: r
priority: 1
condition:
  type: futureCondition
  nested:
    a: 1
    b: [2, 3]
actions:
  - type: changeProvider
    newProvider: anthropic
`
	var r rules.RuleContract
	if err := yaml.Unmarshal([]byte(src), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	uc, ok := r.Condition.(*rules.UnknownCondition)
	if !ok {
		t.Fatalf("type = %T", r.Condition)
	}
	if _, ok := uc.Extra["nested"]; !ok {
		t.Errorf("nested key not preserved: %v", uc.Extra)
	}
}
