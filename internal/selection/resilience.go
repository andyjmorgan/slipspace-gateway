package selection

import (
	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

// resilienceTargetSpec is the per-target input the synthesiser needs: the
// authored fields that survive into a ResilienceTarget plus the target's
// resolved name. Both the authored (contractsconfig.Target) and the resolved
// (Target) shapes project onto it so config validation and the request path
// synthesise through one function.
type resilienceTargetSpec struct {
	// name is the orchestrator identity (breaker key + `target` metric
	// label) — contractsconfig.Group.TargetNames output, so two targets on
	// one provider stay distinct.
	name           string
	provider       string
	alias          string
	weight         int
	timeoutSeconds int
}

// resilienceGroupSpec is the group-wide input the synthesiser needs — the
// orchestration fields both the authored and the resolved Group carry
// verbatim.
type resilienceGroupSpec struct {
	name                         string
	mode                         contractsres.ResilienceMode
	failureStatusCodes           []int
	cb                           *contractsres.CircuitBreakerConfig
	strictWeights                bool
	responseHeaderTimeoutSeconds int
	timeoutSeconds               int
	retry                        *contractsres.RetryConfig
}

// GroupResilienceConfig synthesises the resilience orchestrator's input from
// an authored v2 group. It is the single synthesiser for groups: config
// validation calls it at load time (and on every admin mutation, via
// RevalidateAndIndex) and runs contractsres.ResilienceConfig.Validate on the
// result, so what the validator inspects is exactly what the orchestrator will
// run for a request bound to the group. The request path reaches the same
// function through Group.ResilienceConfig.
//
// Each target becomes a ResilienceTarget named per Group.TargetNames — the
// plain provider name unless the group lists that provider more than once,
// in which case the arms are disambiguated so their breaker keys and
// `target` labels stay distinct — ordered by declaration position for
// failover, with Weight defaulting to 1 when unset (even weighting),
// the target's own timeout_seconds, and per-attempt Actions that switch the
// provider and rewrite the body model to the target alias. The group's
// timeout_seconds and retry block are carried through verbatim.
func GroupResilienceConfig(name string, g contractsconfig.Group) contractsres.ResilienceConfig {
	names := g.TargetNames()
	specs := make([]resilienceTargetSpec, 0, len(g.Targets))
	for i, t := range g.Targets {
		specs = append(specs, resilienceTargetSpec{
			name:           names[i],
			provider:       t.Provider,
			alias:          t.Alias,
			weight:         t.Weight,
			timeoutSeconds: t.TimeoutSeconds,
		})
	}
	return synthesiseGroup(resilienceGroupSpec{
		name:                         name,
		mode:                         g.Mode,
		failureStatusCodes:           g.FailureStatusCodes,
		cb:                           g.CircuitBreaker,
		strictWeights:                g.StrictWeights,
		responseHeaderTimeoutSeconds: g.ResponseHeaderTimeoutSeconds,
		timeoutSeconds:               g.TimeoutSeconds,
		retry:                        g.Retry,
	}, specs)
}

// ResilienceConfig synthesises the orchestrator's input from a resolved group
// at request time. It produces the same ResilienceConfig that
// GroupResilienceConfig validated at load — the resolved Group carries the
// authored orchestration fields verbatim and the per-target name / provider /
// alias / weight / timeout the synthesiser reads. A resolved Target with an
// empty Name (a hand-built Group in tests) falls back to its provider name.
func (g Group) ResilienceConfig() contractsres.ResilienceConfig {
	specs := make([]resilienceTargetSpec, 0, len(g.Targets))
	for _, t := range g.Targets {
		name := t.Name
		if name == "" {
			name = t.Provider
		}
		specs = append(specs, resilienceTargetSpec{
			name:           name,
			provider:       t.Provider,
			alias:          t.Alias,
			weight:         t.Weight,
			timeoutSeconds: t.TimeoutSeconds,
		})
	}
	return synthesiseGroup(resilienceGroupSpec{
		name:                         g.Name,
		mode:                         g.Mode,
		failureStatusCodes:           g.FailureStatusCodes,
		cb:                           g.CircuitBreaker,
		strictWeights:                g.StrictWeights,
		responseHeaderTimeoutSeconds: g.ResponseHeaderTimeoutSeconds,
		timeoutSeconds:               g.TimeoutSeconds,
		retry:                        g.Retry,
	}, specs)
}

// synthesiseGroup is the one place a group becomes a ResilienceConfig. Order is
// the 1-based declaration position (failover walks it ascending; load_balance
// ignores it); a zero Weight becomes 1 so an unweighted group balances evenly.
// Provider always remains the real provider name — it is what the final
// handler re-resolves transport from — while Name carries the (possibly
// disambiguated) telemetry identity.
func synthesiseGroup(g resilienceGroupSpec, specs []resilienceTargetSpec) contractsres.ResilienceConfig {
	targets := make([]contractsres.ResilienceTarget, 0, len(specs))
	for i, s := range specs {
		weight := s.weight
		if weight == 0 {
			weight = 1
		}
		targets = append(targets, contractsres.ResilienceTarget{
			Name:           s.name,
			Provider:       s.provider,
			Order:          i + 1,
			Weight:         weight,
			TimeoutSeconds: s.timeoutSeconds,
			Actions:        ProviderSwitchActions(s.provider, s.alias),
		})
	}
	return contractsres.ResilienceConfig{
		Name:                         g.name,
		Mode:                         g.mode,
		FailureStatusCodes:           g.failureStatusCodes,
		CircuitBreaker:               g.cb,
		StrictWeights:                g.strictWeights,
		ResponseHeaderTimeoutSeconds: g.responseHeaderTimeoutSeconds,
		TimeoutSeconds:               g.timeoutSeconds,
		Retry:                        g.retry,
		Targets:                      targets,
	}
}

// SingleTargetResilienceConfig synthesises a degenerate ModeNone policy carrying
// one target, so a single-provider binding flows through the same orchestrator
// path as a group: the provider switch + body alias are applied once, before the
// body re-marshal step, and the final handler re-resolves the provider. This is
// what lets a single binding carry a model alias (e.g. foundry-model → the
// upstream deployment name) without a bespoke pre-forward rewrite stage. The
// "binding:<provider>" name is a telemetry handle only; it is never authored
// and never validated as a group name.
func SingleTargetResilienceConfig(t Target) contractsres.ResilienceConfig {
	return contractsres.ResilienceConfig{
		Name: "binding:" + t.Provider,
		Mode: contractsres.ModeNone,
		Targets: []contractsres.ResilienceTarget{{
			Name:     t.Provider,
			Provider: t.Provider,
			Order:    1,
			Actions:  ProviderSwitchActions(t.Provider, t.Alias),
		}},
	}
}

// ProviderSwitchActions builds the internal action pair the orchestrator applies
// per attempt: changeProvider switches state.Provider to the provider (the final
// handler re-resolves transport from it), and changeModelName rewrites the body
// model to the alias when one is set. The action registry still parses both
// types, but they are no longer the authorable routing mechanism: a rule-authored
// changeProvider is overwritten every attempt by buildAttemptState re-applying
// the target's own ProviderSwitchActions, so in practice they survive as
// internal selection primitives.
func ProviderSwitchActions(provider, alias string) []contractsrules.Action {
	acts := []contractsrules.Action{&contractsrules.ChangeProviderAction{NewProvider: provider}}
	if alias != "" {
		acts = append(acts, &contractsrules.ChangeModelNameAction{NewModelName: alias})
	}
	return acts
}
