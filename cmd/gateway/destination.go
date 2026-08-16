package main

import (
	"net/http"
	"net/url"
	"strings"

	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/slipspace-gateway/internal/proxy"
	"github.com/andyjmorgan/slipspace-gateway/internal/selection"
)

// groupToResilienceConfig synthesises the resilience orchestrator's input from
// a selected v2 group. The orchestrator is reused unchanged: each v2 target
// becomes a ResilienceTarget whose per-attempt Actions switch the provider
// (state.Provider, re-resolved by the final handler) and rewrite the body model
// to the per-target alias. Order is preserved for failover; load_balance
// ignores it.
func groupToResilienceConfig(name string, g selection.Group) contractsres.ResilienceConfig {
	targets := make([]contractsres.ResilienceTarget, 0, len(g.Targets))
	for i, t := range g.Targets {
		weight := t.Weight
		if weight == 0 {
			weight = 1 // even weighting when unset
		}
		targets = append(targets, contractsres.ResilienceTarget{
			Name:     t.Provider,
			Provider: t.Provider,
			Order:    i + 1,
			Weight:   weight,
			Actions:  providerSwitchActions(t.Provider, t.Alias),
		})
	}
	return contractsres.ResilienceConfig{
		Name:                         name,
		Mode:                         g.Mode,
		FailureStatusCodes:           g.FailureStatusCodes,
		CircuitBreaker:               g.CircuitBreaker,
		StrictWeights:                g.StrictWeights,
		ResponseHeaderTimeoutSeconds: g.ResponseHeaderTimeoutSeconds,
		Targets:                      targets,
	}
}

// singleTargetConfig synthesises a degenerate ModeNone policy carrying one
// target, so a single-provider binding flows through the same orchestrator path
// as a group: the provider switch + body alias are applied once, before the
// body re-marshal step, and the final handler re-resolves the provider. This is
// what lets a single binding carry a model alias (e.g. foundry-model → the
// upstream deployment name) without a bespoke pre-forward rewrite stage.
func singleTargetConfig(t selection.Target) contractsres.ResilienceConfig {
	return contractsres.ResilienceConfig{
		Name: "binding:" + t.Provider,
		Mode: contractsres.ModeNone,
		Targets: []contractsres.ResilienceTarget{{
			Name:     t.Provider,
			Provider: t.Provider,
			Order:    1,
			Actions:  providerSwitchActions(t.Provider, t.Alias),
		}},
	}
}

// providerSwitchActions builds the internal action pair the orchestrator applies
// per attempt: changeProvider switches state.Provider to the provider (the final
// handler re-resolves transport from it), and changeModelName rewrites the body
// model to the alias when one is set. These two action types survive only as
// internal selection primitives — they are no longer authorable in rules.
func providerSwitchActions(provider, alias string) []contractsrules.Action {
	acts := []contractsrules.Action{&contractsrules.ChangeProviderAction{NewProvider: provider}}
	if alias != "" {
		acts = append(acts, &contractsrules.ChangeModelNameAction{NewModelName: alias})
	}
	return acts
}

// buildDestination resolves the upstream destination for a v2 request from
// the selected target plus the auth decision. It is the single credential mint
// site under v2 — the resolved selection.Target already carries the provider's
// base URL, protocol path, auth convention, default query, and the
// configuration's credential, so there is no provider/endpoint lookup and no
// changeProvider/changeUrl/changeApiKey override table.
//
// Credential resolution (see resolveCredentialHeaders for the full table):
//
//	changeApiKey literal override → mint the override key with the post-rule
//	    provider's header format; drop every other credential header.
//	changeApiKey UseSlipSpaceKey override → forward the inbound Authorization
//	    verbatim, stripping nothing (passthrough-style on a managed config).
//	passthrough mode → forward the inbound Authorization verbatim.
//	managed mode, credential non-empty → set target.Auth header (or the
//	    per-protocol default when Auth is nil) to the formatted credential;
//	    drop every other inbound credential header so a managed→other forward
//	    never leaks the inbound token.
//	managed mode, credential empty → strip all credential headers, set none
//	    (no-credential provider, e.g. an unauthenticated in-cluster ollama).
//
// override is the post-rule MutableState.UpstreamCredentialOverride: nil leaves
// the mode default intact, non-nil non-empty substitutes the literal key, and
// the empty-string sentinel forwards the inbound bearer. Threading it here
// keeps this the single credential mint site (invariant #6) — no middleware
// mints the header itself.
//
// pathParams carries the named substitutions for the path template (Gemini's
// {model}/{op}); the body-model aliasing for the body-keyed protocols is handled
// by the body-rewrite stage, not here.
func buildDestination(
	target selection.Target,
	pathParams map[string]string,
	mode auth.Mode,
	dropHeaders []string,
	inboundAuthorization string,
	override *string,
) (proxy.Destination, error) {
	base, err := url.Parse(target.BaseURL)
	if err != nil {
		return proxy.Destination{}, err
	}

	upstream := *base
	upstreamPath := substitutePlaceholders(target.Path, pathParams)
	upstream.Path = joinPaths(base.Path, upstreamPath)
	upstream.RawPath = ""

	if len(target.Query) > 0 {
		q := upstream.Query()
		for k, v := range target.Query {
			q.Set(k, v)
		}
		upstream.RawQuery = q.Encode()
	}

	outgoing := http.Header{}
	for k, v := range target.RequiredHeaders {
		outgoing.Set(k, v)
	}

	drops := append([]string(nil), dropHeaders...)
	drops = resolveCredentialHeaders(outgoing, drops, target, mode, override, inboundAuthorization)

	return proxy.Destination{
		BaseURL:         base,
		UpstreamURL:     &upstream,
		OutgoingHeaders: outgoing,
		DropHeaders:     drops,
	}, nil
}

