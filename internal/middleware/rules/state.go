package rules

import (
	"net/http"
	"net/url"
)

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
}

// NewMutableState builds a MutableState seeded with the values
// routing + bodycapture had resolved by the time the rules middleware
// fires. The caller hands in the routed (provider, endpoint, params)
// tuple and the inbound headers to clone — mutations through this
// state are isolated to the per-request lifetime.
func NewMutableState(provider, endpoint string, pathParams map[string]string, inboundHeaders http.Header) *MutableState {
	params := make(map[string]string, len(pathParams))
	for k, v := range pathParams {
		params[k] = v
	}

	headers := make(http.Header, len(inboundHeaders))
	for k, vs := range inboundHeaders {
		cloned := make([]string, len(vs))
		copy(cloned, vs)
		headers[k] = cloned
	}

	return &MutableState{
		Provider:        provider,
		Endpoint:        endpoint,
		PathParams:      params,
		OutgoingHeaders: headers,
	}
}
