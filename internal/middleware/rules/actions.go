package rules

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/bodypatch"
	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
	"github.com/andyjmorgan/slipspace-gateway/protocols/gemini/content"
	openaichat "github.com/andyjmorgan/slipspace-gateway/protocols/openai/chat"
	openairesponses "github.com/andyjmorgan/slipspace-gateway/protocols/openai/responses"
)

// ErrUnknownModelField is returned by ChangeModelNameAction when the
// rule fires against a typed body the engine doesn't know how to
// mutate (or against a passthrough endpoint where Body is nil).
var ErrUnknownModelField = errors.New("rules: cannot set model on this body type")

// ApplyAction is the exported entry point for any caller that needs
// to apply a rule action to a MutableState — currently the rules
// middleware and the v1.2 resilience orchestrator. The dispatch logic
// is the unexported applyAction; this wrapper exists so contracts/
// rules.Action values can flow through the same machinery from
// outside the package without re-implementing the type switch.
//
// body is the typed request body (bodycapture.Captured.Body) when
// available; nil otherwise. Actions that need body mutation
// (changeModelName) tolerate nil by skipping the body write and
// only updating PathParams.
func ApplyAction(
	act contractsrules.Action,
	state *MutableState,
	body any,
) (contractsrules.Outcome, error) {
	return applyAction(act, state, body)
}

// applyAction dispatches an action to its evaluator. The terminating
// actions (returnStatusCode, llmImpersonation) are dispatched here too
// and signal termination through Outcome.Terminate. UnknownAction
// returns a passthrough Outcome for forward-compat with action types
// this build does not yet model.
func applyAction(
	act contractsrules.Action,
	state *MutableState,
	body any,
) (contractsrules.Outcome, error) {
	if act == nil {
		return contractsrules.Outcome{}, nil
	}
	switch a := act.(type) {
	case *contractsrules.ChangeProviderAction:
		return applyChangeProvider(*a, state)
	case *contractsrules.TranslateAction:
		return applyTranslate(*a, state)
	case *contractsrules.ChangeModelNameAction:
		return applyChangeModelName(*a, state, body)
	case *contractsrules.ChangeUrlAction:
		return applyChangeUrl(*a, state)
	case *contractsrules.ChangeApiKeyAction:
		return applyChangeApiKey(*a, state)
	case *contractsrules.SetHeaderAction:
		return applySetHeader(*a, state)
	case *contractsrules.AppendQueryStringAction:
		return applyAppendQueryString(*a, state)
	case *contractsrules.ReturnStatusCodeAction:
		return applyReturnStatusCode(*a)
	case *contractsrules.LlmImpersonationAction:
		return applyLlmImpersonation(*a)
	case *contractsrules.AddTagAction:
		return applyAddTag(*a, state)
	case *contractsrules.UseResiliencePolicyAction:
		return applyUseResiliencePolicy(*a, state)
	case *contractsrules.RewriteFieldAction:
		return applyRewriteField(*a, state)
	case *contractsrules.RemoveFieldAction:
		return applyRemoveField(*a, state)
	case *contractsrules.AppendFieldAction:
		return applyAppendField(*a, state)
	case *contractsrules.UnknownAction:
		return contractsrules.Outcome{}, nil
	default:
		// Unmodelled action types round-trip via DynamicProperties
		// and no-op here — forward-compat for control-plane-minted
		// types this build does not yet implement.
		return contractsrules.Outcome{}, nil
	}
}

func applyChangeProvider(a contractsrules.ChangeProviderAction, state *MutableState) (contractsrules.Outcome, error) {
	v := strings.TrimSpace(a.NewProvider)
	if v == "" {
		return contractsrules.Outcome{}, fmt.Errorf("rules: changeProvider: %w", errEmptyValue)
	}
	state.Provider = v
	return contractsrules.Outcome{}, nil
}

// applyTranslate marks the request for cross-provider translation. It records
// the inbound protocol in SourceProtocol the first time so the response leg
// knows what to translate the upstream reply back into, then overwrites
// Protocol with the target so the destination builder resolves the upstream
// endpoint under the target protocol. A second translate in the same chain
// only moves the target; the recorded source is never overwritten. The
// destination builder treats target == source as a no-op and is the sole site
// that fails closed when no translator is registered for the pair.
func applyTranslate(a contractsrules.TranslateAction, state *MutableState) (contractsrules.Outcome, error) {
	target := strings.TrimSpace(a.TargetProtocol)
	if target == "" {
		return contractsrules.Outcome{}, fmt.Errorf("rules: translate: %w", errEmptyValue)
	}
	if state.SourceProtocol == "" {
		state.SourceProtocol = state.Protocol
	}
	state.Protocol = target
	return contractsrules.Outcome{}, nil
}

