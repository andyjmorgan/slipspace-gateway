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
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
	"github.com/andyjmorgan/sluice-gateway/internal/routing"
	"github.com/andyjmorgan/sluice-gateway/providers/anthropic/messages"
	openaichat "github.com/andyjmorgan/sluice-gateway/providers/openai/chat"
	openairesponses "github.com/andyjmorgan/sluice-gateway/providers/openai/responses"
)

// buildDataPlaneHandler composes routing, auth, bodycapture and the forwarder
// terminal into a single http.Handler. The order is fixed: routing fires first
// so downstream stages can read the resolved (provider, endpoint) off the
// context; the forwarder runs last and emits the upstream request.
func buildDataPlaneHandler(
	router *routing.Router,
	resolver *auth.Resolver,
	forwarder *proxy.Forwarder,
	evaluator *rules.Evaluator,
	observerFactory proxy.ObserverFactory,
	providers contractsconfig.ProvidersConfig,
	meters *observability.Meters,
	errs *httperr.Writer,
	_ *slog.Logger,
) http.Handler {
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := observability.FromContext(ctx)

		state := rules.MutableStateFromContext(ctx)
		if state == nil {
			log.ErrorContext(ctx, "forwarder: no rules state on context")
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}
		authResult, ok := auth.FromContext(ctx)
		if !ok {
			log.ErrorContext(ctx, "forwarder: no auth on context")
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}

		provider, ok := providers[state.Provider]
		if !ok {
			log.ErrorContext(ctx, "forwarder: unknown provider", "provider", state.Provider)
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}
		// Endpoint lookup is only load-bearing when the destination
		// builder needs to derive the upstream path from the
		// endpoint's template (state.UpstreamURL == nil case). A rule
		// that retargets via ChangeUrl supplies the full URL, so the
		// endpoint name on the new provider doesn't need to exist —
		// matches the .NET predecessor's behaviour and lets rules
		// route openai.chat_completions → anthropic.messages cleanly.
		endpoint, endpointOK := provider.Endpoints[state.Endpoint]
		if !endpointOK && state.UpstreamURL == nil {
			log.ErrorContext(ctx, "forwarder: unknown endpoint", "provider", state.Provider, "endpoint", state.Endpoint)
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}

		captured, _ := bodycapture.FromContext(ctx)
		ctx = observability.WithRequestLabels(ctx, observability.RequestLabels{
			Provider: state.Provider,
			Endpoint: state.Endpoint,
			Model:    outboundModel(captured, state),
		})

		dest, err := buildDestination(provider, endpoint, state, authResult, r)
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
	h = rules.BodyRemarshalHandler(meters, h)
	h = rules.HTTPHandler(evaluator, ruleMatchFromContext, observerFactory, h)
	h = bodycapture.HTTPHandler(kindFrom, h)
	h = auth.HTTPHandler(resolver, routeFromContext, h)
	h = routingMiddleware(router, errs, h)
	return h
}

