package selection

import (
	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

// resilienceTargetSpec is the per-target input the synthesiser needs: the
// three authored fields that survive into a ResilienceTarget. Both the authored
// (contractsconfig.Target) and the resolved (Target) shapes project onto it so
// config validation and the request path synthesise through one function.
type resilienceTargetSpec struct {
	provider string
	alias    string
	weight   int
}

// GroupResilienceConfig synthesises the resilience orchestrator's input from
// an authored v2 group. It is the single synthesiser for groups: config
// validation calls it at load time (and on every admin mutation, via
// RevalidateAndIndex) and runs contractsres.ResilienceConfig.Validate on the
// result, so what the validator inspects is exactly what the orchestrator will
// run for a request bound to the group. The request path reaches the same
// function through Group.ResilienceConfig.
//
// Each target becomes a ResilienceTarget named after its provider (the
// telemetry label and breaker key), ordered by declaration position for
// failover, with Weight defaulting to 1 when unset (even weighting) and
// per-attempt Actions that switch the provider and rewrite the body model to
// the target alias.
func GroupResilienceConfig(name string, g contractsconfig.Group) contractsres.ResilienceConfig {
	specs := make([]resilienceTargetSpec, 0, len(g.Targets))
	for _, t := range g.Targets {
		specs = append(specs, resilienceTargetSpec{provider: t.Provider, alias: t.Alias, weight: t.Weight})
	}
	return synthesiseGroup(name, g.Mode, g.FailureStatusCodes, g.CircuitBreaker, g.StrictWeights, g.ResponseHeaderTimeoutSeconds, specs)
}

// ResilienceConfig synthesises the orchestrator's input from a resolved group
// at request time. It produces the same ResilienceConfig that
// GroupResilienceConfig validated at load — the resolved Group carries the
// authored orchestration fields verbatim and the per-target provider / alias /
// weight the synthesiser reads.
func (g Group) ResilienceConfig() contractsres.ResilienceConfig {
	specs := make([]resilienceTargetSpec, 0, len(g.Targets))
	for _, t := range g.Targets {
		specs = append(specs, resilienceTargetSpec{provider: t.Provider, alias: t.Alias, weight: t.Weight})
	}
	return synthesiseGroup(g.Name, g.Mode, g.FailureStatusCodes, g.CircuitBreaker, g.StrictWeights, g.ResponseHeaderTimeoutSeconds, specs)
}

// synthesiseGroup is the one place a group becomes a ResilienceConfig. Order is
// the 1-based declaration position (failover walks it ascending; load_balance
// ignores it); a zero Weight becomes 1 so an unweighted group balances evenly.
func synthesiseGroup(
	name string,
	mode contractsres.ResilienceMode,
	failureStatusCodes []int,
	cb *contractsres.CircuitBreakerConfig,
	strictWeights bool,
	responseHeaderTimeoutSeconds int,
	specs []resilienceTargetSpec,
) contractsres.ResilienceConfig {
	targets := make([]contractsres.ResilienceTarget, 0, len(specs))
	for i, s := range specs {
		weight := s.weight
		if weight == 0 {
			weight = 1
		}
		targets = append(targets, contractsres.ResilienceTarget{
			Name:     s.provider,
			Provider: s.provider,
			Order:    i + 1,
			Weight:   weight,
			Actions:  ProviderSwitchActions(s.provider, s.alias),
		})
	}
	return contractsres.ResilienceConfig{
		Name:                         name,
		Mode:                         mode,
		FailureStatusCodes:           failureStatusCodes,
		CircuitBreaker:               cb,
		StrictWeights:                strictWeights,
		ResponseHeaderTimeoutSeconds: responseHeaderTimeoutSeconds,
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
