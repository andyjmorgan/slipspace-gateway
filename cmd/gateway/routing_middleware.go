package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/routing"
)

type routeCtxKey struct{}

// routingMiddleware resolves the (method, path) against the router and stashes
// the Match on context for the downstream auth, bodycapture, and forwarder
// stages. Misses produce a typed JSON error and short-circuit the chain.
func routingMiddleware(router *routing.Router, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := observability.FromContext(ctx)

		match, err := router.Resolve(r.Method, r.URL.Path)
		switch {
		case errors.Is(err, routing.ErrNoRoute):
			logger.InfoContext(ctx, "routing miss", "method", r.Method, "path", r.URL.Path)
			writeJSON(w, http.StatusNotFound, errorBody("not found"))
			return
		case errors.Is(err, routing.ErrMethodNotAllowed):
			logger.InfoContext(ctx, "routing method not allowed", "method", r.Method, "path", r.URL.Path)
			writeJSON(w, http.StatusMethodNotAllowed, errorBody("method not allowed"))
			return
		case err != nil:
			logger.ErrorContext(ctx, "routing error", "err", err.Error())
			writeJSON(w, http.StatusInternalServerError, errorBody("internal error"))
			return
		}

		ctx = context.WithValue(ctx, routeCtxKey{}, match)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func matchFromContext(ctx context.Context) (routing.Match, bool) {
	if ctx == nil {
		return routing.Match{}, false
	}
	m, ok := ctx.Value(routeCtxKey{}).(routing.Match)
	return m, ok
}

func errorBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	data, err := json.Marshal(body)
	if err != nil {
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}
	_, _ = w.Write(data)
}
