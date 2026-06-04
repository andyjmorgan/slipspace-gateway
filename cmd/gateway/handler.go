package main

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/headers"
	"github.com/andyjmorgan/sluice-gateway/internal/httperr"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	resiliencemw "github.com/andyjmorgan/sluice-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
	"github.com/andyjmorgan/sluice-gateway/internal/selection"
)

// buildDataPlaneHandler composes the v2 request chain:
//
//	protocol → auth → bodycapture → selection → rules → resilience →
//	    body-remarshal → body-rewrite → final-forward
//
// protocol maps the inbound path to a wire shape; auth identifies the
// configuration from headers alone; selection walks that configuration's
// bindings to pick the provider/group and seeds the per-request state; the
// resilience orchestrator (reused unchanged) dispatches single-target or group
// attempts, each switching state.Provider; the final handler re-resolves the
// chosen provider's transport and forwards.
//
// The store is read per request inside selection + final via store.Snapshot,
// so an admin write that lands mid-request still produces a consistent answer.
func buildDataPlaneHandler(
	resolver *auth.Resolver,
	forwarder *proxy.Forwarder,
	evaluator *rules.Evaluator,
	observerFactory proxy.ObserverFactory,
	store *config.Store,
	breakers resiliencemw.BreakerStore,
	meters *observability.Meters,
	errs *httperr.Writer,
	redactor *headers.Redactor,
	_ *slog.Logger,
) http.Handler {
	h := buildFinalHandler(store, forwarder, errs)
	h = rules.BodyRewriteHandler(meters, h)
	h = rules.BodyRemarshalHandler(meters, h)
	h = resiliencemw.HTTPHandler(nil, breakers, meters, h)
	h = rules.HTTPHandler(evaluator, nil, observerFactory, h)
	h = selectionMiddleware(store, errs, h)
	h = bodycapture.HTTPHandler(kindFromProtocol, redactor, h)
	h = auth.HTTPHandler(resolver, h)
	h = protocolMiddleware(h)
	return h
}

// buildFinalHandler is the terminal data-plane stage. It resolves the upstream
// destination from the post-orchestrator state (generative) or the stashed
// passthrough match, sets the request labels for telemetry, and forwards.
func buildFinalHandler(store *config.Store, forwarder *proxy.Forwarder, errs *httperr.Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		pi, _ := protocolInfoFromContext(ctx)
		inboundAuthorization := r.Header.Get(auth.HeaderAuthorization)

		if pm, ok := passthroughMatchFromContext(ctx); ok {
			ctx = observability.WithRequestLabels(ctx, observability.RequestLabels{
				Provider:      pm.Provider,
				Endpoint:      pm.Family,
				Method:        r.Method,
				Configuration: authResult.ConfigurationName,
			})
			dest, err := buildPassthroughDestination(pm, r.URL.Path, r.URL.Query(), authResult.Mode, authResult.DropHeaders, inboundAuthorization)
			if err != nil {
				log.ErrorContext(ctx, "forwarder: passthrough destination", "err", err.Error())
				errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
				return
			}
			applyStateOverlays(&dest, state)
			if _, err := forwarder.Forward(ctx, w, r.WithContext(ctx), dest); err != nil {
				log.ErrorContext(ctx, "forwarder: forward", "err", err.Error())
				errs.Write(ctx, w, http.StatusInternalServerError, "handler", "forward_failed", "internal error")
			}
			return
		}

		provider := state.Provider
		if provider == "" {
			log.ErrorContext(ctx, "forwarder: no provider resolved")
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}
		snap := store.Snapshot()
		if authResult.Configuration == nil {
			log.ErrorContext(ctx, "forwarder: no configuration on context")
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}
		target, err := selection.ResolveTarget(pi.protocol, provider, "", *authResult.Configuration, snap.Providers)
		if err != nil {
			log.ErrorContext(ctx, "forwarder: resolve target", "provider", provider, "err", err.Error())
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}

		captured, _ := bodycapture.FromContext(ctx)
		ctx = observability.WithRequestLabels(ctx, observability.RequestLabels{
			Provider:      provider,
			Endpoint:      pi.protocol,
			Model:         outboundModel(captured, state),
			Method:        r.Method,
			Configuration: authResult.ConfigurationName,
		})

		dest, err := buildDestination(target, state.PathParams, authResult.Mode, authResult.DropHeaders, inboundAuthorization)
		if err != nil {
			log.ErrorContext(ctx, "forwarder: destination", "err", err.Error())
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "internal", "internal error")
			return
		}
		applyStateOverlays(&dest, state)

		if _, err := forwarder.Forward(ctx, w, r.WithContext(ctx), dest); err != nil {
			log.ErrorContext(ctx, "forwarder: forward", "err", err.Error())
			errs.Write(ctx, w, http.StatusInternalServerError, "handler", "forward_failed", "internal error")
		}
	})
}

// credentialHeaderNames is the closed set of headers the gateway may inject (or
// strip) for upstream authentication. Other headers authored by rules via
// SetHeader are not constrained here.
var credentialHeaderNames = []string{
	auth.HeaderAuthorization,
	"X-Api-Key",
	"X-Goog-Api-Key",
}

// authFormatPlaceholder is the literal substring the destination builder
// substitutes with the resolved credential when a provider protocol declares a
// custom auth format.
const authFormatPlaceholder = "{key}"

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

// outboundModel returns the model identifier the gateway is about to send
// upstream — read at destination-finalisation time so it reflects the
// per-target alias the orchestrator applied. PathParams["model"] wins (Gemini
// path-based addressing + alias rewrites); otherwise the typed body's Model.
func outboundModel(captured bodycapture.Captured, state *rules.MutableState) string {
	if state != nil {
		if v := strings.TrimSpace(state.PathParams["model"]); v != "" {
			return v
		}
	}
	return bodycapture.Model(captured.Body)
}

// openAIAPIType maps the v2 protocol endpoint label to the OpenAI api.type
// span/event attribute's spec value: the chat protocol reports
// "chat_completions" (the well-known surface name), everything else passes
// through unchanged.
func openAIAPIType(endpoint string) string {
	if endpoint == "chat" {
		return "chat_completions"
	}
	return endpoint
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
