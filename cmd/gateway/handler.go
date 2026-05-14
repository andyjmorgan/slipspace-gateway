package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/httperr"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
	"github.com/andyjmorgan/sluice-gateway/internal/routing"
)

// buildDataPlaneHandler composes routing, auth, bodycapture and the forwarder
// terminal into a single http.Handler. The order is fixed: routing fires first
// so downstream stages can read the resolved (provider, endpoint) off the
// context; the forwarder runs last and emits the upstream request.
func buildDataPlaneHandler(
	router *routing.Router,
	resolver *auth.Resolver,
	forwarder *proxy.Forwarder,
	providers contractsconfig.ProvidersConfig,
	errs *httperr.Writer,
	_ *slog.Logger,
) http.Handler {
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := observability.FromContext(ctx)

		match, ok := matchFromContext(ctx)
		if !ok {
			log.ErrorContext(ctx, "forwarder: no route on context")
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}
		authResult, ok := auth.FromContext(ctx)
		if !ok {
			log.ErrorContext(ctx, "forwarder: no auth on context")
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}

		provider, ok := providers[match.Provider]
		if !ok {
			log.ErrorContext(ctx, "forwarder: unknown provider", "provider", match.Provider)
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}
		endpoint, ok := provider.Endpoints[match.Endpoint]
		if !ok {
			log.ErrorContext(ctx, "forwarder: unknown endpoint", "provider", match.Provider, "endpoint", match.Endpoint)
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}

		if s := reqStateFromContext(ctx); s != nil {
			s.mu.Lock()
			s.provider = match.Provider
			s.endpoint = match.Endpoint
			s.mu.Unlock()
		}

		dest, err := buildDestination(provider, endpoint, match, authResult, r)
		if err != nil {
			log.ErrorContext(ctx, "forwarder: destination", "err", err.Error())
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}

		if err := forwarder.Forward(ctx, w, r, dest); err != nil {
			log.ErrorContext(ctx, "forwarder: forward", "err", err.Error())
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "forward_failed", "internal error")
			return
		}
	})

	kindFrom := makeKindFromContext(providers)

	var h http.Handler = final
	h = bodycapture.HTTPHandler(kindFrom, h)
	h = auth.HTTPHandler(resolver, routeFromContext, h)
	h = routingMiddleware(router, errs, h)
	return h
}

// routeFromContext adapts matchFromContext to the auth middleware's expected
// signature so the package does not need to import internal/routing.
func routeFromContext(ctx context.Context) (string, string, bool) {
	m, ok := matchFromContext(ctx)
	if !ok {
		return "", "", false
	}
	return m.Provider, m.Endpoint, true
}

// makeKindFromContext returns a closure that resolves the routed endpoint's
// RequestKind via the providers map. Endpoints with no explicit kind fall back
// to passthrough so the body capture middleware still buffers them.
func makeKindFromContext(providers contractsconfig.ProvidersConfig) bodycapture.KindFromContextFunc {
	return func(ctx context.Context) (bodycapture.RequestKind, bool) {
		m, ok := matchFromContext(ctx)
		if !ok {
			return "", false
		}
		p, ok := providers[m.Provider]
		if !ok {
			return "", false
		}
		e, ok := p.Endpoints[m.Endpoint]
		if !ok {
			return "", false
		}
		kind := strings.TrimSpace(e.RequestKind)
		if kind == "" {
			return bodycapture.KindPassthrough, true
		}
		return bodycapture.RequestKind(kind), true
	}
}

// buildDestination resolves the upstream URL and outgoing headers for a single
// request. The endpoint's canonical Path field is the upstream template;
// `{name}` placeholders are substituted from match.Params, and the result is
// joined onto the provider's BaseURL. In passthrough mode the inbound
// Authorization header is forwarded verbatim because the forwarder always
// drops Authorization unless OutgoingHeaders re-sets it.
func buildDestination(
	provider contractsconfig.Provider,
	endpoint contractsconfig.Endpoint,
	match routing.Match,
	authResult auth.AuthResult,
	req *http.Request,
) (proxy.Destination, error) {
	baseURL, err := url.Parse(provider.BaseURL)
	if err != nil {
		return proxy.Destination{}, err
	}

	upstreamPath := substitutePlaceholders(endpoint.Path, match.Params)
	upstream := *baseURL
	upstream.Path = joinPaths(baseURL.Path, upstreamPath)
	upstream.RawPath = ""

	outgoing := http.Header{}
	for k, v := range provider.RequiredHeaders {
		outgoing.Set(k, v)
	}
	for k, vs := range authResult.SetHeaders {
		for _, v := range vs {
			outgoing.Add(k, v)
		}
	}
	if authResult.Mode == auth.ModePassthrough {
		if inbound := req.Header.Get(auth.HeaderAuthorization); inbound != "" {
			if outgoing.Get(auth.HeaderAuthorization) == "" {
				outgoing.Set(auth.HeaderAuthorization, inbound)
			}
		}
	}

	return proxy.Destination{
		BaseURL:         baseURL,
		UpstreamURL:     &upstream,
		OutgoingHeaders: outgoing,
		DropHeaders:     authResult.DropHeaders,
	}, nil
}

func substitutePlaceholders(path string, params map[string]string) string {
	if len(params) == 0 || !strings.ContainsRune(path, '{') {
		return path
	}
	out := path
	for name, value := range params {
		out = strings.ReplaceAll(out, "{"+name+"}", value)
	}
	return out
}

func joinPaths(base, target string) string {
	if base == "" {
		return target
	}
	if target == "" {
		return base
	}
	switch {
	case strings.HasSuffix(base, "/") && strings.HasPrefix(target, "/"):
		return base + target[1:]
	case !strings.HasSuffix(base, "/") && !strings.HasPrefix(target, "/"):
		return base + "/" + target
	default:
		return base + target
	}
}