// applyChangeModelName writes the new model name into the typed
// request body. Each supported body type exposes Model as a plain
// string field — a type-switch is enough, no interface needed for
// this single call site. Updating PathParams["model"] too keeps
// Gemini's path-template path-params re-render consistent with the
// new body.
//
// state.BodyMutated is set so the body re-marshal middleware
// re-encodes the typed body to bytes the forwarder will send.
func applyChangeModelName(a contractsrules.ChangeModelNameAction, state *MutableState, body any) (contractsrules.Outcome, error) {
	newName := strings.TrimSpace(a.NewModelName)
	if newName == "" {
		return contractsrules.Outcome{}, fmt.Errorf("rules: changeModelName: %w", errEmptyValue)
	}

	switch b := body.(type) {
	case *openaichat.ChatCompletionRequest:
		b.Model = newName
	case *openairesponses.ResponsesRequest:
		b.Model = newName
	case *messages.MessagesRequest:
		b.Model = newName
	case *content.GenerateContentRequest:
		// Gemini carries the model only on the URL path, not the body;
		// PathParams update below handles it.
	case nil:
		// passthrough endpoints have no typed body — model-changing on
		// such a route is a no-op against the body. Still update
		// PathParams in case the path template uses {model}.
	default:
		return contractsrules.Outcome{}, fmt.Errorf("rules: changeModelName: %T: %w", body, ErrUnknownModelField)
	}

	if state.PathParams == nil {
		state.PathParams = make(map[string]string)
	}
	state.PathParams["model"] = newName
	state.BodyMutated = true
	return contractsrules.Outcome{}, nil
}

func applyChangeUrl(a contractsrules.ChangeUrlAction, state *MutableState) (contractsrules.Outcome, error) {
	v := strings.TrimSpace(a.NewURL)
	if v == "" {
		return contractsrules.Outcome{}, fmt.Errorf("rules: changeUrl: %w", errEmptyValue)
	}
	parsed, err := url.Parse(v)
	if err != nil {
		return contractsrules.Outcome{}, fmt.Errorf("rules: changeUrl: parse %q: %w", v, err)
	}
	state.UpstreamURL = parsed
	return contractsrules.Outcome{}, nil
}

// applyChangeApiKey records the changeApiKey override on state for the
// destination builder to mint at the single credential mint site
// (cmd/gateway/destination.go::resolveCredentialHeaders, invariant #6); it
// never mints a header here. UseSlipSpaceKey stores the empty-string sentinel so
// the builder forwards the inbound bearer verbatim; otherwise the trimmed
// literal APIKey is stored for the builder to substitute with the post-rule
// provider's header format. An empty literal is rejected.
func applyChangeApiKey(a contractsrules.ChangeApiKeyAction, state *MutableState) (contractsrules.Outcome, error) {
	if a.UseSlipSpaceKey {
		// Sentinel: empty-string override means "forward the inbound
		// bearer verbatim" — the destination builder resolves it against
		// the request's Authorization header and strips nothing.
		empty := ""
		state.UpstreamCredentialOverride = &empty
		return contractsrules.Outcome{}, nil
	}
	v := strings.TrimSpace(a.APIKey)
	if v == "" {
		return contractsrules.Outcome{}, fmt.Errorf("rules: changeApiKey: %w", errEmptyValue)
	}
	state.UpstreamCredentialOverride = &v
	return contractsrules.Outcome{}, nil
}

