// Package rules holds the gateway's rule engine.
//
// The public schema lives in contracts/rules. This package owns
// evaluation: per-condition Matches, per-action Apply, the per-
// request middleware that drives the engine, and the body re-marshal
// step that re-serialises a mutated typed body before forwarding.
package rules

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/providers/anthropic/messages"
	"github.com/andyjmorgan/sluice-gateway/providers/gemini/content"
	openaichat "github.com/andyjmorgan/sluice-gateway/providers/openai/chat"
	openairesponses "github.com/andyjmorgan/sluice-gateway/providers/openai/responses"
)

// Evaluator runs the rule engine for a single request.
//
// Construction is config-load → NewEvaluator → long-lived shared
// instance. Evaluate is goroutine-safe — every per-request mutable
// piece (state, match buffer) is owned by the caller; the evaluator
// holds only read-only references to rules + meters + the depth cap.
type Evaluator struct {
	// perConfigRules maps configuration name to its priority-sorted
	// rule slice. Populated by config.Resolved.PerConfigurationRules;
	// looked up once per request.
	perConfigRules map[string][]*contractsrules.RuleContract

	// maxGroupDepth caps recursive RuleGroup descent. Sourced from
	// ServerEnv.RulesMaxGroupDepth; exceeding the cap fires
	// gateway.rule.errors.total{error_kind="group_depth"} and the
	// offending group evaluates to false.
	maxGroupDepth int

	// meters carries the OTel handles for rule-engine instruments.
	// nil-tolerant — callers (mostly tests) that don't wire telemetry
	// pass nil and the engine quietly skips emission.
	meters *observability.Meters
}

// NewEvaluator builds an Evaluator. The perConfigRules map is
// retained, not copied; callers must not mutate it concurrently.
func NewEvaluator(
	perConfigRules map[string][]*contractsrules.RuleContract,
	maxGroupDepth int,
	meters *observability.Meters,
) *Evaluator {
	if maxGroupDepth <= 0 {
		maxGroupDepth = 8
	}
	return &Evaluator{
		perConfigRules: perConfigRules,
		maxGroupDepth:  maxGroupDepth,
		meters:         meters,
	}
}

// Result is the engine's per-request evaluation summary handed to
// the middleware. SourceRule is non-nil only when Outcome.Terminate
// is true — it names the rule whose action short-circuited the
// pipeline, used by the middleware to compose X-Sluice-Synthetic
// and to record the synthetic status on the reporter.
type Result struct {
	// Outcome carries the terminating Response when set; zero-value
	// (Terminate=false, Response=nil) means evaluation completed
	// without a synthetic response.
	Outcome contractsrules.Outcome

	// SourceRule is the rule whose terminating action produced
	// Outcome. Nil when no terminating action fired.
	SourceRule *contractsrules.RuleContract
}

