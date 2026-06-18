// Package selection resolves a request to its upstream destination under the
// v2 config model (providers + bindings). It replaces v1's path→(provider,
// endpoint) route table and the changeProvider/changeUrl/changeApiKey/
// useResiliencePolicy rule actions: routing is now config data.
//
// Two surfaces:
//
//   - Select — generative protocols. Given (protocol, model, configuration) it
//     walks the configuration's bindings and returns the resolved Destination
//     (a single target or a resilience group), with effective transport
//     composed from provider ∪ per-target overrides.
//   - MatchPassthrough — opaque/stateful families (batches, files). Path-pattern
//     matched, not model-keyed; the request is proxied verbatim with rewrites.
//
// The engine is a pure function of the v2 config; it does no I/O and is unit
// tested against the hand-authored golden fixture independently of the request
// pipeline.
package selection

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
)

// ErrNoBinding is returned by Select when no binding in the configuration
// matches the (protocol, model) pair. The caller maps this to 404 — the model
// is not served on that protocol by this configuration (no fallthrough).
var ErrNoBinding = errors.New("selection: no binding matches protocol+model")

// ErrNoPassthrough is returned by MatchPassthrough when no exposed passthrough
// family claims the path.
var ErrNoPassthrough = errors.New("selection: no passthrough family matches path")

// ErrMethodNotAllowed is returned by MatchPassthrough when a family claims the
// path but not the method.
var ErrMethodNotAllowed = errors.New("selection: method not allowed for passthrough path")

// Destination is the resolved outcome of a generative binding match: either a
// single Target or a resilience Group (the orchestrator expands a Group across
// its targets). Exactly one of Single / Group is non-nil.
type Destination struct {
	// Protocol is the generative protocol the request was selected under.
	Protocol string

	// Tags are the binding's tags, applied to the request for telemetry.
	Tags []string

	// Single is the resolved single-provider target (nil when Group is set).
	Single *Target

	// Group is the resolved resilience group (nil when Single is set).
	Group *Group
}

// Target is a fully resolved upstream destination: a provider connection with
// the effective transport for one protocol, the model alias to apply, and the
// credential the configuration holds for the provider. Everything the forwarder
// needs to mint the request, with no further config lookups.
type Target struct {
	// Provider is the provider name (telemetry label / diagnostics).
	Provider string

	// BaseURL is the provider connection root.
	BaseURL string

	// Path is the effective upstream path for the protocol (target override
	// wins over the provider protocol path).
	Path string

	// Auth is the effective credential convention for the protocol; nil means
	// "provider-native default" (resolved downstream at the single mint site).
	Auth *contractsconfig.ProviderAuth

	// RequiredHeaders are the provider's always-injected headers.
	RequiredHeaders map[string]string

	// Query is the effective query params (provider ∪ target override, target
	// wins).
	Query map[string]string

	// Credential is the upstream credential the configuration holds for this
	// provider ("" = no-credential provider: strip and forward).
	Credential string

	// Alias, when non-empty, is the model name to rewrite the request body to
	// when this target is dispatched.
	Alias string

	// Weight is the relative load-balance selection weight (0 = even).
	Weight int
}

// Group is a resolved resilience group: the orchestration policy plus the
// resolved targets it routes across.
type Group struct {
	// Name is the group name (telemetry / diagnostics).
	Name string

	// Mode is the orchestration strategy.
	Mode resilience.ResilienceMode

	// FailureStatusCodes is the failure set for retry / breaker accounting.
	FailureStatusCodes []int

	// CircuitBreaker is the group-wide breaker config (breaker state is keyed
	// per provider downstream).
	CircuitBreaker *resilience.CircuitBreakerConfig

	// StrictWeights, in load_balance mode, makes the first weighted pick final.
	StrictWeights bool

	// ResponseHeaderTimeoutSeconds overrides the gateway-wide upstream
	// response-header timeout for every attempt under this group (0 = default).
	ResponseHeaderTimeoutSeconds int

	// Targets is the resolved targets the orchestrator dispatches across.
	Targets []Target
}