// applySetHeader mutates state.OutgoingHeaders per HeaderAction.
// Append/Prepend join with ", " — RFC 7230 §3.2.2 specifies that
// multi-value headers concatenate via comma when serialised to the
// wire. The .NET predecessor concatenated without a separator (so
// "a"+"b" = "ab"), which we deliberately diverge from. Append and
// Prepend create the header when missing so they're symmetric with
// Set; the .NET behaviour of "silently no-op on Append-to-missing"
// was a footgun.
func applySetHeader(a contractsrules.SetHeaderAction, state *MutableState) (contractsrules.Outcome, error) {
	name := strings.TrimSpace(a.HeaderName)
	if name == "" {
		return contractsrules.Outcome{}, fmt.Errorf("rules: setHeader: %w", errEmptyValue)
	}
	if state.OutgoingHeaders == nil {
		state.OutgoingHeaders = make(map[string][]string)
	}

	switch a.HeaderAction {
	case contractsrules.HeaderSet:
		state.OutgoingHeaders.Set(name, a.HeaderValue)
	case contractsrules.HeaderRemove:
		state.OutgoingHeaders.Del(name)
	case contractsrules.HeaderAppend:
		existing := state.OutgoingHeaders.Get(name)
		if existing == "" {
			state.OutgoingHeaders.Set(name, a.HeaderValue)
		} else {
			state.OutgoingHeaders.Set(name, existing+", "+a.HeaderValue)
		}
	case contractsrules.HeaderPrepend:
		existing := state.OutgoingHeaders.Get(name)
		if existing == "" {
			state.OutgoingHeaders.Set(name, a.HeaderValue)
		} else {
			state.OutgoingHeaders.Set(name, a.HeaderValue+", "+existing)
		}
	default:
		return contractsrules.Outcome{}, fmt.Errorf("rules: setHeader: unknown header_action %q", a.HeaderAction)
	}
	return contractsrules.Outcome{}, nil
}

// applyAppendQueryString accumulates a (key, value) pair on
// state.QueryAdditions. The destination builder applies the
// accumulated deltas after UpstreamURL is resolved — this lets the
// action fire regardless of whether ChangeUrl has run yet (rule
// order is operator-authored, not engine-imposed).
//
// Duplicates are allowed: an upstream API that interprets repeated
// keys as a list (?tag=a&tag=b) will see both values in the order
// the rules appended them.
func applyAppendQueryString(a contractsrules.AppendQueryStringAction, state *MutableState) (contractsrules.Outcome, error) {
	key := strings.TrimSpace(a.Key)
	if key == "" {
		return contractsrules.Outcome{}, fmt.Errorf("rules: appendQueryString: %w", errEmptyValue)
	}
	state.QueryAdditions = append(state.QueryAdditions, QueryAddition{Key: key, Value: a.Value})
	return contractsrules.Outcome{}, nil
}

// errEmptyValue is the common sentinel for "the rule arrived with a
// required string field empty".
//
// This is the only check for most actions, not a second line of defence.
// Only the translate and body-rewrite actions implement the contract
// package's optional validate() hook, so a rule with an empty tag,
// header name, provider, api-key, query key, or impersonation message
// loads clean and fails here — per request, on every match — rather
// than at config load. Widening load-time validation is tracked
// separately; until then an authoring mistake surfaces at request time.
var errEmptyValue = errors.New("rules: required value is empty")

// applyLlmImpersonation is TERMINATING. v1.0.1 ships a stub
// implementation: it short-circuits the pipeline and writes the
// rule's Message as a plain-text response. The full design
// (per-provider response-shape synthesisers — chat.completion /
// response / message / candidates with streaming variants) is
// deferred to v1.0.3+ once the rest of the engine is mileage-tested
// in production.
//
// The stub is deliberately honest: an operator pointing the OpenAI
// SDK at the gateway and triggering an llmImpersonation rule sees a
// text/plain response with the configured message, NOT a fake
// chat.completion JSON shape. That makes the stubbed nature
// obvious in monitoring and keeps SDK round-tripping from masking
// the deferral with subtly-wrong output.
//
// Empty Message returns errEmptyValue. Nothing rejects it earlier —
// LlmImpersonationAction implements no load-time validate() hook — so
// this is the sole enforcement point and it fires per request.
func applyLlmImpersonation(a contractsrules.LlmImpersonationAction) (contractsrules.Outcome, error) {
	msg := strings.TrimSpace(a.Message)
	if msg == "" {
		return contractsrules.Outcome{}, fmt.Errorf("rules: llmImpersonation: %w", errEmptyValue)
	}
	return contractsrules.Outcome{
		Terminate: true,
		Response: &contractsrules.Response{
			StatusCode: 200,
			Body:       []byte(msg),
			BodyType:   contractsrules.StatusBodyText,
		},
	}, nil
}