// Evaluate walks the rules attached to gc.ConfigurationName in
// priority-sorted order. On each matched rule the actions run in
// sequence, each Apply call gets the same MutableState handle so
// later actions observe earlier mutations.
//
// Behavior=Exit halts iteration after the current rule's actions
// run; it does NOT set Outcome.Terminate — that flag is reserved
// for terminating actions (returnStatusCode, llmImpersonation).
// The distinction matters for telemetry: "rule asked us to stop
// iterating" is not the same as "a synthetic response short-
// circuited the pipeline".
//
// MatchBuffer pulled from ctx receives one events.RuleMatched per
// matched rule. The forwarder's observer drains it at OnComplete.
func (e *Evaluator) Evaluate(
	ctx context.Context,
	gc GatewayContext,
	state *MutableState,
	body any,
) (Result, error) {
	if e == nil {
		return Result{}, nil
	}

	rules := e.perConfigRules[gc.ConfigurationName]
	start := time.Now()
	defer func() {
		if e.meters != nil && e.meters.RuleEvaluationDuration != nil {
			e.meters.RuleEvaluationDuration.Record(ctx,
				time.Since(start).Seconds(),
				metric.WithAttributes(attribute.String("configuration", gc.ConfigurationName)),
			)
		}
	}()

	if len(rules) == 0 {
		return Result{}, nil
	}

	buffer := MatchBufferFromContext(ctx)
	var result Result

	for _, rule := range rules {
		// Cascade: each rule's Condition sees the live state left by
		// every earlier rule's actions — not the frozen snapshot from
		// the start of Evaluate. liveGatewayContext derives Provider
		// / Endpoint / Model / Headers from state + body so a catch-
		// all rule keyed on a normalised model name fires regardless
		// of which upstream rule normalised it. Each rule still
		// matches at most once per request; the single-pass loop
		// bounds the cascade with no risk of oscillation.
		live := liveGatewayContext(gc, state, body)
		matched := matchCondition(rule.Condition, live, 0, e.maxGroupDepth, func() {
			e.recordError(ctx, rule, "group_depth")
		})
		if !matched {
			continue
		}

		applied, outcome, lastErr := e.applyRuleActions(ctx, rule, state, body)
		match := events.RuleMatched{
			RuleName:       rule.Name,
			Configuration:  gc.ConfigurationName,
			MatchedAt:      time.Now().UTC(),
			ActionsApplied: applied,
			Terminated:     outcome.Terminate,
		}
		if rule.ID != nil {
			match.RuleID = rule.ID.String()
		}
		if lastErr != nil {
			match.ErrorMessage = lastErr.Error()
		}
		buffer.Record(match)

		if e.meters != nil && e.meters.RuleMatchesTotal != nil {
			e.meters.RuleMatchesTotal.Add(ctx, 1, metric.WithAttributes(
				attribute.String("rule_name", rule.Name),
				attribute.String("rule_id", match.RuleID),
				attribute.Bool("terminated", outcome.Terminate),
				attribute.Int("action_count", len(applied)),
			))
		}

		if outcome.Terminate {
			result.Outcome = outcome
			result.SourceRule = rule
			break
		}

		if rule.Behavior == contractsrules.BehaviorExit {
			break
		}
	}

	return result, nil
}

// applyRuleActions runs each action in order. Each Apply call sees
// the cumulative MutableState from earlier actions. On error the
// loop continues — the .NET predecessor's silent-on-error behaviour
// is replaced with a metric increment + the error attached to the
// rule's events.RuleMatched payload, so operators can see WHICH
// action failed without scraping logs.
//
// A terminating action (returnStatusCode, llmImpersonation) stops
// the per-rule action loop immediately — subsequent actions on the
// same rule do NOT run, because the request is about to be
// short-circuited with a synthetic response. The terminating
// Outcome is returned for the caller to surface upstream.
func (e *Evaluator) applyRuleActions(
	ctx context.Context,
	rule *contractsrules.RuleContract,
	state *MutableState,
	body any,
) ([]string, contractsrules.Outcome, error) {
	applied := make([]string, 0, len(rule.Actions))
	var lastErr error
	var terminating contractsrules.Outcome
	for _, act := range rule.Actions {
		out, err := applyAction(act, state, body)
		if err != nil {
			e.recordError(ctx, rule, "action_apply")
			lastErr = err
			// Still record the action type — it ran, even if it
			// errored — so operators can see the intended sequence.
		}
		applied = append(applied, act.ActionType())
		if out.Terminate {
			terminating = out
			break
		}
	}
	return applied, terminating, lastErr
}

// recordError fires the rule-error counter with stable label
// cardinality (rule_name + rule_id + error_kind). The kind taxonomy
// is intentionally small to keep dashboards readable:
//   - "group_depth": a RuleGroup tree exceeded the configured cap.
//   - "action_apply": an Apply call returned an error.
//   - "body_remarshal" (PR 2 body re-marshal middleware).
func (e *Evaluator) recordError(ctx context.Context, rule *contractsrules.RuleContract, kind string) {
	if e.meters == nil || e.meters.RuleErrorsTotal == nil {
		return
	}
	ruleID := ""
	if rule.ID != nil {
		ruleID = rule.ID.String()
	}
	e.meters.RuleErrorsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("rule_name", rule.Name),
		attribute.String("rule_id", ruleID),
		attribute.String("error_kind", kind),
	))
}

