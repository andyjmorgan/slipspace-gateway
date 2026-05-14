package rules

import "fmt"

// Validate enforces the minimum invariants on a RuleContract: ID is set,
// Condition is non-nil, at least one Action is present, and Behavior is one
// of the BehaviorX constants (empty is treated as BehaviorContinue downstream).
func (r *RuleContract) Validate() error {
	if r.ID == "" {
		return ErrEmptyRuleID
	}
	if r.Condition == nil {
		return fmt.Errorf("rule %q: %w", r.ID, ErrNoCondition)
	}
	if len(r.Actions) == 0 {
		return fmt.Errorf("rule %q: %w", r.ID, ErrNoActions)
	}
	switch r.Behavior {
	case "", BehaviorContinue, BehaviorExit:
	default:
		return fmt.Errorf("rule %q: %w: %q", r.ID, ErrUnknownBehavior, r.Behavior)
	}
	return nil
}