// ruleMatchFromContext adapts matchFromContext to the rules
// middleware's MatchFromContextFunc signature, so the rules package
// does not need to import internal/routing.
func ruleMatchFromContext(ctx context.Context) (string, string, map[string]string, bool) {
	m, ok := matchFromContext(ctx)
	if !ok {
		return "", "", nil, false
	}
	return m.Provider, m.Endpoint, m.Params, true
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

// buildDestination resolves the upstream URL and outgoing headers
// from the post-rule MutableState. Resolution order:
//
//  1. UpstreamURL — if a rule wrote an explicit override via
//     ChangeUrlAction, use it verbatim. Otherwise compute the URL
//     from the (provider, endpoint) under the post-rule provider's
//     BaseURL with {name} placeholders substituted from
//     state.PathParams.
//  2. Outgoing headers — provider.RequiredHeaders + auth-resolved
//     SetHeaders + state.OutgoingHeaders. Rule-supplied headers
//     overlay the auth defaults; passthrough Authorization is
//     forwarded verbatim when present.
//
// In passthrough mode the inbound Authorization header is forwarded
// verbatim because the forwarder always drops Authorization unless
// OutgoingHeaders re-sets it.
func buildDestination(
	provider contractsconfig.Provider,
	endpoint contractsconfig.Endpoint,
	state *rules.MutableState,
	authResult auth.AuthResult,
	req *http.Request,
) (proxy.Destination, error) {
	baseURL, err := url.Parse(provider.BaseURL)
	if err != nil {
		return proxy.Destination{}, err
	}

	var upstream url.URL
	if state.UpstreamURL != nil {
		upstream = *state.UpstreamURL
	} else {
		upstream = *baseURL
		upstreamPath := substitutePlaceholders(endpoint.Path, state.PathParams)
		upstream.Path = joinPaths(baseURL.Path, upstreamPath)
		upstream.RawPath = ""
	}

	if len(state.QueryAdditions) > 0 {
		q := upstream.Query()
		for _, add := range state.QueryAdditions {
			q.Add(add.Key, add.Value)
		}
		upstream.RawQuery = q.Encode()
	}

	outgoing := http.Header{}
	for k, v := range provider.RequiredHeaders {
		outgoing.Set(k, v)
	}
	for k, vs := range authResult.SetHeaders {
		for _, v := range vs {
			outgoing.Add(k, v)
		}
	}
	// Re-mint the credential header when a rule changed the provider
	// or supplied an explicit override. The auth middleware sets
	// SetHeaders for the original provider; rule mutations need the
	// destination builder to update it. Resolution order:
	//
	//   1. ChangeApiKey explicit override: state.UpstreamCredentialOverride
	//      replaces the credential value, formatted per state.Provider.
	//   2. ChangeProvider without explicit override: look up the new
	//      provider's credential in Configuration.UpstreamCredentials
	//      (managed-mode) and mint with the new provider's format.
	//   3. Otherwise: leave SetHeaders as the auth middleware emitted.
	//
	// Empty override is the UseSluiceKey sentinel — handled by the
	// passthrough branch below.
	if state.UpstreamCredentialOverride != nil && *state.UpstreamCredentialOverride != "" {
		name, value := auth.UpstreamCredentialHeader(state.Provider, *state.UpstreamCredentialOverride)
		outgoing.Del(auth.HeaderAuthorization)
		outgoing.Del("X-Api-Key")
		outgoing.Del("X-Goog-Api-Key")
		outgoing.Set(name, value)
	} else if state.UpstreamCredentialOverride == nil &&
		authResult.Mode == auth.ModeManaged &&
		state.Provider != authResult.Provider &&
		authResult.Configuration != nil {
		if cred, ok := authResult.Configuration.UpstreamCredentials[state.Provider]; ok {
			name, value := auth.UpstreamCredentialHeader(state.Provider, cred)
			outgoing.Del(auth.HeaderAuthorization)
			outgoing.Del("X-Api-Key")
			outgoing.Del("X-Goog-Api-Key")
			outgoing.Set(name, value)
		}
	}
	for k, vs := range state.OutgoingHeaders {
		outgoing.Del(k)
		for _, v := range vs {
			outgoing.Add(k, v)
		}
	}
	if authResult.Mode == auth.ModePassthrough ||
		(state.UpstreamCredentialOverride != nil && *state.UpstreamCredentialOverride == "") {
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

// outboundModel returns the model identifier the gateway is about
// to send upstream — read at destination-finalisation time so it
// reflects rule mutations as well as routing.
//
// Resolution order:
//
//  1. state.PathParams["model"] — Gemini's path-based addressing,
//     and any rule that updated the {model} placeholder via
//     ChangeModelNameAction.
//  2. The Model field on the typed captured body for
//     chat/responses/messages. Captured.Body is a pointer so rule
//     mutations through it are visible here.
//
// Returns "" when no model is in scope (e.g. /v1/models listing
// endpoints with request_kind: passthrough). Callers are expected
// to sanitise the result before using it as a metric-label value.
func outboundModel(captured bodycapture.Captured, state *rules.MutableState) string {
	if state != nil {
		if v := strings.TrimSpace(state.PathParams["model"]); v != "" {
			return v
		}
	}
	switch b := captured.Body.(type) {
	case *openaichat.ChatCompletionRequest:
		return strings.TrimSpace(b.Model)
	case *openairesponses.ResponsesRequest:
		return strings.TrimSpace(b.Model)
	case *messages.MessagesRequest:
		return strings.TrimSpace(b.Model)
	}
	return ""
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