// liveGatewayContext returns a GatewayContext that reflects the
// post-mutation state — Provider / Endpoint from state, Model from
// the live body pointer (or state.PathParams for Gemini's path-
// based model addressing), Headers merged from the inbound set and
// any SetHeader writes. Called once per rule iteration in Evaluate
// so each Condition sees the result of every prior rule's actions.
//
// ConfigurationName is never mutated; it propagates from the
// entry-time gc verbatim. Body propagates the live pointer so
// content-based conditions (none today, but conceivable for v1.2+)
// observe rule-driven body mutations without a deep copy.
//
// The merge order on Headers is inbound first, then OutgoingHeaders
// — a SetHeader/HeaderSet overwrites the inbound value, mirroring
// what the destination builder will send upstream.
func liveGatewayContext(initial GatewayContext, state *MutableState, body any) GatewayContext {
	gc := initial
	if state != nil {
		gc.Provider = state.Provider
		gc.Endpoint = state.Endpoint
		gc.Tags = state.Tags
	}
	gc.Body = body
	// BodyRaw propagates verbatim — it is the inbound bytes and rule
	// evaluation never mutates them (body patches apply post-rule).
	if model := liveModelName(state, body); model != "" {
		gc.Model = model
	}
	gc.Headers = mergeRuleHeaders(initial.Headers, headersFromState(state))
	return gc
}

// liveModelName mirrors the destination builder's outboundModel
// resolution order: PathParams first (Gemini's URL-templated model
// + the value ChangeModelNameAction writes there), then the typed
// body's Model field for the OpenAI/Anthropic shapes.
func liveModelName(state *MutableState, body any) string {
	if state != nil {
		if v := strings.TrimSpace(state.PathParams["model"]); v != "" {
			return v
		}
	}
	switch b := body.(type) {
	case *openaichat.ChatCompletionRequest:
		return strings.TrimSpace(b.Model)
	case *openairesponses.ResponsesRequest:
		return strings.TrimSpace(b.Model)
	case *messages.MessagesRequest:
		return strings.TrimSpace(b.Model)
	case *content.GenerateContentRequest:
		// Gemini carries the model on the URL path, not the body —
		// the PathParams branch above handles it.
		return ""
	}
	return ""
}

func headersFromState(state *MutableState) http.Header {
	if state == nil {
		return nil
	}
	return state.OutgoingHeaders
}

// mergeRuleHeaders combines the inbound header set with any
// SetHeader writes the rule engine has accumulated. Outgoing values
// win on key collision; inbound values pass through unmodified
// otherwise.
func mergeRuleHeaders(inbound, outgoing http.Header) http.Header {
	if len(inbound) == 0 && len(outgoing) == 0 {
		return nil
	}
	merged := make(http.Header, len(inbound)+len(outgoing))
	for k, vs := range inbound {
		copied := make([]string, len(vs))
		copy(copied, vs)
		merged[k] = copied
	}
	for k, vs := range outgoing {
		copied := make([]string, len(vs))
		copy(copied, vs)
		merged[k] = copied
	}
	return merged
}

// GatewayContext is the read-only slice of request state a rule's
// Condition inspects. Actions mutate via MutableState; splitting
// the read/write surfaces keeps Condition implementations honest.
//
// Within Evaluate the gc handed to each Condition is rebuilt per
// iteration via liveGatewayContext so the cascade semantic holds —
// Provider, Endpoint, Model, and Headers always reflect the state
// every prior rule left behind.
type GatewayContext struct {
	// Provider is the routed upstream provider name (e.g. "openai").
	Provider string

	// Endpoint is the routed endpoint name under that provider (e.g.
	// "chat_completions").
	Endpoint string

	// Model is the model identifier the client requested, read from
	// the routing match or the decoded body. Empty for endpoints
	// that carry no model.
	Model string

	// Headers is the inbound request header set. HeaderCondition
	// reads from this; rule-driven mutations land on
	// MutableState.OutgoingHeaders.
	Headers http.Header

	// ConfigurationName is the resolved configuration the request
	// was authorised under. The evaluator uses this to look up the
	// applicable rule slice; the rule-matched event records it for
	// downstream subscribers.
	ConfigurationName string

	// Body is the typed decoded request body when the route carries
	// one (bodycapture.Captured.Body) — nil for passthrough
	// endpoints. Content-based conditions type-switch on this.
	Body any

	// BodyRaw is the verbatim inbound request body bytes
	// (bodycapture.Captured.Raw). BodyFieldCondition reads from this
	// via gjson — the raw bytes, not the typed body, so unknown fields
	// and exact shapes are visible. Reflects the inbound body; rule-
	// driven typed mutations (changeModelName) are not re-serialised
	// into it, which is fine because model matching has its own
	// ModelNameCondition.
	BodyRaw []byte

	// Tags is the set of tags AddTagAction has attached to this
	// request via earlier rules. TagCondition reads from here.
	// Rebuilt per iteration via liveGatewayContext, so the cascade
	// semantic holds — a rule sees only the tags its predecessors
	// attached.
	Tags []string
}
