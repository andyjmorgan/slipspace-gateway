// Package rules holds the gateway's rule engine. v1.0 ships a no-op
// Evaluator that always returns a passthrough Outcome; the public schema
// lives in contracts/rules. The v1.1 engine flips evaluation on per the
// algorithm in the "Rule Schema" design note.
package rules

import (
	"context"
	"net/http"

	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

// Evaluator runs rule evaluation against a GatewayContext.
//
// v1.0 always returns Outcome{Terminate: false} regardless of the configured
// rules — the engine is wired but inert so the YAML schema is locked in
// place ahead of v1.1, which flips evaluation on without a config
// migration.
type Evaluator struct {
	// rules is the configured rule set, retained by reference. Read-only
	// after construction; the v1.1 engine iterates this in priority order.
	rules []contractsrules.RuleContract
}

// NewEvaluator constructs an Evaluator from the configured rule set.
//
// The slice is retained, not copied; callers must not mutate it
// concurrently. The intended lifecycle is config-load → NewEvaluator →
// long-lived shared instance.
func NewEvaluator(rules []contractsrules.RuleContract) *Evaluator {
	return &Evaluator{rules: rules}
}

// Rules returns the rule set the evaluator was constructed with. The slice
// is shared with the evaluator's internal state; callers must not mutate
// it.
func (e *Evaluator) Rules() []contractsrules.RuleContract { return e.rules }

// Evaluate iterates the configured rules in priority order and returns the
// resulting Outcome. v1.0 short-circuits to a passthrough Outcome; the real
// engine lands in v1.1 per the "Rule Schema" design note.
func (e *Evaluator) Evaluate(_ context.Context, _ GatewayContext) (contractsrules.Outcome, error) {
	return contractsrules.Outcome{Terminate: false}, nil
}

// GatewayContext is the read-only slice of request state a rule's
// Condition inspects. v1.0 carried only the routing identifiers and
// inbound headers; v1.0.1 extends it with the resolved Configuration
// name (for telemetry) and the decoded typed Body so content-based
// conditions can read structured fields without re-parsing Raw bytes.
//
// Actions mutate state via MutableState, not GatewayContext. Splitting
// them keeps the read/write surfaces explicit and lets the evaluator
// pass GatewayContext by value to Condition.Matches without leaking a
// mutable handle into condition implementations.
type GatewayContext struct {
	// Provider is the routed upstream provider name (e.g. "openai").
	Provider string

	// Endpoint is the routed endpoint name under that provider (e.g.
	// "chat_completions").
	Endpoint string

	// Model is the model identifier the client requested, read from
	// the routing match or the decoded body. Empty for endpoints that
	// carry no model.
	Model string

	// Headers is the inbound request header set. HeaderCondition reads
	// from this; rule-driven mutations land on MutableState.
	Headers http.Header

	// ConfigurationName is the resolved configuration the request was
	// authorised under. Surfaces on rule-match telemetry labels and on
	// the gateway.rule.matched event.
	ConfigurationName string

	// Body is the typed decoded request body when the route carries
	// one (bodycapture.Captured.Body) — nil for passthrough endpoints.
	// Content-based conditions type-switch on this.
	Body any
}