// Select resolves the destination for a generative request. It returns
// ErrNoBinding when no binding matches; resolution errors (unknown provider /
// group / protocol-not-served) are programming/validation errors that config
// validation rejects before runtime, surfaced here defensively.
func Select(
	protocol, model string,
	cfg contractsconfig.Configuration,
	providers contractsconfig.ProvidersConfig,
	groups contractsconfig.GroupsConfig,
) (Destination, error) {
	for _, b := range cfg.Bindings {
		if b.Protocol != protocol {
			continue
		}
		if !matchesModelPatterns(model, b.Models) {
			continue
		}

		switch {
		case b.Group != "":
			grp, ok := groups[b.Group]
			if !ok {
				return Destination{}, fmt.Errorf("selection: binding references unknown group %q", b.Group)
			}
			resolved := Group{
				Name:                         b.Group,
				Mode:                         grp.Mode,
				FailureStatusCodes:           grp.FailureStatusCodes,
				CircuitBreaker:               grp.CircuitBreaker,
				StrictWeights:                grp.StrictWeights,
				ResponseHeaderTimeoutSeconds: grp.ResponseHeaderTimeoutSeconds,
				Targets:                      make([]Target, 0, len(grp.Targets)),
			}
			for _, gt := range grp.Targets {
				t, err := resolveTarget(protocol, cfg, providers, gt)
				if err != nil {
					return Destination{}, err
				}
				resolved.Targets = append(resolved.Targets, t)
			}
			return Destination{Protocol: protocol, Tags: b.Tags, Group: &resolved}, nil

		default:
			t, err := resolveTarget(protocol, cfg, providers, contractsconfig.Target{
				Provider: b.Provider,
				Alias:    b.Alias,
				Query:    b.Query,
				Path:     b.Path,
			})
			if err != nil {
				return Destination{}, err
			}
			return Destination{Protocol: protocol, Tags: b.Tags, Single: &t}, nil
		}
	}
	return Destination{}, ErrNoBinding
}

// ResolveTarget resolves a single provider into a fully resolved Target for the
// protocol, using the configuration's credentials and the given model alias.
// The request pipeline calls it to (re)resolve the destination for a chosen
// provider — including per-attempt during group orchestration, where the
// orchestrator picks the provider and the pipeline re-resolves its transport.
func ResolveTarget(
	protocol, provider, alias string,
	cfg contractsconfig.Configuration,
	providers contractsconfig.ProvidersConfig,
) (Target, error) {
	return resolveTarget(protocol, cfg, providers, contractsconfig.Target{Provider: provider, Alias: alias})
}

// resolveTarget composes a contracts Target against its provider and the
// configuration's credentials into a fully resolved Target for one protocol.
func resolveTarget(
	protocol string,
	cfg contractsconfig.Configuration,
	providers contractsconfig.ProvidersConfig,
	tgt contractsconfig.Target,
) (Target, error) {
	be, ok := providers[tgt.Provider]
	if !ok {
		return Target{}, fmt.Errorf("selection: target references unknown provider %q", tgt.Provider)
	}
	proto, ok := be.Protocols[protocol]
	if !ok {
		return Target{}, fmt.Errorf("selection: provider %q does not serve protocol %q", tgt.Provider, protocol)
	}

	path := proto.Path
	if tgt.Path != "" {
		path = tgt.Path
	}

	cred, hasCred := cfg.Credentials[tgt.Provider]
	if !hasCred {
		return Target{}, fmt.Errorf("selection: configuration holds no credential entry for provider %q", tgt.Provider)
	}

	return Target{
		Provider:        tgt.Provider,
		BaseURL:         be.BaseURL,
		Path:            path,
		Auth:            proto.Auth,
		RequiredHeaders: be.RequiredHeaders,
		Query:           mergeQuery(be.Query, tgt.Query),
		Credential:      cred,
		Alias:           tgt.Alias,
		Weight:          tgt.Weight,
	}, nil
}

// mergeQuery returns base ∪ override with override winning. Returns nil when
// both are empty so callers can cheaply detect "no query params".
func mergeQuery(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// matchesModelPatterns mirrors cmd/gateway/binding.go: empty patterns match
// everything (the protocol catch-all — default-permissive, invariant #1),
// trailing-`*` is a prefix match, otherwise exact-equal.
func matchesModelPatterns(model string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if strings.HasSuffix(p, "*") {
			if strings.HasPrefix(model, p[:len(p)-1]) {
				return true
			}
			continue
		}
		if p == model {
			return true
		}
	}
	return false
}