// resolveCredentialHeaders applies the upstream credential onto outgoing and
// returns the updated drop set. It is the single mint site for upstream
// credentials (invariant #6); both the generative and passthrough destination
// builders funnel through it so the per-(provider, protocol) header format lives
// in exactly one place.
//
// The post-rule changeApiKey override takes precedence over the auth mode, since
// rules win the last word on the wire:
//
//	override != nil && non-empty → mint the literal key with the post-rule
//	    provider's header format and drop every other credential header.
//	override != nil && empty (UseSlipSpaceKey sentinel) → forward the inbound
//	    Authorization verbatim; strip nothing.
//	override nil, passthrough mode → forward the inbound Authorization verbatim.
//	override nil, managed mode, credential non-empty → mint target.Credential,
//	    drop every other credential header.
//	override nil, managed mode, credential empty → strip all credential headers
//	    (no-credential provider).
func resolveCredentialHeaders(
	outgoing http.Header,
	drops []string,
	target selection.Target,
	mode auth.Mode,
	override *string,
	inboundAuthorization string,
) []string {
	switch {
	case override != nil && *override != "":
		// changeApiKey literal: mint the override with the post-rule
		// provider's header format, drop the rest.
		name, value := credentialHeaderFor(target, *override)
		outgoing.Set(name, value)
		drops = dropOtherCredentialHeaders(drops, name)
	case override != nil:
		// changeApiKey UseSlipSpaceKey sentinel (empty string): forward the
		// inbound bearer verbatim, stripping nothing.
		if inboundAuthorization != "" {
			outgoing.Set(auth.HeaderAuthorization, inboundAuthorization)
		}
	case mode == auth.ModePassthrough:
		if inboundAuthorization != "" {
			outgoing.Set(auth.HeaderAuthorization, inboundAuthorization)
		}
	case target.Credential == "":
		// No-credential provider: strip everything, set nothing.
		for _, h := range credentialHeaderNames {
			drops = appendUnique(drops, h)
		}
	default:
		name, value := credentialHeaderFor(target, target.Credential)
		outgoing.Set(name, value)
		drops = dropOtherCredentialHeaders(drops, name)
	}
	return drops
}

// dropOtherCredentialHeaders adds every credential header in the closed set
// EXCEPT minted to drops, so an inbound Bearer/x-api-key cannot leak
// cross-provider while the header we just set survives.
//
// The comparison canonicalises both sides. credentialHeaderNames is written in
// canonical case ("X-Api-Key") while auth.UpstreamCredentialHeader returns the
// lowercase wire literals ("x-api-key", "x-goog-api-key"), so an exact-string
// compare never matched for anthropic or gemini and added the just-minted
// header to its own drop list. That is benign only because the forwarder
// applies DropHeaders before OutgoingHeaders — an ordering that is
// load-bearing by accident. Any reordering, or a second consumer of
// Destination.DropHeaders, would strip the upstream credential and 401 every
// managed anthropic/gemini request.
func dropOtherCredentialHeaders(drops []string, minted string) []string {
	canonical := http.CanonicalHeaderKey(minted)
	for _, h := range credentialHeaderNames {
		if http.CanonicalHeaderKey(h) == canonical {
			continue
		}
		drops = appendUnique(drops, h)
	}
	return drops
}

// credentialHeaderFor returns the (header, value) for a managed-mode credential
// on target, using the provider's per-protocol auth convention when present and
// falling back to the per-provider-name default otherwise. credential is the
// value substituted into the format — target.Credential for the managed default,
// or a changeApiKey override key.
func credentialHeaderFor(target selection.Target, credential string) (string, string) {
	if target.Auth == nil || target.Auth.Header == "" {
		return auth.UpstreamCredentialHeader(target.Provider, credential)
	}
	if target.Auth.Format == "" {
		return target.Auth.Header, credential
	}
	return target.Auth.Header, strings.ReplaceAll(target.Auth.Format, authFormatPlaceholder, credential)
}
