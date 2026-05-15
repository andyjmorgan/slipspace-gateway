package rules

import (
	"net/http"
	"net/url"
)

// QueryAddition is a single (key, value) pair from
// AppendQueryStringAction. Stored on MutableState until the
// destination builder resolves UpstreamURL; deferring the apply
// avoids requiring the destination to be finalised at action time.
type QueryAddition struct {
	Key string

	Value string
}

// MutableState is the rule engine's write-side handle to per-request
// state. Action implementations call methods on this value (or assign
// to its fields) to mutate the destination the request will reach.
// The destination builder consumes the final state after evaluation
// and renders headers + URL + upstream credential from it; nothing
// else in the pipeline writes here.
//
// The split between MutableState and GatewayContext is deliberate.
// Conditions only need to read; actions need to write. Two surfaces,
// two responsibilities, no risk of a condition implementation
// accidentally mutating destination state.
type MutableState struct {
	// Provider is the upstream provider the request will be sent to.
	// Initialised from routing; ChangeProviderAction overwrites it.
	// Re-resolution of UpstreamCredential against the new provider
	// happens in the destination builder, not here.
	Provider string

	// Endpoint is the endpoint name under Provider. Initialised from
	// routing. v1.0.1 actions do not write this directly; provider
	// changes that need a different endpoint name are paired with a
	// ChangeUrlAction the rule author writes explicitly.
	Endpoint string

	// UpstreamURL is the post-rule destination URL the forwarder will
	// dial. nil means "let the destination builder resolve from
	// (Provider, Endpoint, PathParams)". ChangeUrlAction sets this to
	// a verbatim override.
	UpstreamURL *url.URL

	// OutgoingHeaders is the header set the destination builder seeds
	// onto the upstream request. SetHeaderAction mutates this map in
	// place. The destination builder layers provider-required headers
	// + auth-header binding on top after evaluation; rules that need
	// to override the auth header should set it here, not rely on the
	// builder's defaults.
	OutgoingHeaders http.Header

	// UpstreamCredentialOverride, when non-nil, replaces the credential
	// the destination builder would otherwise pull from
	// Configuration.UpstreamCredentials[Provider]. Written by
	// ChangeApiKeyAction; nil leaves managed-mode lookup intact.
	UpstreamCredentialOverride *string

	// PathParams carries the named substitutions for the endpoint's
	// path template (e.g. {model} for Gemini). Initialised from
	// routing's Match.Params. ChangeModelNameAction updates the
	// "model" key here so the path template re-renders correctly when
	// the typed body also gets mutated.
	PathParams map[string]string

	// BodyMutated is set by any action that writes through the typed
	// body pointer carried on bodycapture.Captured.Body. The body
	// re-marshal middleware reads this flag to decide whether to
	// re-encode and replace r.Body before forwarding — skipped when
	// false so the unchanged path costs nothing.
	BodyMutated bool

	// QueryAdditions accumulates AppendQueryStringAction deltas. The
	// destination builder applies them after UpstreamURL is resolved
	// so the action does not depend on the URL being known at rule-
	// evaluation time. Order is preserved; duplicates are allowed.
	QueryAdditions []QueryAddition
}

// NewMutableState builds a MutableState seeded with the routing
// resolution (provider, endpoint, path params). OutgoingHeaders
// starts EMPTY — the destination builder layers provider-required
// + auth-resolved headers on top of the auth defaults, and rule-set
// values overlay those. The inbound header set is read-only and
// reaches conditions via GatewayContext.Headers.
//
// inboundHeaders is intentionally unused beyond signalling the
// caller's intent; the parameter is retained on the public API so
// future evolutions (header echoing, audit) can opt in without a
// signature break.
func NewMutableState(provider, endpoint string, pathParams map[string]string, _ http.Header) *MutableState {
	params := make(map[string]string, len(pathParams))
	for k, v := range pathParams {
		params[k] = v
	}

	return &MutableState{
		Provider:        provider,
		Endpoint:        endpoint,
		PathParams:      params,
		OutgoingHeaders: make(http.Header),
	}
}