// PassthroughMatch is the resolved outcome of an opaque passthrough family
// match. The request is proxied verbatim to Provider with the family's auth and
// any captured path params; no typed parsing, no GenAI telemetry, no capture.
type PassthroughMatch struct {
	// Family is the passthrough family name (telemetry / diagnostics).
	Family string

	// Provider is the provider name the family is bound to.
	Provider string

	// BaseURL is the provider connection root.
	BaseURL string

	// Auth is the family's credential convention; nil = provider-native.
	Auth *contractsconfig.ProviderAuth

	// RequiredHeaders are the provider's always-injected headers.
	RequiredHeaders map[string]string

	// Query is the provider's default query params.
	Query map[string]string

	// Credential is the configuration's credential for the provider.
	Credential string

	// Tags are the passthrough binding's tags.
	Tags []string

	// Params are the captured `{name}` path values.
	Params map[string]string
}

// MatchPassthrough resolves an opaque passthrough request. It walks the
// configuration's passthrough bindings, matching the path against each exposed
// family's path patterns and the method against that path's method set.
// ErrNoPassthrough when no family claims the path; ErrMethodNotAllowed when one
// claims the path but not the method.
func MatchPassthrough(
	method, path string,
	cfg contractsconfig.Configuration,
	providers contractsconfig.ProvidersConfig,
) (PassthroughMatch, error) {
	up := strings.ToUpper(method)
	methodSeen := false

	for _, pb := range cfg.PassthroughBindings {
		be, ok := providers[pb.Provider]
		if !ok {
			return PassthroughMatch{}, fmt.Errorf("selection: passthrough binding references unknown provider %q", pb.Provider)
		}
		fam, ok := be.Passthrough[pb.Family]
		if !ok {
			return PassthroughMatch{}, fmt.Errorf("selection: provider %q has no passthrough family %q", pb.Provider, pb.Family)
		}
		for _, pp := range fam.Paths {
			params, matched := matchPath(pp.Match, path)
			if !matched {
				continue
			}
			if !hasMethod(pp.Methods, up) {
				methodSeen = true
				continue
			}
			cred := cfg.Credentials[pb.Provider]
			return PassthroughMatch{
				Family:          pb.Family,
				Provider:        pb.Provider,
				BaseURL:         be.BaseURL,
				Auth:            fam.Auth,
				RequiredHeaders: be.RequiredHeaders,
				Query:           be.Query,
				Credential:      cred,
				Tags:            pb.Tags,
				Params:          params,
			}, nil
		}
	}
	if methodSeen {
		return PassthroughMatch{}, ErrMethodNotAllowed
	}
	return PassthroughMatch{}, ErrNoPassthrough
}

// placeholder matches a `{name}` segment in a passthrough path pattern.
var placeholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// matchPath matches an inbound path against a pattern that may contain `{name}`
// placeholders, returning the captured params. A pattern with no placeholders
// is an exact-string compare.
func matchPath(pattern, path string) (map[string]string, bool) {
	if !strings.ContainsRune(pattern, '{') {
		if pattern == path {
			return nil, true
		}
		return nil, false
	}
	var (
		names []string
		b     strings.Builder
	)
	b.WriteByte('^')
	idx := 0
	for _, loc := range placeholder.FindAllStringSubmatchIndex(pattern, -1) {
		start, end := loc[0], loc[1]
		nameStart, nameEnd := loc[2], loc[3]
		b.WriteString(regexp.QuoteMeta(pattern[idx:start]))
		b.WriteString(`([^/]+)`)
		names = append(names, pattern[nameStart:nameEnd])
		idx = end
	}
	b.WriteString(regexp.QuoteMeta(pattern[idx:]))
	b.WriteByte('$')

	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, false
	}
	sub := re.FindStringSubmatch(path)
	if sub == nil {
		return nil, false
	}
	params := make(map[string]string, len(names))
	for i, name := range names {
		params[name] = sub[i+1]
	}
	return params, true
}

// hasMethod reports whether want (already upper-cased) is in methods
// (case-insensitively).
func hasMethod(methods []string, want string) bool {
	for _, m := range methods {
		if strings.ToUpper(strings.TrimSpace(m)) == want {
			return true
		}
	}
	return false
}
