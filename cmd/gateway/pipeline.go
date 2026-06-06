package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	contractsres "github.com/andyjmorgan/sluice-gateway/contracts/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/httperr"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/auth"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	resiliencemw "github.com/andyjmorgan/sluice-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
	"github.com/andyjmorgan/sluice-gateway/internal/selection"
)

// protocolInfo is the result of mapping an inbound path to its v2 protocol.
// Generative is true for the typed wire shapes (chat/responses/messages/
// generate_content/embeddings); false marks a candidate for opaque passthrough
// matching, resolved per-configuration by the selection middleware.
type protocolInfo struct {
	protocol   string
	params     map[string]string
	generative bool
}

type protocolInfoKey struct{}

type passthroughMatchKey struct{}

func withProtocolInfo(ctx context.Context, pi protocolInfo) context.Context {
	return context.WithValue(ctx, protocolInfoKey{}, pi)
}

func protocolInfoFromContext(ctx context.Context) (protocolInfo, bool) {
	pi, ok := ctx.Value(protocolInfoKey{}).(protocolInfo)
	return pi, ok
}

func withPassthroughMatch(ctx context.Context, pm selection.PassthroughMatch) context.Context {
	return context.WithValue(ctx, passthroughMatchKey{}, pm)
}

func passthroughMatchFromContext(ctx context.Context) (selection.PassthroughMatch, bool) {
	pm, ok := ctx.Value(passthroughMatchKey{}).(selection.PassthroughMatch)
	return pm, ok
}

// protocolMiddleware maps the inbound path to its v2 protocol and stashes the
// result on the request context. It is the first data-plane stage: the path is
// the protocol under v2, so this replaces v1's route table. A recognised
// generative path carries its protocol (and Gemini's path-derived model/op);
// an unrecognised path is marked non-generative and falls through to
// per-configuration passthrough matching downstream.
func protocolMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto, params, ok := selection.ProtocolForPath(r.URL.Path)
		pi := protocolInfo{protocol: proto, params: params, generative: ok}
		next.ServeHTTP(w, r.WithContext(withProtocolInfo(r.Context(), pi)))
	})
}

// kindFromProtocol resolves the bodycapture RequestKind from the stashed
// protocol. The typed body-keyed protocols map one-to-one onto the capture
// kinds; everything else (embeddings, non-generative passthrough families) is
// buffered raw under KindPassthrough — embeddings has no typed request model in
// the providers package, so it routes only via catch-all bindings.
func kindFromProtocol(ctx context.Context) (bodycapture.RequestKind, bool) {
	pi, ok := protocolInfoFromContext(ctx)
	if !ok {
		return "", false
	}
	if !pi.generative {
		return bodycapture.KindPassthrough, true
	}
	switch pi.protocol {
	case selection.ProtocolChat:
		return bodycapture.KindChat, true
	case selection.ProtocolResponses:
		return bodycapture.KindResponses, true
	case selection.ProtocolMessages:
		return bodycapture.KindMessages, true
	case selection.ProtocolGenerateContent:
		return bodycapture.KindGenerateContent, true
	default:
		return bodycapture.KindPassthrough, true
	}
}

