// Package auth resolves the inbound auth mode (managed or passthrough) for
// each request, looks up the matching Configuration, and exposes the
// resolved AuthResult on the request context for downstream middleware.
//
// Per-endpoint authorization is implicit, not enforced here: managed mode
// can only forward to providers that have an entry in
// Configuration.UpstreamCredentials, and passthrough mode is gated by the
// upstream's own auth on the client-supplied BYOK token.
package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// HTTPHandler wraps next with the auth resolution step.
//
// Under the v2 model auth identifies the request from its headers alone —
// there is no route to resolve first. It invokes resolver.Resolve against the
// request headers and, on success, stashes the resulting AuthResult (carrying
// the resolved v2 configuration) on the request context, retrieved downstream
// via FromContext. The selection middleware then walks that configuration's
// bindings to pick the provider. On failure a typed JSON error response is
// written and next is not invoked.
func HTTPHandler(resolver *Resolver, next http.Handler) http.Handler {
	if resolver == nil {
		panic("auth: HTTPHandler called with nil resolver")
	}
	if next == nil {
		panic("auth: HTTPHandler called with nil next handler")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		logger := observability.FromContext(ctx)

		ar, err := resolver.Resolve(req.Header)
		if ar.LegacyConfigurationHeader {
			logger.WarnContext(ctx, "deprecated header in use",
				"header", HeaderConfiguration,
				"replacement", HeaderIdentity,
				"configuration", ar.ConfigurationName,
			)
		}
		if err != nil {
			result := classifyResult(err, ar)
			logger.WarnContext(ctx, "auth failed",
				"mode", string(ar.Mode),
				"result", string(result),
				"configuration", ar.ConfigurationName,
			)
			writeAuthError(w, err)
			return
		}

		logger.InfoContext(ctx, "auth resolved",
			"mode", string(ar.Mode),
			"api_key_id", apiKeyID(ar),
			"configuration", ar.ConfigurationName,
			"result", string(ResultSuccess),
		)

		next.ServeHTTP(w, req.WithContext(withAuth(ctx, ar)))
	})
}

func apiKeyID(ar AuthResult) string {
	if ar.APIKey == nil {
		return ""
	}
	return ar.APIKey.Name
}

// classifyResult maps a typed auth error onto the audit Result string the
// design note requires. The disabled-key vs unknown-key distinction is
// preserved in logs even though the wire response collapses both to 401.
func classifyResult(err error, ar AuthResult) Result {
	switch {
	case errors.Is(err, ErrUnknownConfiguration):
		return ResultUnknownConfiguration
	case errors.Is(err, ErrUnauthorized):
		if ar.APIKey != nil && !ar.APIKey.Enabled {
			return ResultDisabledKey
		}
		return ResultUnknownKey
	default:
		return ResultUnknownKey
	}
}

func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, ErrUnknownConfiguration):
		writeError(w, http.StatusForbidden, "unknown configuration")
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, err := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: message})
	if err != nil {
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}
	_, _ = w.Write(body)
}
