// Package routing maps inbound HTTP requests to the (provider, endpoint) pair
// that owns them, based on the route table built by the config loader.
package routing

import (
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// Router matches incoming HTTP requests to (provider, endpoint) pairs.
//
// Internal state (the exact map + compiled pattern slice) is held behind
// an atomic.Pointer so a config.Store.Replace can swap the route table
// in lockstep with the new ResolvedConfig snapshot without blocking
// in-flight Resolve calls. Resolve reads state with a single atomic load
// and walks the snapshot it observed for the rest of the call, so a swap
// that lands mid-request still produces a consistent answer (either the
// pre-swap or the post-swap table — never a torn mix).
type Router struct {
	// state carries the active route table. Replaced atomically by the
	// store subscriber on every successful config swap.
	state atomic.Pointer[routerState]
}

// routerState is the per-snapshot derived form of the route table.
type routerState struct {
	// exact holds literal paths. Lookup is a single map probe.
	exact map[string]bound

	// patterns holds compiled regex routes. Iterated in lexicographic
	// order of the source path so a config that defines two overlapping
	// patterns resolves deterministically across restarts.
	patterns []patternedRoute
}

// bound is the per-exact-path payload stored in Router.exact.
type bound struct {
	Provider string

	Endpoint string

	// AcceptedPath is the un-prefixed accepted_paths value this route
	// was emitted from. Surfaced on Match so the destination builder
	// can mirror it as the upstream path when the endpoint declares no
	// explicit Path.
	AcceptedPath string

	// Methods is the set of uppercase HTTP verbs allowed at this route,
	// pre-normalized at build time so Resolve only does a map probe.
	Methods map[string]struct{}
}

// patternedRoute is a single placeholder-bearing route compiled to a
// regex. ParamNames carries the names of the `{...}` segments in left-to-
// right order; the i-th regex capture group corresponds to ParamNames[i].
type patternedRoute struct {
	// Path is the prefixed gateway-side route key. Retained for
	// diagnostics and for the lexicographic sort that gives Resolve a
	// stable iteration order.
	Path string

	// AcceptedPath is the un-prefixed accepted_paths value this pattern
	// was emitted from. Surfaced on Match so the destination builder
	// can mirror it as the upstream path when the endpoint declares no
	// explicit Path.
	AcceptedPath string

	// Regex is the anchored compiled pattern built by compilePattern.
	Regex *regexp.Regexp

	// ParamNames are the placeholder identifiers in left-to-right order.
	// Empty for patterns that contain no `{name}` segments.
	ParamNames []string

	// Methods is the set of uppercase HTTP verbs allowed at this route.
	Methods map[string]struct{}

	Provider string

	Endpoint string
}

// Match is the result of a successful route resolution.
type Match struct {
	Provider string

	Endpoint string

	// MatchedPath is the un-prefixed accepted_paths value that owned
	// the route (e.g. "/v1beta/models/{model}:generateContent"). The
	// destination builder uses this as the upstream path template when
	// the endpoint declares no explicit Path — supports folding
	// streaming and non-streaming variants of the same wire shape into
	// one endpoint entry.
	MatchedPath string

	// Params carries the captured `{name}` values for patterned matches,
	// keyed by placeholder name. Nil for exact-path matches and for
	// patterned routes that declare no placeholders.
	Params map[string]string
}

// New builds a Router from the live snapshot of store and registers a
// subscriber that rebuilds the internal route table on every subsequent
// successful Replace. The initial build runs synchronously here so a
// startup with a broken route table fails fast; later rebuilds happen
// inside the subscriber and are silent — the admin-write path runs
// Validate on its clone before calling Replace, so an invalid table
// never reaches here at runtime.
//
// logger is used only by the subscriber's failure path; nil is safe and
// downgrades the failure to a silent state-retain.
func New(store *config.Store, logger *slog.Logger) (*Router, error) {
	if store == nil {
		return nil, fmt.Errorf("routing: new: store is nil")
	}

	r := &Router{}
	st, err := buildState(store.Snapshot())
	if err != nil {
		return nil, err
	}
	r.state.Store(st)

	// Subscribe fires immediately with the current snapshot; skip that
	// first invocation since the synchronous build above already produced
	// the same state. Subsequent fires are real Replace events.
	first := true
	store.Subscribe(func(resolved *config.ResolvedConfig) {
		if first {
			first = false
			return
		}
		next, err := buildState(resolved)
		if err != nil {
			if logger != nil {
				logger.Error("routing: rebuild on config swap failed; retaining previous route table", slog.String("error", err.Error()))
			}
			return
		}
		r.state.Store(next)
	})

	return r, nil
}

// buildState produces a fully indexed routerState from resolved. Errors
// here are the same shape the v0 New produced — they describe an
// inconsistency between the route index and the providers tree (an
// unknown provider, an endpoint with no declared methods, an
// uncompilable pattern). Validate catches every such case on the clone
// in the admin-write path, so at runtime this only fires for an initial
// startup load.
func buildState(resolved *config.ResolvedConfig) (*routerState, error) {
	if resolved == nil {
		return nil, fmt.Errorf("routing: build: resolved config is nil")
	}

	st := &routerState{
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
			st.exact[path] = bound{
				Provider:     route.Provider,
				Endpoint:     route.Endpoint,
				AcceptedPath: route.AcceptedPath,
				Methods:      methods,
			}
			continue
		}

		re, params, err := compilePattern(path)
		if err != nil {
			return nil, err
		}
		st.patterns = append(st.patterns, patternedRoute{
			Path:         path,
			AcceptedPath: route.AcceptedPath,
			Regex:        re,
			ParamNames:   params,
			Methods:      methods,
			Provider:     route.Provider,
			Endpoint:     route.Endpoint,
		})
	}

	sort.Slice(st.patterns, func(i, j int) bool {
		return st.patterns[i].Path < st.patterns[j].Path
	})

	return st, nil
}

// Resolve returns the (provider, endpoint) match for an inbound request,
// or ErrNoRoute / ErrMethodNotAllowed.
//
// Lookup order is exact map first, then patterns in their stored
// (lexicographic) order. An exact-path entry always wins over a pattern
// that would also match the same literal path. Method matching happens
// after path matching, so a path-matched-but-method-rejected request
// reports ErrMethodNotAllowed rather than falling through to ErrNoRoute.
func (r *Router) Resolve(method, path string) (Match, error) {
	st := r.state.Load()
	m := strings.ToUpper(method)

	if b, ok := st.exact[path]; ok {
		if _, allowed := b.Methods[m]; !allowed {
			return Match{}, ErrMethodNotAllowed
		}
		return Match{
			Provider:    b.Provider,
			Endpoint:    b.Endpoint,
			MatchedPath: b.AcceptedPath,
		}, nil
	}

	for i := range st.patterns {
		p := &st.patterns[i]
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
		return Match{
			Provider:    p.Provider,
			Endpoint:    p.Endpoint,
			MatchedPath: p.AcceptedPath,
			Params:      params,
		}, nil
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
