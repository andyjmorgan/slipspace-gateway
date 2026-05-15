package rules

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/providers/anthropic/messages"
	"github.com/andyjmorgan/sluice-gateway/providers/gemini/content"
	openaichat "github.com/andyjmorgan/sluice-gateway/providers/openai/chat"
	openairesponses "github.com/andyjmorgan/sluice-gateway/providers/openai/responses"
)

// ErrUnknownModelField is returned by ChangeModelNameAction when the
// rule fires against a typed body the engine doesn't know how to
// mutate (or against a passthrough endpoint where Body is nil).
var ErrUnknownModelField = errors.New("rules: cannot set model on this body type")

// applyAction dispatches a non-terminating action to its evaluator.
// Terminating actions (returnStatusCode, llmImpersonation) ship in
// later PRs; UnknownAction returns a passthrough Outcome for
// forward-compat with control-plane-minted action types this build
// does not yet model.
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
	case *contractsrules.UnknownAction:
		return contractsrules.Outcome{}, nil
	default:
		// LlmImpersonation lands in a later PR. Until then, treat
		// unmodelled action types as passthrough so YAML authored
		// against the eventual schema doesn't break the engine.
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

func applyChangeApiKey(a contractsrules.ChangeApiKeyAction, state *MutableState) (contractsrules.Outcome, error) {
	if a.UseSluiceKey {
		// Sentinel: empty-string override means "forward the inbound
		// bearer verbatim" — the destination builder handles the
		// resolution against the request's Authorization header.
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
// required string field empty". The contract package's Validate
// catches most of these at load time; this is the runtime
// belt-and-braces against an UnknownAction that round-tripped
// through DynamicProperties with a missing field.
var errEmptyValue = errors.New("rules: required value is empty")

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
