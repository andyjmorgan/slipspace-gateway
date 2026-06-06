package admin

import (
	"sort"

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
// with every secret redacted at the boundary. Under v2 it carries the
// per-provider credentials the configuration holds plus its bindings (the
// router, as data) — generative and passthrough.
type ConfigurationDetail struct {
	Name string `json:"name"`

	Credentials map[string]RedactedSecret `json:"credentials"`

	Bindings []BindingRow `json:"bindings"`

	PassthroughBindings []PassthroughBindingRow `json:"passthrough_bindings"`

	Rules []RuleAttachment `json:"rules"`

	Tags map[string]string `json:"tags,omitempty"`

	// ConnectorBindings is exposed so the configuration editor can round-trip
	// them on a full-replace PUT — without this an edit would silently drop the
	// configuration's connector bindings. No secrets (connector credentials are
	// secret_refs), so the raw contract type is returned.
	ConnectorBindings []contractsconfig.ConnectorBinding `json:"connector_bindings,omitempty"`

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

// ProtocolRow is one row in a ProviderDetail's protocol table: the wire shape
// the provider serves, its upstream path, and the per-protocol auth convention
// (the load-bearing data for debugging the OpenAI-compat surfaces).
type ProtocolRow struct {
	Name string `json:"name"`

	Path string `json:"path"`

	AuthHeader string `json:"auth_header,omitempty"`

	AuthFormat string `json:"auth_format,omitempty"`
}

// PassthroughFamilyRow is one opaque passthrough family exposed by a provider:
// a named set of path patterns + methods proxied verbatim, with no GenAI
// telemetry or body inspection.
type PassthroughFamilyRow struct {
	Name string `json:"name"`

	AuthHeader string `json:"auth_header,omitempty"`

	Paths []PassthroughPathRow `json:"paths"`
}

// PassthroughPathRow is one path pattern + its accepted methods within a
// passthrough family.
type PassthroughPathRow struct {
	Match string `json:"match"`

	Methods []string `json:"methods"`
}

// ProviderSummary is the row shape returned by the providers list endpoint.
type ProviderSummary struct {
	Name string `json:"name"`

	BaseURL string `json:"base_url"`

	Protocols []string `json:"protocols"`

	HasPassthrough bool `json:"has_passthrough"`
}

// ProviderDetail is the full read-only view of one provider connection: base URL,
// required headers, default query, per-protocol auth, and passthrough families.
type ProviderDetail struct {
	Name string `json:"name"`

	BaseURL string `json:"base_url"`

	RequiredHeaders map[string]string `json:"required_headers,omitempty"`

	Query map[string]string `json:"query,omitempty"`

	Protocols []ProtocolRow `json:"protocols"`

	Passthrough []PassthroughFamilyRow `json:"passthrough,omitempty"`
}

// BindingRow is one generative binding: (protocol, models) → provider|group,
// with optional per-binding alias/tags. It is the v2 analogue of a route row —
// the router expressed as configuration data.
type BindingRow struct {
	Configuration string `json:"configuration,omitempty"`

	Protocol string `json:"protocol"`

	Models []string `json:"models"`

	Provider string `json:"provider,omitempty"`

	Group string `json:"group,omitempty"`

	Alias string `json:"alias,omitempty"`

	Tags []string `json:"tags,omitempty"`
}

// PassthroughBindingRow is one passthrough binding: an opaque family exposed on
// a provider by a configuration.
type PassthroughBindingRow struct {
	Configuration string `json:"configuration,omitempty"`

	Family string `json:"family"`

	Provider string `json:"provider"`

	Tags []string `json:"tags,omitempty"`
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
	case *rulescontract.ProtocolCondition:
		return "protocol " + opString(string(v.Operator)) + " " + v.ExpectedProtocol + notSuffix(v.Not)
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

// providerSummaryFromContract folds a loaded provider into the wire summary,
// listing its protocol names and whether it exposes any passthrough family.
func providerSummaryFromContract(name string, b contractsconfig.Provider) ProviderSummary {
	protos := make([]string, 0, len(b.Protocols))
	for p := range b.Protocols {
		protos = append(protos, p)
	}
	sort.Strings(protos)
	return ProviderSummary{
		Name:           name,
		BaseURL:        b.BaseURL,
		Protocols:      protos,
		HasPassthrough: len(b.Passthrough) > 0,
	}
}

// providerDetailFromContract projects a provider onto the full detail DTO:
// per-protocol auth conventions + passthrough families, sorted for stable
// rendering.
func providerDetailFromContract(name string, b contractsconfig.Provider) ProviderDetail {
	protos := make([]ProtocolRow, 0, len(b.Protocols))
	for pname, p := range b.Protocols {
		row := ProtocolRow{Name: pname, Path: p.Path}
		if p.Auth != nil {
			row.AuthHeader = p.Auth.Header
			row.AuthFormat = p.Auth.Format
		}
		protos = append(protos, row)
	}
	sort.Slice(protos, func(i, j int) bool { return protos[i].Name < protos[j].Name })

	var families []PassthroughFamilyRow
	for fname, f := range b.Passthrough {
		fr := PassthroughFamilyRow{Name: fname}
		if f.Auth != nil {
			fr.AuthHeader = f.Auth.Header
		}
		for _, pp := range f.Paths {
			fr.Paths = append(fr.Paths, PassthroughPathRow{
				Match:   pp.Match,
				Methods: append([]string(nil), pp.Methods...),
			})
		}
		families = append(families, fr)
	}
	sort.Slice(families, func(i, j int) bool { return families[i].Name < families[j].Name })

	return ProviderDetail{
		Name:            name,
		BaseURL:         b.BaseURL,
		RequiredHeaders: b.RequiredHeaders,
		Query:           b.Query,
		Protocols:       protos,
		Passthrough:     families,
	}
}

// bindingRowsFromConfiguration flattens a configuration's bindings into wire
// rows. configName is stamped on each row when non-empty so the global
// bindings view can attribute each row to its owning configuration.
func bindingRowsFromConfiguration(configName string, cfg contractsconfig.Configuration) ([]BindingRow, []PassthroughBindingRow) {
	gen := make([]BindingRow, 0, len(cfg.Bindings))
	for _, b := range cfg.Bindings {
		gen = append(gen, BindingRow{
			Configuration: configName,
			Protocol:      b.Protocol,
			Models:        append([]string(nil), b.Models...),
			Provider:      b.Provider,
			Group:         b.Group,
			Alias:         b.Alias,
			Tags:          append([]string(nil), b.Tags...),
		})
	}
	pass := make([]PassthroughBindingRow, 0, len(cfg.PassthroughBindings))
	for _, pb := range cfg.PassthroughBindings {
		pass = append(pass, PassthroughBindingRow{
			Configuration: configName,
			Family:        pb.Family,
			Provider:      pb.Provider,
			Tags:          append([]string(nil), pb.Tags...),
		})
	}
	return gen, pass
}
