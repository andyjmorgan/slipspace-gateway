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

// Evaluator runs rule evaluation on the gateway context. v1.0 always returns
// Outcome{Terminate: false} regardless of the configured rules.
type Evaluator struct {
	rules []contractsrules.RuleContract
}

// NewEvaluator constructs an Evaluator from the configured rule set. The
// slice is retained, not copied; callers must not mutate it concurrently.
func NewEvaluator(rules []contractsrules.RuleContract) *Evaluator {
	return &Evaluator{rules: rules}
}

// Rules returns the rule set the evaluator was constructed with. The slice is
// shared; callers must not mutate it.
func (e *Evaluator) Rules() []contractsrules.RuleContract { return e.rules }

// Evaluate iterates the configured rules in priority order. v1.0 short-circuits
// to a passthrough Outcome; the real engine lands in v1.1.
func (e *Evaluator) Evaluate(_ context.Context, _ GatewayContext) (contractsrules.Outcome, error) {
	return contractsrules.Outcome{Terminate: false}, nil
}

// GatewayContext is the minimal slice of request state a rule needs to
// inspect or mutate. v1.0 takes empty; v1.1 extends with decoded body access.
type GatewayContext struct {
	Provider string

	Endpoint string

	Model string

	Headers http.Header
}
