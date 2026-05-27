package rules

import (
	"net/http"
	"net/url"

	"github.com/andyjmorgan/sluice-gateway/internal/bodypatch"
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

	// MatchedPath is the un-prefixed accepted_paths value the router
	// matched the inbound request to. Used by the destination builder
	// as the upstream path template when the endpoint declares no
	// explicit Path — supports folding streaming and non-streaming
	// variants of the same wire shape into one endpoint. Untouched by
	// any action; a rule that retargets via ChangeUrlAction bypasses
	// this field entirely.
	MatchedPath string

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

	// Tags is the accumulated set of tags AddTagAction has attached
	// to this request. Set semantics — AddTag is a no-op for a tag
	// already present. Order reflects first-attach order so the SPA
	// can render chips deterministically. The reporter drains this
	// at OnComplete onto events.Request.Tags and bumps the
	// gateway.tags.applied.total counter once per tag.
	Tags []string

	// BodyRewrites accumulates body-patch operations recorded by the
	// rewriteField / removeField / appendField actions, in rule order.
	// BodyRewriteHandler applies them against the serialized request
	// body after the typed re-marshal step. Empty means nothing to do —
	// the common path costs nothing. Distinct from BodyMutated, which
	// tracks typed-body field mutation (changeModelName); a request can
	// have both, and the byte patches always apply last.
	BodyRewrites []bodypatch.Op

	// PolicyRef names the resilience policy the rules engine resolved
	// for this request. Set by UseResiliencePolicyAction; consumed
	// post-rules by the v1.2 orchestrator. Empty means "no policy
	// selected by rules" — the orchestrator may still fall back to the
	// configuration's default policy in that case. Multiple
	// useResiliencePolicy actions in a chain follow last-writer-wins
	// semantics, so a later rule can replace or clear (via empty
	// PolicyName) an earlier rule's selection.
	PolicyRef string
}

// AddTag appends t to Tags iff Tags does not already contain it.
// Returns true when the tag was newly added; false when it was
// already present. Trimmed-empty tags are silently ignored —
// AddTagAction validates non-empty input upstream so this branch is
// mostly defensive.
func (s *MutableState) AddTag(t string) bool {
	if s == nil {
		return false
	}
	if t == "" {
		return false
	}
	for _, existing := range s.Tags {
		if existing == t {
			return false
		}
	}
	s.Tags = append(s.Tags, t)
	return true
}

// HasTag reports whether Tags contains t. Used by TagCondition.
func (s *MutableState) HasTag(t string) bool {
	if s == nil {
		return false
	}
	for _, existing := range s.Tags {
		if existing == t {
			return true
		}
	}
	return false
}

// Clone returns a deep copy of s suitable for per-attempt mutation
// in the v1.2 resilience orchestrator. The clone is fully independent
// from the original — mutating one does not affect the other for any
// field exposed on MutableState.
//
// The typed request body pointer (carried on bodycapture.Captured.
// Body, not on MutableState directly) is NOT cloned here. For the
// single-target degenerate path (PR-6) this is irrelevant; for
// multi-target failover (PR-7+) the orchestrator is responsible for
// body restoration if per-target body mutations need to be isolated.
//
// nil receiver returns nil so the call site does not need a guard.
func (s *MutableState) Clone() *MutableState {
	if s == nil {
		return nil
	}
	out := &MutableState{
		Provider:    s.Provider,
		Endpoint:    s.Endpoint,
		MatchedPath: s.MatchedPath,
		BodyMutated: s.BodyMutated,
		PolicyRef:   s.PolicyRef,
	}
	if s.UpstreamURL != nil {
		u := *s.UpstreamURL
		out.UpstreamURL = &u
	}
	if s.OutgoingHeaders != nil {
		out.OutgoingHeaders = s.OutgoingHeaders.Clone()
	} else {
		out.OutgoingHeaders = make(http.Header)
	}
	if s.UpstreamCredentialOverride != nil {
		v := *s.UpstreamCredentialOverride
		out.UpstreamCredentialOverride = &v
	}
	if s.PathParams != nil {
		out.PathParams = make(map[string]string, len(s.PathParams))
		for k, v := range s.PathParams {
			out.PathParams[k] = v
		}
	}
	if len(s.QueryAdditions) > 0 {
		out.QueryAdditions = append([]QueryAddition(nil), s.QueryAdditions...)
	}
	if len(s.Tags) > 0 {
		out.Tags = append([]string(nil), s.Tags...)
	}
	if len(s.BodyRewrites) > 0 {
		out.BodyRewrites = append([]bodypatch.Op(nil), s.BodyRewrites...)
	}
	return out
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
func NewMutableState(provider, endpoint, matchedPath string, pathParams map[string]string, _ http.Header) *MutableState {
	params := make(map[string]string, len(pathParams))
	for k, v := range pathParams {
		params[k] = v
	}

	return &MutableState{
		Provider:        provider,
		Endpoint:        endpoint,
		MatchedPath:     matchedPath,
		PathParams:      params,
		OutgoingHeaders: make(http.Header),
	}
}
