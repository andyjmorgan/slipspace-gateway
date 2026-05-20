package admin

import (
	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

// ConfigurationSummary is the row shape returned by the configurations
// list endpoint. Detail fields (credentials, attached rules, keys) come
// down on the configuration detail endpoint, not the list.
type ConfigurationSummary struct {
	Name string `json:"name"`

	KeyCount int `json:"key_count"`

	RuleCount int `json:"rule_count"`

	Tags map[string]string `json:"tags,omitempty"`
}

// APIKeySummary is the redacted projection of a single api_key.
// Secret is omitted entirely; the operator sees the last four characters
// only, with the total length so they can sanity-check against what they
// stored in their secret manager.
type APIKeySummary struct {
	Name string `json:"name"`

	Secret RedactedSecret `json:"secret"`

	Enabled bool `json:"enabled"`
}

// APIKeyReveal carries an api-key's PLAINTEXT secret back to the operator.
// Returned only by the reveal endpoint, which sits behind the same Basic
// auth as the rest of the admin API — the operator already has the
// password, so handing them the secret is not a privilege escalation.
// Bulk list endpoints stay redacted; this is opt-in, per-row.
type APIKeyReveal struct {
	Name string `json:"name"`

	Secret string `json:"secret"`

	Enabled bool `json:"enabled"`

	Configuration string `json:"configuration"`
}

// RuleAttachment is a configuration's view of one attached rule:
// the rule name + a one-line summary that the SPA renders without
// needing to fetch the rule detail. Position in the parent slice IS
// the evaluation order — there is no separate priority field.
type RuleAttachment struct {
	Name string `json:"name"`

	ConditionSummary string `json:"condition_summary"`

	ActionTypes []string `json:"action_types"`

	Behavior rulescontract.RuleBehavior `json:"behavior,omitempty"`
}

// ConfigurationDetail is the full read-only view of a single configuration,
// with every secret redacted at the boundary.
type ConfigurationDetail struct {
	Name string `json:"name"`

	UpstreamCredentials map[string]RedactedSecret `json:"upstream_credentials"`

	Rules []RuleAttachment `json:"rules"`

	Tags map[string]string `json:"tags,omitempty"`

	APIKeys []APIKeySummary `json:"api_keys"`
}

// RuleSummary is the row shape returned by the rules list endpoint.
// The condition + actions are summarised inline so the SPA's list view
// can render meaningful detail without paging through detail responses.
type RuleSummary struct {
	Name string `json:"name"`

	ConditionSummary string `json:"condition_summary"`

	ActionTypes []string `json:"action_types"`

	Behavior rulescontract.RuleBehavior `json:"behavior,omitempty"`

	// UsedBy lists the configurations that reference this rule by name,
	// sorted for stable rendering.
	UsedBy []string `json:"used_by"`
}

// RuleDetail is the full read-only view of one rule: name +
// the raw condition / actions (already JSON-friendly via the rules
// contract's MarshalJSON) + the used-by backlink.
type RuleDetail struct {
	Name string `json:"name"`

	Behavior rulescontract.RuleBehavior `json:"behavior,omitempty"`

	Condition rulescontract.Condition `json:"condition,omitempty"`

	Actions []rulescontract.Action `json:"actions,omitempty"`

	UsedBy []string `json:"used_by"`
}

// EndpointSummary is one row in a ProviderDetail's endpoint table.
type EndpointSummary struct {
	Name string `json:"name"`

	Path string `json:"path"`

	Methods []string `json:"methods"`

	AcceptedPaths []string `json:"accepted_paths"`

	AcceptsStreaming bool `json:"accepts_streaming"`

	RequestKind string `json:"request_kind"`

	AuthHeader string `json:"auth_header,omitempty"`

	AuthFormat string `json:"auth_format,omitempty"`

	PrefixOptional bool `json:"prefix_optional,omitempty"`
}

// ProviderSummary is the row shape returned by the providers list endpoint.
type ProviderSummary struct {
	Name string `json:"name"`

	Prefix string `json:"prefix,omitempty"`

	PrefixRequired bool `json:"prefix_required"`

	BaseURL string `json:"base_url"`

	EndpointCount int `json:"endpoint_count"`
}

// ProviderDetail is the full read-only view of one provider: required
// headers + per-endpoint auth overrides included so an operator can
// debug the OpenAI-compat surfaces at a glance.
type ProviderDetail struct {
	Name string `json:"name"`

	Prefix string `json:"prefix,omitempty"`

	PrefixRequired bool `json:"prefix_required"`

	BaseURL string `json:"base_url"`

	AuthHeader string `json:"auth_header,omitempty"`

	AuthFormat string `json:"auth_format,omitempty"`

	RequiredHeaders map[string]string `json:"required_headers,omitempty"`

	Endpoints []EndpointSummary `json:"endpoints"`
}

// RouteRow is one row in the flattened-routes endpoint. The route table
// is the data the routing middleware reads on every request; surfacing
// it is high-leverage during debugging.
type RouteRow struct {
	Path string `json:"path"`

	Provider string `json:"provider"`

	Endpoint string `json:"endpoint"`

	Methods []string `json:"methods"`
}

// Internal helpers that build the DTO summaries from the contract types.
// Kept here so the handler file stays focused on HTTP plumbing.

// summariseCondition renders a Condition into a single-line human-readable
// string. Falls back to the discriminator + an "unknown shape" tag for
// types the summariser does not know — we never want the call to panic
// because a new condition type shipped without summariser support.
func summariseCondition(c rulescontract.Condition) string {
	if c == nil {
		return "(no condition)"
	}
	switch v := c.(type) {
	case *rulescontract.ProviderCondition:
		return "provider " + opString(string(v.Operator)) + " " + v.ExpectedProvider + notSuffix(v.Not)
	case *rulescontract.EndpointCondition:
		return "endpoint " + opString(string(v.Operator)) + " " + v.ExpectedEndpoint + notSuffix(v.Not)
	case *rulescontract.ModelNameCondition:
		return "model " + opString(string(v.Operator)) + " " + v.ExpectedModelName + notSuffix(v.Not)
	case *rulescontract.HeaderCondition:
		base := "header " + opString(string(v.KeyOperator)) + " " + v.KeyPattern
		if v.ValueOperator != nil && *v.ValueOperator != "" {
			base += " · value " + opString(string(*v.ValueOperator)) + " " + v.ValuePattern
		}
		return base + notSuffix(v.Not)
	case *rulescontract.RuleGroup:
		return "group (" + string(v.LogicalOperator) + " · " + pluralChildren(len(v.Children)) + ")" + notSuffix(v.Not)
	default:
		return c.ConditionType()
	}
}

// summariseActionTypes returns the ordered list of action discriminators.
// The SPA renders these as chips.
func summariseActionTypes(actions []rulescontract.Action) []string {
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		if a == nil {
			continue
		}
		out = append(out, a.ActionType())
	}
	return out
}

func opString(s string) string {
	if s == "" {
		return "Equals"
	}
	return s
}

func notSuffix(not bool) string {
	if not {
		return " (negated)"
	}
	return ""
}

// pluralChildren produces "1 child" or "N children" for RuleGroup
// summaries. Single-purpose because that is the only summariser that
// ever needs a singular/plural form.
func pluralChildren(n int) string {
	if n == 1 {
		return "1 child"
	}
	return itoa(n) + " children"
}

// providerSummaryFromContract folds the loaded provider into the wire
// summary, counting endpoints lazily.
func providerSummaryFromContract(name string, p contractsconfig.Provider) ProviderSummary {
	return ProviderSummary{
		Name:           name,
		Prefix:         p.Prefix,
		PrefixRequired: p.PrefixRequired,
		BaseURL:        p.BaseURL,
		EndpointCount:  len(p.Endpoints),
	}
}
