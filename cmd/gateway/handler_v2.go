package main

import (
	"net/http"
	"net/url"
	"strings"

	contractsres "github.com/andyjmorgan/sluice-gateway/contracts/resilience"
	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
	"github.com/andyjmorgan/sluice-gateway/internal/selection"
)

// groupToResilienceConfig synthesises the resilience orchestrator's input from
// a selected v2 group. The orchestrator is reused unchanged: each v2 target
// becomes a ResilienceTarget whose per-attempt Actions switch the backend
// (state.Provider, re-resolved by the final handler) and rewrite the body model
// to the per-target alias. Order is preserved for failover; load_balance
// ignores it.
func groupToResilienceConfig(name string, g selection.Group) contractsres.ResilienceConfig {
	targets := make([]contractsres.ResilienceTarget, 0, len(g.Targets))
	for i, t := range g.Targets {
		targets = append(targets, contractsres.ResilienceTarget{
			Name:     t.Backend,
			Provider: t.Backend,
			Order:    i + 1,
			Actions:  backendSwitchActions(t.Backend, t.Alias),
		})
	}
	return contractsres.ResilienceConfig{
		Name:               name,
		Mode:               g.Mode,
		FailureStatusCodes: g.FailureStatusCodes,
		CircuitBreaker:     g.CircuitBreaker,
		Targets:            targets,
	}
}

// singleTargetConfig synthesises a degenerate ModeNone policy carrying one
// target, so a single-backend binding flows through the same orchestrator path
// as a group: the backend switch + body alias are applied once, before the
// body re-marshal step, and the final handler re-resolves the backend. This is
// what lets a single binding carry a model alias (e.g. foundry-model → the
// upstream deployment name) without a bespoke pre-forward rewrite stage.
func singleTargetConfig(t selection.Target) contractsres.ResilienceConfig {
	return contractsres.ResilienceConfig{
		Name: "binding:" + t.Backend,
		Mode: contractsres.ModeNone,
		Targets: []contractsres.ResilienceTarget{{
			Name:     t.Backend,
			Provider: t.Backend,
			Order:    1,
			Actions:  backendSwitchActions(t.Backend, t.Alias),
		}},
	}
}

// backendSwitchActions builds the internal action pair the orchestrator applies
// per attempt: changeProvider switches state.Provider to the backend (the final
// handler re-resolves transport from it), and changeModelName rewrites the body
// model to the alias when one is set. These two action types survive only as
// internal selection primitives — they are no longer authorable in rules.
func backendSwitchActions(backend, alias string) []contractsrules.Action {
	acts := []contractsrules.Action{&contractsrules.ChangeProviderAction{NewProvider: backend}}
	if alias != "" {
		acts = append(acts, &contractsrules.ChangeModelNameAction{NewModelName: alias})
	}
	return acts
}

// buildDestinationV2 resolves the upstream destination for a v2 request from
// the selected target plus the auth decision. It is the single credential mint
// site under v2 — the resolved selection.Target already carries the backend's
// base URL, protocol path, auth convention, default query, and the
// configuration's credential, so there is no provider/endpoint lookup and no
// changeProvider/changeUrl/changeApiKey override table.
//
// Credential resolution:
//
//	passthrough mode → forward the inbound Authorization verbatim.
//	managed mode, credential non-empty → set target.Auth header (or the
//	    per-protocol default when Auth is nil) to the formatted credential;
//	    drop every other inbound credential header so a managed→other forward
//	    never leaks the inbound token.
//	managed mode, credential empty → strip all credential headers, set none
//	    (no-credential backend, e.g. an unauthenticated in-cluster ollama).
//
// pathModel is the effective model for path substitution (Gemini's {model});
// it is the target alias when set, else the path-derived model. Body-model
// aliasing for the body-keyed protocols is handled by the body-rewrite stage,
// not here.
func buildDestinationV2(
	target selection.Target,
	pathParams map[string]string,
	mode auth.Mode,
	dropHeaders []string,
	inboundAuthorization string,
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

	switch {
	case mode == auth.ModePassthrough:
		if inboundAuthorization != "" {
			outgoing.Set(auth.HeaderAuthorization, inboundAuthorization)
		}
	case target.Credential == "":
		// No-credential backend: strip everything, set nothing.
		for _, h := range credentialHeaderNames {
			drops = appendUnique(drops, h)
		}
	default:
		name, value := credentialHeaderFor(target)
		outgoing.Set(name, value)
		for _, h := range credentialHeaderNames {
			if h != name {
				drops = appendUnique(drops, h)
			}
		}
	}

	return proxy.Destination{
		BaseURL:         base,
		UpstreamURL:     &upstream,
		OutgoingHeaders: outgoing,
		DropHeaders:     drops,
	}, nil
}

// credentialHeaderFor returns the (header, value) for a managed-mode credential
// on target, using the backend's per-protocol auth convention when present and
// falling back to the per-backend-name default otherwise.
func credentialHeaderFor(target selection.Target) (string, string) {
	if target.Auth == nil || target.Auth.Header == "" {
		return auth.UpstreamCredentialHeader(target.Backend, target.Credential)
	}
	if target.Auth.Format == "" {
		return target.Auth.Header, target.Credential
	}
	return target.Auth.Header, strings.ReplaceAll(target.Auth.Format, authFormatPlaceholder, target.Credential)
}
