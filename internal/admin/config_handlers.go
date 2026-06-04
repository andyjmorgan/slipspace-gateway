package admin

import (
	"net/http"
	"sort"
	"strings"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// pathPrefix is the path component the routing tree below strips before
// dispatching to a detail handler. Defined here rather than in mux so the
// handler tests can match the dispatch wiring without the mux indirection.
const (
	pathConfigurations = "/api/v1/config/configurations/"
	pathRules          = "/api/v1/config/rules/"
	pathBackends       = "/api/v1/config/backends/"
)

// ConfigurationsListHandler returns the sorted summary list of every
// configuration loaded from policy.yaml. A nil Store (admin disabled in
// test or partial wiring) returns 503 rather than 200 with an empty body
// so the SPA can distinguish "no configs" from "feature unavailable".
//
// Each handler snapshots the store once at the top of the request and
// reads through that snapshot for the rest of the call, so a config
// Replace landing mid-handler still produces a consistent JSON payload.
func ConfigurationsListHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		keysByConfig := apiKeyCountsByConfiguration(resolved.APIKeys)
		out := make([]ConfigurationSummary, 0, len(resolved.Configurations))
		for name, cfg := range resolved.Configurations {
			out = append(out, ConfigurationSummary{
				Name:      name,
				KeyCount:  keysByConfig[name],
				RuleCount: len(cfg.RuleNames),
				Tags:      cfg.Tags,
			})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		writeJSON(w, out)
	})
}

// ConfigurationDetailHandler returns the redacted detail view of a single
// configuration. 404 when the path component does not match a loaded
// configuration name; 503 when the store is nil.
func ConfigurationDetailHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, pathConfigurations)
		name = strings.TrimSuffix(name, "/")
		if name == "" {
			http.NotFound(w, r)
			return
		}
		if _, ok := resolved.Configurations[name]; !ok {
			http.NotFound(w, r)
			return
		}

		writeJSON(w, configurationDetailView(name, resolved))
	})
}

// configurationDetailView projects one configuration into the redacted detail
// DTO — credentials masked, bindings split into generative + passthrough, rule
// attachments and api-key summaries resolved. Shared by the GET detail handler
// and the create/replace write handlers so a write response is byte-identical to
// a follow-up GET.
func configurationDetailView(name string, resolved *config.ResolvedConfig) ConfigurationDetail {
	cfg := resolved.Configurations[name]
	gen, pass := bindingRowsFromConfiguration("", cfg)
	return ConfigurationDetail{
		Name:                name,
		Credentials:         redactMap(cfg.Credentials),
		Bindings:            gen,
		PassthroughBindings: pass,
		Tags:                cfg.Tags,
		ConnectorBindings:   cfg.ConnectorBindings,
		Rules:               buildRuleAttachments(name, resolved),
		APIKeys:             buildAPIKeySummaries(name, resolved.APIKeys),
	}
}

// RulesListHandler returns the sorted rule library with usage backlinks.
func RulesListHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		ruleAttach := invertRuleUsage(resolved)
		out := make([]RuleSummary, 0, len(resolved.Rules))
		for i := range resolved.Rules {
			rule := &resolved.Rules[i]
			out = append(out, RuleSummary{
				Name:             rule.Name,
				ConditionSummary: summariseCondition(rule.Condition),
				ActionTypes:      summariseActionTypes(rule.Actions),
				Behavior:         rule.Behavior,
				UsedBy:           sortedCopy(ruleAttach[rule.Name]),
			})
		}
		// Library sorted alphabetically — evaluation order is per
		// configuration (see ConfigurationDetail.Rules), not a library
		// concern.
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		writeJSON(w, out)
	})
}

// RuleDetailHandler returns the full rule body for a single rule.
func RuleDetailHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, pathRules)
		name = strings.TrimSuffix(name, "/")
		if name == "" {
			http.NotFound(w, r)
			return
		}
		rule, ok := resolved.RuleIndex[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		ruleAttach := invertRuleUsage(resolved)
		out := RuleDetail{
			Name:      rule.Name,
			Behavior:  rule.Behavior,
			Condition: rule.Condition,
			Actions:   rule.Actions,
			UsedBy:    sortedCopy(ruleAttach[rule.Name]),
		}
		writeJSON(w, out)
	})
}

// BackendsListHandler returns the sorted summary list of every backend
// connection loaded from the v2 config (was the providers list).
func BackendsListHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		out := make([]BackendSummary, 0, len(resolved.Backends))
		for name, b := range resolved.Backends {
			out = append(out, backendSummaryFromContract(name, b))
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		writeJSON(w, out)
	})
}

// BackendDetailHandler returns a backend's full protocol catalogue, including
// per-protocol auth conventions + passthrough families — the load-bearing data
// for debugging the OpenAI-compat surfaces on Anthropic and Gemini.
func BackendDetailHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, pathBackends)
		name = strings.TrimSuffix(name, "/")
		if name == "" {
			http.NotFound(w, r)
			return
		}
		b, ok := resolved.Backends[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, backendDetailFromContract(name, b))
	})
}

