// Package routing maps inbound HTTP requests to the (provider, endpoint) pair
// that owns them, based on the route table built by the config loader.
package routing

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// Router matches incoming HTTP requests to (provider, endpoint) pairs.
type Router struct {
	exact map[string]bound

	patterns []patternedRoute
}

type bound struct {
	Provider string

	Endpoint string

	Methods map[string]struct{}
}

type patternedRoute struct {
	Path string

	Regex *regexp.Regexp

	ParamNames []string

	Methods map[string]struct{}

	Provider string

	Endpoint string
}

// Match is the result of a successful route resolution.
type Match struct {
	Provider string

	Endpoint string

	Params map[string]string
}

// New builds a Router from the resolved config. The methods allowed for each
// route are stashed at build time so Resolve is a single map lookup on the
// exact-match path.
func New(resolved *config.ResolvedConfig) (*Router, error) {
	if resolved == nil {
		return nil, fmt.Errorf("routing: new: resolved config is nil")
	}

	r := &Router{
		exact: make(map[string]bound),
	}

	paths := make([]string, 0, len(resolved.RouteIndex))
	for p := range resolved.RouteIndex {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		route := resolved.RouteIndex[path]
		methods, err := methodSetFor(resolved, route)
		if err != nil {
			return nil, err
		}

		if !strings.ContainsRune(path, '{') {
			r.exact[path] = bound{
				Provider: route.Provider,
				Endpoint: route.Endpoint,
				Methods:  methods,
			}
			continue
		}

		re, params, err := compilePattern(path)
		if err != nil {
			return nil, err
		}
		r.patterns = append(r.patterns, patternedRoute{
			Path:       path,
			Regex:      re,
			ParamNames: params,
			Methods:    methods,
			Provider:   route.Provider,
			Endpoint:   route.Endpoint,
		})
	}

	sort.Slice(r.patterns, func(i, j int) bool {
		return r.patterns[i].Path < r.patterns[j].Path
	})

	return r, nil
}

// Resolve returns the (provider, endpoint) match for an inbound request, or
// ErrNoRoute / ErrMethodNotAllowed.
func (r *Router) Resolve(method, path string) (Match, error) {
	m := strings.ToUpper(method)

	if b, ok := r.exact[path]; ok {
		if _, allowed := b.Methods[m]; !allowed {
			return Match{}, ErrMethodNotAllowed
		}
		return Match{Provider: b.Provider, Endpoint: b.Endpoint}, nil
	}

	for i := range r.patterns {
		p := &r.patterns[i]
		sub := p.Regex.FindStringSubmatch(path)
		if sub == nil {
			continue
		}
		if _, allowed := p.Methods[m]; !allowed {
			return Match{}, ErrMethodNotAllowed
		}
		var params map[string]string
		if len(p.ParamNames) > 0 {
			params = make(map[string]string, len(p.ParamNames))
			for idx, name := range p.ParamNames {
				params[name] = sub[idx+1]
			}
		}
		return Match{Provider: p.Provider, Endpoint: p.Endpoint, Params: params}, nil
	}

	return Match{}, ErrNoRoute
}

// methodSetFor extracts the case-normalized allowed methods for the endpoint
// referenced by route. The route always points at an existing provider and
// endpoint — the config loader rejected the config otherwise — so a missing
// lookup here is treated as a programming error, not a runtime config error.
func methodSetFor(resolved *config.ResolvedConfig, route config.Route) (map[string]struct{}, error) {
	provider, ok := resolved.Providers[route.Provider]
	if !ok {
		return nil, fmt.Errorf("routing: new: route references unknown provider %q", route.Provider)
	}
	endpoint, ok := provider.Endpoints[route.Endpoint]
	if !ok {
		return nil, fmt.Errorf(
			"routing: new: route references unknown endpoint %q on provider %q",
			route.Endpoint, route.Provider,
		)
	}
	if len(endpoint.Method) == 0 {
		return nil, fmt.Errorf(
			"routing: new: %s.%s has no methods declared",
			route.Provider, route.Endpoint,
		)
	}
	out := make(map[string]struct{}, len(endpoint.Method))
	for _, m := range endpoint.Method {
		out[strings.ToUpper(strings.TrimSpace(m))] = struct{}{}
	}
	return out, nil
}

// placeholder matches a `{name}` segment inside an accepted_paths entry. Names
// are restricted to identifier-like characters to keep the regex translation
// deterministic.
var placeholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// compilePattern translates an accepted_paths entry that contains `{name}`
// placeholders into an anchored regex, returning the regex plus the parameter
// names in left-to-right order.
func compilePattern(path string) (*regexp.Regexp, []string, error) {
	var (
		params []string
		b      strings.Builder
	)
	b.WriteByte('^')

	idx := 0
	for _, loc := range placeholder.FindAllStringSubmatchIndex(path, -1) {
		start, end := loc[0], loc[1]
		nameStart, nameEnd := loc[2], loc[3]

		b.WriteString(regexp.QuoteMeta(path[idx:start]))
		b.WriteString(`([^/]+)`)
		params = append(params, path[nameStart:nameEnd])
		idx = end
	}
	b.WriteString(regexp.QuoteMeta(path[idx:]))
	b.WriteByte('$')

	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, nil, fmt.Errorf("routing: compile %q: %w", path, err)
	}
	return re, params, nil
}
