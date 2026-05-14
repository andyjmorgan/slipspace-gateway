package config

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	koanfyaml "github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"gopkg.in/yaml.v3"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
)

const (
	keyGateway        = "gateway"
	keyProviders      = "providers"
	keyConfigurations = "configurations"
	keyAPIKeys        = "api_keys"
)

var knownTopLevelKeys = map[string]struct{}{
	keyGateway:        {},
	keyProviders:      {},
	keyConfigurations: {},
	keyAPIKeys:        {},
}

// Load reads the YAML configuration directory at dir and returns the merged,
// validated, indexed runtime view.
//
// Files are merged by top-level key — duplicate keys across files are rejected
// rather than silently overlaid, because file ordering would otherwise
// silently determine policy.
func Load(ctx context.Context, dir string) (*ResolvedConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("config: load %q: %w", dir, err)
	}

	files, err := listYAMLFiles(dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("config: load %q: %w", dir, ErrEmptyDirectory)
	}

	merged, err := mergeFiles(files)
	if err != nil {
		return nil, err
	}

	resolved, err := decode(merged)
	if err != nil {
		return nil, err
	}

	if err := resolved.Validate(); err != nil {
		return nil, err
	}

	resolved.buildIndexes()
	return resolved, nil
}

func listYAMLFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("config: read dir %q: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Strings(out)
	return out, nil
}

// mergedTree holds the per-top-level-key yaml.Node assembled across the
// configuration directory, alongside the filename that contributed each block
// so duplicate-key errors can name both offenders.
type mergedTree struct {
	nodes  map[string]*yaml.Node
	origin map[string]string
}

func mergeFiles(files []string) (*mergedTree, error) {
	out := &mergedTree{
		nodes:  make(map[string]*yaml.Node),
		origin: make(map[string]string),
	}

	for _, path := range files {
		topLevel, err := parseTopLevel(path)
		if err != nil {
			return nil, err
		}
		for key, node := range topLevel {
			if _, ok := knownTopLevelKeys[key]; !ok {
				return nil, fmt.Errorf("config: %q in %s: %w", key, path, ErrUnknownTopLevelKey)
			}
			if existing, dup := out.origin[key]; dup {
				return nil, fmt.Errorf(
					"config: top-level key %q defined in both %s and %s: %w",
					key, existing, path, ErrDuplicateTopLevelKey,
				)
			}
			out.nodes[key] = node
			out.origin[key] = path
		}
	}

	return out, nil
}

// parseTopLevel loads path through koanf (the approved file+YAML layer), then
// re-marshals the tree to bytes and reparses with yaml.v3 to obtain per-key
// yaml.Node trees.
//
// The round-trip preserves verbatim rules/resilience subtrees while
// normalizing input to a guaranteed mapping shape; that lets the second-stage
// parser stay branch-free.
func parseTopLevel(path string) (map[string]*yaml.Node, error) {
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), koanfyaml.Parser()); err != nil {
		return nil, fmt.Errorf("config: load %s: %w: %w", path, ErrParse, err)
	}

	raw, err := k.Marshal(koanfyaml.Parser())
	if err != nil {
		return nil, fmt.Errorf("config: round-trip %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w: %w", path, ErrParse, err)
	}

	if doc.Kind == 0 || len(doc.Content) == 0 {
		return map[string]*yaml.Node{}, nil
	}
	root := doc.Content[0]

	out := make(map[string]*yaml.Node, len(root.Content)/2)
	for i := 0; i+1 < len(root.Content); i += 2 {
		out[root.Content[i].Value] = root.Content[i+1]
	}
	return out, nil
}

func decode(merged *mergedTree) (*ResolvedConfig, error) {
	out := &ResolvedConfig{
		Providers:      contractsconfig.ProvidersConfig{},
		Configurations: contractsconfig.ConfigurationsConfig{},
	}

	if node, ok := merged.nodes[keyGateway]; ok {
		if err := node.Decode(&out.Gateway); err != nil {
			return nil, fmt.Errorf("config: decode gateway: %w: %w", ErrParse, err)
		}
	}
	if node, ok := merged.nodes[keyProviders]; ok {
		if err := node.Decode(&out.Providers); err != nil {
			return nil, fmt.Errorf("config: decode providers: %w: %w", ErrParse, err)
		}
	}
	if node, ok := merged.nodes[keyConfigurations]; ok {
		if err := node.Decode(&out.Configurations); err != nil {
			return nil, fmt.Errorf("config: decode configurations: %w: %w", ErrParse, err)
		}
	}
	if node, ok := merged.nodes[keyAPIKeys]; ok {
		if err := node.Decode(&out.APIKeys); err != nil {
			return nil, fmt.Errorf("config: decode api_keys: %w: %w", ErrParse, err)
		}
	}
	return out, nil
}

