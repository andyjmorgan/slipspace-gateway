package rules

import "errors"

// ErrEmptyRuleID is returned when a RuleContract carries no ID.
var ErrEmptyRuleID = errors.New("rules: rule id required")

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
