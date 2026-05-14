// Package rules defines the public schema for the gateway's rule engine. The
// schema is parsed and validated in v1.0 but evaluation is deferred to v1.1;
// see the "Rule Schema" design note for the long-form.
//
// YAML tags are camelCase (the operator-authored wire format); JSON tags are
// snake_case (the internal/API representation). They differ on purpose.
package rules

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// StringOperator selects the comparison strategy for string-valued conditions.
type StringOperator string

// String operators accepted by ModelNameCondition and HeaderCondition.
const (
	StringEquals StringOperator = "Equals"

	StringStartsWith StringOperator = "StartsWith"

	StringEndsWith StringOperator = "EndsWith"

	StringContains StringOperator = "Contains"

	StringRegex StringOperator = "Regex"
)

// EnumOperator selects the comparison strategy for enum-valued conditions.
// Only equality is currently meaningful for provider/endpoint matches.
type EnumOperator string

// EnumEquals is the only currently-supported enum operator.
const EnumEquals EnumOperator = "Equals"

// LogicalOperator selects how a RuleGroup composes its children.
type LogicalOperator string

// Logical operators accepted by RuleGroup.
const (
	LogicalAnd LogicalOperator = "And"

	LogicalOr LogicalOperator = "Or"
)

// HeaderOp selects how a SetHeaderAction modifies an outgoing header.
type HeaderOp string

// Header operations accepted by SetHeaderAction.
const (
	HeaderSet HeaderOp = "Set"

	HeaderAppend HeaderOp = "Append"

	HeaderPrepend HeaderOp = "Prepend"

	HeaderRemove HeaderOp = "Remove"
)

// StatusCodeBodyType selects the Content-Type family for the synthetic body of
// a ReturnStatusCodeAction response.
type StatusCodeBodyType string

// Status code body types accepted by ReturnStatusCodeAction.
const (
	StatusBodyText StatusCodeBodyType = "text"

	StatusBodyJSON StatusCodeBodyType = "json"

	StatusBodyHTML StatusCodeBodyType = "html"
)

// RuleBehavior controls whether evaluation continues to lower-priority rules
// after the current rule's actions have fired.
type RuleBehavior string

// Rule behaviors accepted by RuleContract.Behavior.
const (
	BehaviorContinue RuleBehavior = "continue"

	BehaviorExit RuleBehavior = "exit"
)

// RuleContract is one rule applied to a request as it flows through the
// pipeline. Rules are evaluated in ascending Priority order. On match, every
// Action in Actions fires in sequence; terminating actions short-circuit the
// pipeline. Behavior controls whether evaluation continues after this rule.
type RuleContract struct {
	// ID is the rule's stable identifier; surfaced in telemetry and used to
	// reference the rule in logs and reporting events.
	ID string `yaml:"id" json:"id"`

	// Name is a human-readable label. Optional; ID is the canonical handle.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// Priority orders evaluation. Lower values run first; ties break on the
	// rule's position in the Configuration.
	Priority int `yaml:"priority" json:"priority"`

	// Condition is the predicate that must match for the rule's Actions to
	// fire. Polymorphic; see Condition and its concrete types.
	Condition Condition `yaml:"condition" json:"condition"`

	// Actions are executed in order on a match. A terminating action
	// short-circuits the pipeline regardless of Behavior.
	Actions []Action `yaml:"actions" json:"actions"`

	// Behavior controls whether evaluation continues to lower-priority rules
	// after this rule's Actions complete. Empty defaults to BehaviorContinue.
	Behavior RuleBehavior `yaml:"behavior,omitempty" json:"behavior,omitempty"`
}

// ruleContractWire mirrors RuleContract with the polymorphic fields held as
// raw JSON so the polymorphic registries can dispatch on the discriminator.
type ruleContractWire struct {
	ID string `json:"id"`

	Name string `json:"name,omitempty"`

	Priority int `json:"priority"`

	Condition json.RawMessage `json:"condition"`

	Actions []json.RawMessage `json:"actions"`

	Behavior RuleBehavior `json:"behavior,omitempty"`
}

// UnmarshalJSON dispatches the Condition and each Action through the
// polymorphic registries.
func (r *RuleContract) UnmarshalJSON(data []byte) error {
	var w ruleContractWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("rules: unmarshal rule: %w", err)
	}
	r.ID = w.ID
	r.Name = w.Name
	r.Priority = w.Priority
	r.Behavior = w.Behavior

	if len(w.Condition) > 0 && !isJSONNull(w.Condition) {
		cond, err := UnmarshalCondition(w.Condition)
		if err != nil {
			return fmt.Errorf("rules: rule %q: condition: %w", w.ID, err)
		}
		r.Condition = cond
	}

	if len(w.Actions) > 0 {
		r.Actions = make([]Action, len(w.Actions))
		for i, raw := range w.Actions {
			act, err := UnmarshalAction(raw)
			if err != nil {
				return fmt.Errorf("rules: rule %q: actions[%d]: %w", w.ID, i, err)
			}
			r.Actions[i] = act
		}
	}
	return nil
}

// UnmarshalYAML decodes a RuleContract from a YAML mapping node, dispatching
// the polymorphic Condition and Actions via their yaml.Node trees.
func (r *RuleContract) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("rules: rule: expected mapping, got %v", value.Kind)
	}

	type scalarAlias struct {
		ID       string       `yaml:"id"`
		Name     string       `yaml:"name,omitempty"`
		Priority int          `yaml:"priority"`
		Behavior RuleBehavior `yaml:"behavior,omitempty"`
	}
	var s scalarAlias
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("rules: rule: %w", err)
	}
	r.ID = s.ID
	r.Name = s.Name
	r.Priority = s.Priority
	r.Behavior = s.Behavior

	for i := 0; i < len(value.Content); i += 2 {
		keyNode := value.Content[i]
		valNode := value.Content[i+1]
		switch keyNode.Value {
		case "condition":
			cond, err := decodeConditionNode(valNode)
			if err != nil {
				return fmt.Errorf("rules: rule %q: condition: %w", r.ID, err)
			}
			r.Condition = cond
		case "actions":
			if valNode.Kind != yaml.SequenceNode {
				return fmt.Errorf("rules: rule %q: actions: expected sequence, got %v", r.ID, valNode.Kind)
			}
			r.Actions = make([]Action, len(valNode.Content))
			for j, n := range valNode.Content {
				act, err := decodeActionNode(n)
				if err != nil {
					return fmt.Errorf("rules: rule %q: actions[%d]: %w", r.ID, j, err)
				}
				r.Actions[j] = act
			}
		}
	}
	return nil
}

func isJSONNull(data []byte) bool {
	return bytes.Equal(bytes.TrimSpace(data), []byte("null"))
}
