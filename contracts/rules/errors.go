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

// ErrEmptyTranslateTarget is returned when a TranslateAction carries an empty
// target_protocol. The destination builder cannot resolve a translator without
// it, so the rule is rejected at config load rather than failing per-request.
var ErrEmptyTranslateTarget = errors.New("rules: translate target protocol required")

// ErrEmptyActionField is returned at config load when an action's required
// string field is empty after trimming (an addTag with no tag, a setHeader
// with no headerName, ...). It is wrapped with the action type and field
// name. The runtime keeps its own errEmptyValue check as belt-and-braces;
// this sentinel is what makes an authoring mistake a startup / admin-write
// (422) failure instead of a per-request one (issue #527).
var ErrEmptyActionField = errors.New("rules: required action field is empty")

// ErrInvalidActionField is returned at config load when an action field is
// present but malformed: a changeUrl newUrl that does not parse, a
// returnStatusCode statusCode outside [100, 599], or a setHeader
// headerAction outside the Set / Append / Prepend / Remove set. Wrapped with
// the action type and field name.
var ErrInvalidActionField = errors.New("rules: invalid action field")

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

// ErrInvalidTarget is returned when a rewrite/condition target is not a
// valid scope-prefixed path — bad scope, empty path, or (for write
// targets) a segment that is not a bare identifier.
var ErrInvalidTarget = errors.New("rules: invalid body target")

// ErrInvalidRewriteValue is returned when a rewriteField/appendField
// value cannot be decoded from its wire form.
var ErrInvalidRewriteValue = errors.New("rules: invalid rewrite value")

// ErrResponseScopeUnsupported is returned when a bodyField *condition*
// targets response.body. Conditions evaluate on the request path, before
// any response exists, so the scope is rejected at config load. It does
// not apply to the write actions — rewriteField / removeField /
// appendField accept response.body and are applied in the response phase.
var ErrResponseScopeUnsupported = errors.New("rules: response.body scope not yet supported")

// ErrUnknownBodyFieldOperator is returned when a BodyFieldCondition
// carries an operator outside the BodyFieldX set.
var ErrUnknownBodyFieldOperator = errors.New("rules: unknown bodyField operator")

// ErrInvalidBodyFieldRegex is returned when a BodyFieldCondition with
// the Matches operator carries a value that does not compile as a
// regular expression.
var ErrInvalidBodyFieldRegex = errors.New("rules: invalid bodyField regex")
