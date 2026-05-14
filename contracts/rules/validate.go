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
	return nil
}