// selectionMiddleware resolves the request to a provider (or resilience group)
// from the resolved configuration's bindings and seeds the per-request state
// the downstream stages consume. It runs after auth (which resolves the
// configuration) and bodycapture (which decodes the model), and before rules +
// resilience.
//
// Generative path: selection.Select picks the binding; the chosen
// single-target or group is synthesised into a resilience config stashed on
// context (the orchestrator reuses it), and a MutableState is seeded so rules
// can condition on provider/protocol/model. No binding match → 404 (no
// fallthrough). Non-generative path: selection.MatchPassthrough picks the
// opaque family → 404/405 on miss.
func selectionMiddleware(store *config.Store, errs *httperr.Writer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := observability.FromContext(ctx)

		pi, ok := protocolInfoFromContext(ctx)
		if !ok {
			log.ErrorContext(ctx, "selection: no protocol on context")
			errs.Write(ctx, w, http.StatusInternalServerError, "selection", "internal", "internal error")
			return
		}
		ar, ok := auth.FromContext(ctx)
		if !ok || ar.Configuration == nil {
			log.ErrorContext(ctx, "selection: no configuration on context")
			errs.Write(ctx, w, http.StatusInternalServerError, "selection", "internal", "internal error")
			return
		}
		snap := store.Snapshot()
		cfg := *ar.Configuration

		if !pi.generative {
			pm, err := selection.MatchPassthrough(r.Method, r.URL.Path, cfg, snap.Providers)
			switch {
			case errors.Is(err, selection.ErrNoPassthrough):
				errs.Write(ctx, w, http.StatusNotFound, "selection", "no_route", "no route for path")
				return
			case errors.Is(err, selection.ErrMethodNotAllowed):
				errs.Write(ctx, w, http.StatusMethodNotAllowed, "selection", "method_not_allowed", "method not allowed")
				return
			case err != nil:
				log.ErrorContext(ctx, "selection: passthrough resolve", "err", err.Error())
				errs.Write(ctx, w, http.StatusInternalServerError, "selection", "internal", "internal error")
				return
			}
			ctx = withPassthroughMatch(ctx, pm)
			// Rules condition on the passthrough family name as the
			// "endpoint" (e.g. messages_batches), since a passthrough request
			// has no generative protocol.
			state := rules.NewMutableState(pm.Provider, pm.Family, r.URL.Path, pm.Params, r.Header)
			for _, t := range pm.Tags {
				state.AddTag(t)
			}
			ctx = rules.WithMutableState(ctx, state)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		model := pi.params["model"]
		if model == "" {
			if captured, ok := bodycapture.FromContext(ctx); ok {
				model = bodycapture.Model(captured.Body)
			}
		}

		dest, err := selection.Select(pi.protocol, model, cfg, snap.Providers, snap.Groups)
		switch {
		case errors.Is(err, selection.ErrNoBinding):
			errs.Write(ctx, w, http.StatusNotFound, "selection", "no_binding", "model not served on this protocol")
			return
		case err != nil:
			log.ErrorContext(ctx, "selection: select", "err", err.Error())
			errs.Write(ctx, w, http.StatusInternalServerError, "selection", "internal", "internal error")
			return
		}

		var (
			rc       contractsres.ResilienceConfig
			provider string
		)
		if dest.Group != nil {
			rc = groupToResilienceConfig(dest.Group.Name, *dest.Group)
		} else {
			rc = singleTargetConfig(*dest.Single)
			provider = dest.Single.Provider
		}

		state := rules.NewMutableState(provider, pi.protocol, "", pi.params, r.Header)
		for _, t := range dest.Tags {
			state.AddTag(t)
		}
		state.PolicyRef = rc.Name

		ctx = rules.WithMutableState(ctx, state)
		ctx = resiliencemw.WithResilienceConfig(ctx, &rc)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// buildPassthroughDestination resolves the opaque-proxy destination: the
// inbound path is forwarded verbatim under the provider base URL, the inbound
// query is preserved with the provider's default query overlaid, and the family
// auth convention is applied at the single credential mint site.
func buildPassthroughDestination(
	pm selection.PassthroughMatch,
	inboundPath string,
	inboundQuery url.Values,
	mode auth.Mode,
	dropHeaders []string,
	inboundAuthorization string,
	override *string,
) (proxy.Destination, error) {
	base, err := url.Parse(pm.BaseURL)
	if err != nil {
		return proxy.Destination{}, err
	}
	upstream := *base
	upstream.Path = joinPaths(base.Path, inboundPath)
	upstream.RawPath = ""

	q := inboundQuery
	if len(pm.Query) > 0 {
		if q == nil {
			q = url.Values{}
		}
		for k, v := range pm.Query {
			q.Set(k, v)
		}
	}
	if q != nil {
		upstream.RawQuery = q.Encode()
	}

	target := selection.Target{
		Provider:        pm.Provider,
		BaseURL:         pm.BaseURL,
		Auth:            pm.Auth,
		RequiredHeaders: pm.RequiredHeaders,
		Credential:      pm.Credential,
	}

	outgoing := http.Header{}
	for k, v := range pm.RequiredHeaders {
		outgoing.Set(k, v)
	}
	drops := append([]string(nil), dropHeaders...)
	drops = resolveCredentialHeaders(outgoing, drops, target, mode, override, inboundAuthorization)

	return proxy.Destination{
		BaseURL:         base,
		UpstreamURL:     &upstream,
		OutgoingHeaders: outgoing,
		DropHeaders:     drops,
	}, nil
}

// applyStateOverlays layers the post-rule MutableState's authored mutations —
// SetHeader writes and appendQueryString deltas — onto the resolved
// destination. Rules win the last word on the wire, mirroring v1.
func applyStateOverlays(dest *proxy.Destination, state *rules.MutableState) {
	if len(state.QueryAdditions) > 0 && dest.UpstreamURL != nil {
		q := dest.UpstreamURL.Query()
		for _, add := range state.QueryAdditions {
			q.Add(add.Key, add.Value)
		}
		dest.UpstreamURL.RawQuery = q.Encode()
	}
	for k, vs := range state.OutgoingHeaders {
		dest.OutgoingHeaders.Del(k)
		for _, v := range vs {
			dest.OutgoingHeaders.Add(k, v)
		}
	}
}
