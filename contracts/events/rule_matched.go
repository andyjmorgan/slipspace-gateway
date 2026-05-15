package events

import "time"

// RuleMatched is the payload carried inside a `gateway.rule.matched`
// envelope — one event per rule that matched on a request, emitted by
// the per-request reporter observer at OnComplete after the rules
// middleware has accumulated the per-request match records.
//
// Cardinality on the bus is bounded by the number of matched rules per
// request; subscribers can filter by Configuration or RuleName for
// per-policy feeds without needing to decode the request envelope.
type RuleMatched struct {
	// CorrelationID is the gateway-assigned request UUID — joinable
	// back to the parent gateway.request event.
	CorrelationID string `json:"correlation_id,omitempty"`

	// RuleID is the optional UUID minted by the control plane when the
	// rule is authored via the management API. Empty for operator-
	// authored static config; use RuleName as the stable handle in
	// that case.
	RuleID string `json:"rule_id,omitempty"`

	// RuleName is the operator-authored rule name. Always populated;
	// the canonical telemetry label.
	RuleName string `json:"rule_name"`

	// Configuration is the resolved configuration name the rule was
	// evaluated under.
	Configuration string `json:"configuration"`

	// MatchedAt is the wall-clock time the rule's condition matched.
	MatchedAt time.Time `json:"matched_at"`

	// ActionsApplied lists the action type discriminators that fired
	// for this match, in the order they ran (e.g. ["changeProvider",
	// "setHeader"]). An action that returned an error contributes its
	// type here too — ErrorMessage carries the detail.
	ActionsApplied []string `json:"actions_applied"`

	// Terminated is true iff a terminating action (returnStatusCode,
	// llmImpersonation) short-circuited the pipeline. Behavior=Exit
	// alone does NOT set this — that's an iteration boundary, not a
	// terminating outcome.
	Terminated bool `json:"terminated,omitempty"`

	// ErrorMessage carries the human-readable error string when an
	// action failed during apply. Empty on the success path.
	ErrorMessage string `json:"error_message,omitempty"`
}