// APIKeysRevealHandler returns the plaintext secret for a single api_key
// identified by (configuration, name). Lives behind the same Basic auth
// as the rest of /admin/api/v1/* — the operator already holds the admin
// password, so handing them their own credential is not an escalation.
// Kept on a dedicated path so the bulk-list endpoint can stay redacted
// by default; reveal is opt-in, per-row.
//
// Both query parameters are required. Returns 400 on either missing,
// 404 when no key matches the composite, 503 when the store is nil.
func APIKeysRevealHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		q := r.URL.Query()
		configuration := q.Get("configuration")
		name := q.Get("name")
		if configuration == "" || name == "" {
			http.Error(w, "configuration and name query parameters are required", http.StatusBadRequest)
			return
		}
		for _, k := range resolved.APIKeys {
			if k.Configuration != configuration || k.Name != name {
				continue
			}
			writeJSON(w, APIKeyReveal{
				Name:          k.Name,
				Secret:        k.Secret,
				Enabled:       k.Enabled,
				Configuration: k.Configuration,
			})
			return
		}
		http.NotFound(w, r)
	})
}

// BindingsHandler returns the flattened binding table across every
// configuration — the v2 analogue of the route table. A binding is
// (protocol, models) → backend|group; this is the data selection reads on
// every request, so surfacing it is the highest-value page for routing
// debugging. Passthrough bindings are returned as a separate list.
func BindingsHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		var (
			gen  []BindingRow
			pass []PassthroughBindingRow
		)
		for name, cfg := range resolved.Configurations {
			g, p := bindingRowsFromConfiguration(name, cfg)
			gen = append(gen, g...)
			pass = append(pass, p...)
		}
		sort.Slice(gen, func(i, j int) bool {
			if gen[i].Configuration != gen[j].Configuration {
				return gen[i].Configuration < gen[j].Configuration
			}
			if gen[i].Protocol != gen[j].Protocol {
				return gen[i].Protocol < gen[j].Protocol
			}
			return strings.Join(gen[i].Models, ",") < strings.Join(gen[j].Models, ",")
		})
		sort.Slice(pass, func(i, j int) bool {
			if pass[i].Configuration != pass[j].Configuration {
				return pass[i].Configuration < pass[j].Configuration
			}
			return pass[i].Family < pass[j].Family
		})
		writeJSON(w, struct {
			Bindings            []BindingRow            `json:"bindings"`
			PassthroughBindings []PassthroughBindingRow `json:"passthrough_bindings"`
		}{Bindings: gen, PassthroughBindings: pass})
	})
}

// snapshot is the one-line nil-tolerant wrapper around store.Snapshot
// every handler calls at the top of the request. Returns nil when the
// store is nil or its current snapshot is nil; handlers translate that
// to a 503.
func snapshot(store *config.Store) *config.ResolvedConfig {
	if store == nil {
		return nil
	}
	return store.Snapshot()
}

// --- helpers --------------------------------------------------------------

func apiKeyCountsByConfiguration(keys []contractsconfig.APIKey) map[string]int {
	out := make(map[string]int, len(keys))
	for _, k := range keys {
		out[k.Configuration]++
	}
	return out
}

func buildAPIKeySummaries(configName string, keys []contractsconfig.APIKey) []APIKeySummary {
	out := make([]APIKeySummary, 0)
	for _, k := range keys {
		if k.Configuration != configName {
			continue
		}
		out = append(out, APIKeySummary{
			Name:    k.Name,
			Secret:  redact(k.Secret),
			Enabled: k.Enabled,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// buildRuleAttachments materialises a configuration's attached rules in
// runtime priority order. Falls back to the configuration's RuleNames
// ordering if the indexes have not been built (defensive — Validate
// builds them, but the handlers do not require that).
func buildRuleAttachments(configName string, resolved *config.ResolvedConfig) []RuleAttachment {
	cfg, ok := resolved.Configurations[configName]
	if !ok {
		return nil
	}
	attached := resolved.PerConfigurationRules[configName]
	if len(attached) == 0 && len(cfg.RuleNames) > 0 {
		// Lookups via RuleIndex with the authored order as the only
		// guarantee.
		attached = make([]*rulescontract.RuleContract, 0, len(cfg.RuleNames))
		for _, ruleName := range cfg.RuleNames {
			if rule, ok := resolved.RuleIndex[ruleName]; ok {
				attached = append(attached, rule)
			}
		}
	}
	out := make([]RuleAttachment, 0, len(attached))
	for _, rule := range attached {
		out = append(out, RuleAttachment{
			Name:             rule.Name,
			ConditionSummary: summariseCondition(rule.Condition),
			ActionTypes:      summariseActionTypes(rule.Actions),
			Behavior:         rule.Behavior,
		})
	}
	return out
}

// invertRuleUsage maps rule name → configurations that reference it.
// Configuration order matches insertion order from the map; callers sort
// for stable rendering.
func invertRuleUsage(resolved *config.ResolvedConfig) map[string][]string {
	out := make(map[string][]string)
	for name, cfg := range resolved.Configurations {
		for _, ruleName := range cfg.RuleNames {
			out[ruleName] = append(out[ruleName], name)
		}
	}
	return out
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
