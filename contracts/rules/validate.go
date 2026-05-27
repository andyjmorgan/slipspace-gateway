package rules

import "fmt"

// Validate enforces the minimum invariants on a RuleContract: Name is set,
// Condition is non-nil, at least one Action is present, and Behavior is one
// of the BehaviorX constants (empty is treated as BehaviorContinue downstream).
// ID is nullable — only the control plane mints IDs — but when set must be
// a non-nil pointer (uuid.Parse already enforced UUID shape on unmarshal).
func (r *RuleContract) Validate() error {
	if r.Name == "" {
		return ErrEmptyRuleName
	}
	if r.Condition == nil {
		return fmt.Errorf("rule %q: %w", r.Name, ErrNoCondition)
	}
	if len(r.Actions) == 0 {
		return fmt.Errorf("rule %q: %w", r.Name, ErrNoActions)
	}
	switch r.Behavior {
	case "", BehaviorContinue, BehaviorExit:
	default:
		return fmt.Errorf("rule %q: %w: %q", r.Name, ErrUnknownBehavior, r.Behavior)
	}
	if err := validateCondition(r.Condition); err != nil {
		return fmt.Errorf("rule %q: %w", r.Name, err)
	}
	for i, act := range r.Actions {
		if v, ok := act.(interface{ validate() error }); ok {
			if err := v.validate(); err != nil {
				return fmt.Errorf("rule %q: actions[%d]: %w", r.Name, i, err)
			}
		}
	}
	return nil
}

// validateCondition runs a condition's optional self-validation,
// descending into RuleGroup children so a malformed bodyField nested
// inside a group is still caught at config-load.
func validateCondition(c Condition) error {
	if c == nil {
		return nil
	}
	if g, ok := c.(*RuleGroup); ok {
		for i, child := range g.Children {
			if err := validateCondition(child); err != nil {
				return fmt.Errorf("condition: children[%d]: %w", i, err)
			}
		}
		return nil
	}
	if v, ok := c.(interface{ validate() error }); ok {
		return v.validate()
	}
	return nil
}