// Validate enforces cross-block invariants — every allowed_endpoint resolves
// to a real provider.endpoint, every api_key references a known configuration,
// every provider with prefix_required has a prefix, and no two endpoints can
// claim the same fully-resolved route path.
//
// Returns the first violation as a wrapped sentinel error.
func (r *ResolvedConfig) Validate() error {
	if len(r.Configurations) == 0 {
		return fmt.Errorf("config: validate: %w", ErrNoConfigurations)
	}

	if r.Gateway.HTTP.Bind != "" {
		if err := validateBind(r.Gateway.HTTP.Bind); err != nil {
			return err
		}
	}

	for name, cfg := range r.Configurations {
		for _, allowed := range cfg.AllowedEndpoints {
			provider, endpoint, err := splitProviderEndpoint(allowed)
			if err != nil {
				return fmt.Errorf("config: configuration %q allowed_endpoint %q: %w", name, allowed, err)
			}
			p, ok := r.Providers[provider]
			if !ok {
				return fmt.Errorf(
					"config: configuration %q allowed_endpoint %q: %w (no such provider)",
					name, allowed, ErrEndpointNotInProvider,
				)
			}
			if _, ok := p.Endpoints[endpoint]; !ok {
				return fmt.Errorf(
					"config: configuration %q allowed_endpoint %q: %w (provider %q has no endpoint %q)",
					name, allowed, ErrEndpointNotInProvider, provider, endpoint,
				)
			}
		}
	}

	for i, key := range r.APIKeys {
		if _, ok := r.Configurations[key.Configuration]; !ok {
			return fmt.Errorf(
				"config: api_keys[%d] %q: %w (name=%q)",
				i, key.Configuration, ErrUnknownConfiguration, key.Name,
			)
		}
	}

	seen := make(map[string]Route)
	providerNames := make([]string, 0, len(r.Providers))
	for name := range r.Providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	for _, providerName := range providerNames {
		p := r.Providers[providerName]
		if p.PrefixRequired && p.Prefix == "" {
			return fmt.Errorf("config: provider %q: %w", providerName, ErrPrefixRequiredEmpty)
		}
		endpointNames := make([]string, 0, len(p.Endpoints))
		for name := range p.Endpoints {
			endpointNames = append(endpointNames, name)
		}
		sort.Strings(endpointNames)
		for _, endpointName := range endpointNames {
			e := p.Endpoints[endpointName]
			for _, route := range emitRoutes(providerName, endpointName, p, e) {
				if prev, dup := seen[route.Path]; dup {
					return fmt.Errorf(
						"config: route %q claimed by %s.%s and %s.%s: %w",
						route.Path, prev.Provider, prev.Endpoint, providerName, endpointName, ErrPathCollision,
					)
				}
				seen[route.Path] = Route{Provider: providerName, Endpoint: endpointName}
			}
		}
	}

	return nil
}

// emittedRoute is one fully-resolved RouteIndex entry for a
// (provider, endpoint, accepted_path) triple.
type emittedRoute struct {
	Path     string
	Provider string
	Endpoint string
}

// emitRoutes expands a (provider, endpoint) pair into every accepted_path it
// claims, generating both the prefixed and bare forms unless prefix_required
// pins it to prefixed-only.
func emitRoutes(providerName, endpointName string, p contractsconfig.Provider, e contractsconfig.Endpoint) []emittedRoute {
	out := make([]emittedRoute, 0, len(e.AcceptedPaths)*2)
	for _, ap := range e.AcceptedPaths {
		if p.Prefix != "" {
			out = append(out, emittedRoute{
				Path:     "/" + p.Prefix + ap,
				Provider: providerName,
				Endpoint: endpointName,
			})
		}
		if !p.PrefixRequired {
			out = append(out, emittedRoute{
				Path:     ap,
				Provider: providerName,
				Endpoint: endpointName,
			})
		}
	}
	return out
}

func (r *ResolvedConfig) buildIndexes() {
	r.SecretIndex = make(map[string]*contractsconfig.APIKey, len(r.APIKeys))
	for i := range r.APIKeys {
		key := &r.APIKeys[i]
		r.SecretIndex[key.Secret] = key
	}

	r.ConfigurationIndex = make(map[string]*contractsconfig.Configuration, len(r.Configurations))
	for name, cfg := range r.Configurations {
		entry := cfg
		r.ConfigurationIndex[name] = &entry
	}

	r.RouteIndex = make(map[string]Route)
	for providerName, p := range r.Providers {
		for endpointName, e := range p.Endpoints {
			for _, route := range emitRoutes(providerName, endpointName, p, e) {
				r.RouteIndex[route.Path] = Route{Provider: route.Provider, Endpoint: route.Endpoint}
			}
		}
	}
}

func splitProviderEndpoint(s string) (string, string, error) {
	provider, endpoint, ok := strings.Cut(s, ".")
	if !ok || provider == "" || endpoint == "" {
		return "", "", ErrMalformedAllowedEndpoint
	}
	return provider, endpoint, nil
}

func validateBind(bind string) error {
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		return fmt.Errorf("config: gateway.http.bind=%q: %w: %w", bind, ErrInvalidBind, err)
	}
	if host == "" && port == "" {
		return fmt.Errorf("config: gateway.http.bind=%q: %w", bind, ErrInvalidBind)
	}
	if _, perr := strconv.Atoi(port); perr != nil {
		return fmt.Errorf("config: gateway.http.bind=%q: %w: port not numeric", bind, ErrInvalidBind)
	}
	return nil
}
