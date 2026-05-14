package rules

import "errors"

// ErrEmptyRuleID is returned when a RuleContract carries no ID and ID is
// required by the caller's validation policy. ID is otherwise nullable on the
// type — only the control plane mints IDs; static config leaves it nil.
var ErrEmptyRuleID = errors.New("rules: rule id required")

// ErrEmptyRuleName is returned when a RuleContract carries no Name. Name is
// the canonical handle referenced from Configuration.RuleNames and is required
// at the schema level.
var ErrEmptyRuleName = errors.New("rules: rule name required")

// ErrInvalidRuleID is returned when a RuleContract.ID string fails to parse as
// a UUID during unmarshal or in-memory construction.
var ErrInvalidRuleID = errors.New("rules: rule id not a valid uuid")

// ErrNoCondition is returned when a RuleContract has no condition set.
var ErrNoCondition = errors.New("rules: rule condition required")

// ErrNoActions is returned when a RuleContract has no actions in its list.
var ErrNoActions = errors.New("rules: rule must have at least one action")

// ErrUnknownConditionType is reserved for future strict-mode validation that
// rejects an unknown condition discriminator instead of falling back to
// UnknownCondition.
var ErrUnknownConditionType = errors.New("rules: unknown condition type")

// ErrUnknownActionType is reserved for future strict-mode validation that
// rejects an unknown action discriminator instead of falling back to
// UnknownAction.
var ErrUnknownActionType = errors.New("rules: unknown action type")

// ErrUnknownBehavior is returned when a RuleContract.Behavior is not one of
// the BehaviorX constants.
var ErrUnknownBehavior = errors.New("rules: unknown behavior")