// applyAddTag attaches a.Tag to state's tag set. Idempotent — adding
// a tag that's already present is a no-op (set semantics). Empty
// Tag returns errEmptyValue so a misconfigured rule fails loudly
// at evaluate time rather than silently doing nothing. Nothing rejects
// it earlier — AddTagAction implements no load-time validate() hook —
// so this is the sole enforcement point and it fires per request (see
// errEmptyValue).
func applyAddTag(a contractsrules.AddTagAction, state *MutableState) (contractsrules.Outcome, error) {
	t := strings.TrimSpace(a.Tag)
	if t == "" {
		return contractsrules.Outcome{}, fmt.Errorf("rules: addTag: %w", errEmptyValue)
	}
	state.AddTag(t)
	return contractsrules.Outcome{}, nil
}

// applyRewriteField records a body set/replace operation on state. The
// target is parsed (validated at config-load, re-parsed here) and the
// resolved Op is appended to state.BodyRewrites for BodyRewriteHandler
// to apply against the serialized body after evaluation. The action
// itself does not touch the body — it queues the mutation so it lands
// once, on the final bytes, after any typed re-marshal.
func applyRewriteField(a contractsrules.RewriteFieldAction, state *MutableState) (contractsrules.Outcome, error) {
	return recordBodyOp(state, bodypatch.OpSet, a.ActionType(), a.Target, a.Value)
}

// applyRemoveField records a body delete operation on state.
func applyRemoveField(a contractsrules.RemoveFieldAction, state *MutableState) (contractsrules.Outcome, error) {
	return recordBodyOp(state, bodypatch.OpRemove, a.ActionType(), a.Target, contractsrules.RewriteValue{})
}

// applyAppendField records a body array-append operation on state.
func applyAppendField(a contractsrules.AppendFieldAction, state *MutableState) (contractsrules.Outcome, error) {
	return recordBodyOp(state, bodypatch.OpAppend, a.ActionType(), a.Target, a.Value)
}

// recordBodyOp parses a write target and queues the bodypatch.Op on the
// phase-appropriate slot: request.body.* targets land on BodyRewrites
// (applied on the request path), response.body.* targets on
// ResponseRewrites (applied by the forwarder's ModifyResponse hook).
func recordBodyOp(state *MutableState, kind bodypatch.OpKind, actionType, target string, value contractsrules.RewriteValue) (contractsrules.Outcome, error) {
	t, err := contractsrules.ParseTarget(target)
	if err != nil {
		return contractsrules.Outcome{}, fmt.Errorf("rules: %s: %w", actionType, err)
	}
	op := bodypatch.Op{Kind: kind, Path: t.Path, Value: value, ActionType: actionType}
	if t.Scope == contractsrules.ScopeResponseBody {
		state.ResponseRewrites = append(state.ResponseRewrites, op)
	} else {
		state.BodyRewrites = append(state.BodyRewrites, op)
	}
	return contractsrules.Outcome{}, nil
}

// applyUseResiliencePolicy writes a.PolicyName into state.PolicyRef.
// Non-terminating; last writer wins when multiple rules in a chain
// invoke this action.
//
// The write is INERT under v2: selection stashes the binding-derived
// ResilienceConfig on the request context and the orchestrator prefers
// that over state.PolicyRef, while the gateway wires the by-name
// PolicyLookup to nil. Nothing validates PolicyName either — v2 has no
// `resilience_policies:` library to cross-check it against — so an
// unknown name is a silent no-op rather than a startup error. Trimming
// is permissive for that reason: there is no real policy to resolve.
func applyUseResiliencePolicy(a contractsrules.UseResiliencePolicyAction, state *MutableState) (contractsrules.Outcome, error) {
	state.PolicyRef = strings.TrimSpace(a.PolicyName)
	return contractsrules.Outcome{}, nil
}

// applyReturnStatusCode is TERMINATING. It returns an Outcome with
// Terminate=true and a Response carrying the configured status,
// body, and body-type — the rules middleware writes the synthetic
// response to the client and short-circuits the pipeline before the
// forwarder runs.
//
// StatusCode must be in [100, 599]; values outside that band fall
// back to 500 so a misconfigured rule produces a recognisably-bad
// response rather than a panic in net/http.
func applyReturnStatusCode(a contractsrules.ReturnStatusCodeAction) (contractsrules.Outcome, error) {
	status := a.StatusCode
	if status < 100 || status > 599 {
		status = 500
	}
	return contractsrules.Outcome{
		Terminate: true,
		Response: &contractsrules.Response{
			StatusCode: status,
			Body:       []byte(a.Body),
			BodyType:   a.BodyType,
		},
	}, nil
}
