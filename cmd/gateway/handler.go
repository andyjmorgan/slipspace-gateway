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
			Provider:      state.Provider,
			Endpoint:      state.Endpoint,
			Model:         outboundModel(captured, state),
			Configuration: authResult.ConfigurationName,
		})

		dest, err := buildDestination(provider, endpoint, state, authResult, r)
		if err != nil {
			log.ErrorContext(ctx, "forwarder: destination", "err", err.Error())
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}

		if _, err := forwarder.Forward(ctx, w, r, dest); err != nil {
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

// buildDestination resolves the upstream URL, outgoing headers, and
// drop list from the post-rule MutableState plus the auth decision.
// It is the single mint site for the upstream credential header —
// the auth middleware resolves identity + policy but does not touch
// credentials.
//
// URL resolution: UpstreamURL (rule ChangeUrl override) wins;
// otherwise compute from baseURL + endpoint template with PathParams
// substituted. QueryAdditions overlay on the final URL.
//
// Credential resolution follows this decision table on
// (authResult.Mode, state.UpstreamCredentialOverride, configured
// Configuration.UpstreamCredentials[state.Provider]):
//
//	managed + override non-nil and non-empty
//	    → drop inbound Authorization + cousins; set per state.Provider's format
//	managed + override == &""           (UseSluiceKey sentinel)
//	    → forward inbound Authorization verbatim
//	managed + override nil, cred non-empty
//	    → drop inbound Authorization + cousins; set per state.Provider's format
//	managed + override nil, cred empty or missing
//	    → drop inbound Authorization + cousins; SET NOTHING (private endpoint)
//	passthrough + override non-nil and non-empty
//	    → drop inbound Authorization + cousins; set per state.Provider's format
//	passthrough + override == &""       (UseSluiceKey sentinel; redundant)
//	    → forward inbound Authorization verbatim
//	passthrough + override nil
//	    → forward inbound Authorization verbatim (Claude Code path)
//
// After the credential decision, state.OutgoingHeaders (rule
// SetHeader writes) overlays. Rules win the last word on the wire.
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

	dropHeaders := append([]string(nil), authResult.DropHeaders...)

	switch credentialStrategy(authResult, state) {
	case credSetFromProvider:
		cred := credentialFor(authResult, state)
		name, value := resolveCredentialHeader(provider, endpoint, state.Provider, cred)
		outgoing.Set(name, value)
		// Drop every credential header name the inbound request
		// might carry that is NOT the one we just set, so a managed
		// → anthropic forwarder doesn't accidentally pass the
		// inbound openai-style Bearer through.
		for _, h := range credentialHeaderNames {
			if h != name {
				dropHeaders = appendUnique(dropHeaders, h)
			}
		}
	case credStripNoSet:
		// Private endpoint or missing credential mapping. Strip
		// every credential header — the inbound request must not
		// leak a token to an endpoint we did not authenticate it
		// against.
		for _, h := range credentialHeaderNames {
			dropHeaders = appendUnique(dropHeaders, h)
		}
	case credForwardInbound:
		// Passthrough or UseSluiceKey sentinel. The forwarder strips
		// Authorization unconditionally via alwaysDropHeaders, so
		// we re-inject the inbound value via OutgoingHeaders to make
		// the forwarder put it back on the upstream request.
		if inbound := req.Header.Get(auth.HeaderAuthorization); inbound != "" {
			outgoing.Set(auth.HeaderAuthorization, inbound)
		}
	}

	for k, vs := range state.OutgoingHeaders {
		outgoing.Del(k)
		for _, v := range vs {
			outgoing.Add(k, v)
		}
	}

	return proxy.Destination{
		BaseURL:         baseURL,
		UpstreamURL:     &upstream,
		OutgoingHeaders: outgoing,
		DropHeaders:     dropHeaders,
	}, nil
}

// credentialHeaderNames is the closed set of headers the gateway may
// inject (or strip) for upstream authentication. Other headers
// authored by rules via SetHeader are not constrained here.
var credentialHeaderNames = []string{
	auth.HeaderAuthorization,
	"X-Api-Key",
	"X-Goog-Api-Key",
}

// authFormatPlaceholder is the literal substring the destination builder
// substitutes with the resolved credential when an endpoint or provider
// declares a custom auth_format. Kept in sync with the loader's
// validation constant.
const authFormatPlaceholder = "{key}"

// resolveCredentialHeader returns the (header name, header value) the
// destination builder will set for managed-mode credentials, preferring
// — in order — the endpoint-level override, the provider-level override,
// and finally the per-provider default in auth.UpstreamCredentialHeader.
//
// An override is "in effect" when AuthHeader is non-empty at that level;
// AuthFormat is consulted alongside but defaults to "{key}" (raw
// credential) when only the header was overridden.
//
// The OpenAI-compat surfaces on Anthropic and Gemini both want
// `Authorization: Bearer {key}` rather than the provider's native
// header; the override path expresses that without splitting the
// destination builder by endpoint.
func resolveCredentialHeader(
	provider contractsconfig.Provider,
	endpoint contractsconfig.Endpoint,
	providerName, credential string,
) (string, string) {
	header, format := pickAuthHeaderAndFormat(provider, endpoint)
	if header == "" {
		return auth.UpstreamCredentialHeader(providerName, credential)
	}
	if format == "" {
		return header, credential
	}
	return header, strings.ReplaceAll(format, authFormatPlaceholder, credential)
}

// pickAuthHeaderAndFormat applies the endpoint-then-provider fallback for
// the auth header override. Returns ("", "") when neither level overrides,
// signalling the caller to fall back to the per-provider default.
func pickAuthHeaderAndFormat(p contractsconfig.Provider, e contractsconfig.Endpoint) (string, string) {
	if e.AuthHeader != "" {
		return e.AuthHeader, e.AuthFormat
	}
	if p.AuthHeader != "" {
		return p.AuthHeader, p.AuthFormat
	}
	return "", ""
}

// credStrategy is the credential decision derived from auth mode +
// rule state. The destination builder consumes this to decide
// whether to Set a credential header, strip credentials entirely,
// or forward the inbound Authorization verbatim.
type credStrategy int

const (
	credForwardInbound credStrategy = iota
	credSetFromProvider
	credStripNoSet
)

func credentialStrategy(authResult auth.AuthResult, state *rules.MutableState) credStrategy {
	if state.UpstreamCredentialOverride != nil {
		if *state.UpstreamCredentialOverride == "" {
			return credForwardInbound // UseSluiceKey sentinel
		}
		return credSetFromProvider
	}
	if authResult.Mode == auth.ModePassthrough {
		return credForwardInbound
	}
	// managed mode
	if authResult.Configuration == nil {
		return credStripNoSet
	}
	cred, ok := authResult.Configuration.UpstreamCredentials[state.Provider]
	if !ok || cred == "" {
		return credStripNoSet
	}
	return credSetFromProvider
}

func credentialFor(authResult auth.AuthResult, state *rules.MutableState) string {
	if state.UpstreamCredentialOverride != nil {
		return *state.UpstreamCredentialOverride
	}
	if authResult.Configuration == nil {
		return ""
	}
	return authResult.Configuration.UpstreamCredentials[state.Provider]
}

func appendUnique(slice []string, v string) []string {
	for _, s := range slice {
		if s == v {
			return slice
		}
	}
	return append(slice, v)
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
	return bodycapture.Model(captured.Body)
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
