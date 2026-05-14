package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/andyjmorgan/sluice-gateway/internal/httperr"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/routing"
)

type routeCtxKey struct{}

// routingMiddleware resolves the (method, path) against the router and stashes
// the Match on context for the downstream auth, bodycapture, and forwarder
// stages. Misses produce a typed JSON error via errs and short-circuit the chain.
func routingMiddleware(router *routing.Router, errs *httperr.Writer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := observability.FromContext(ctx)

		match, err := router.Resolve(r.Method, r.URL.Path)
		switch {
		case errors.Is(err, routing.ErrNoRoute):
			logger.InfoContext(ctx, "routing miss", "method", r.Method, "path", r.URL.Path)
			errs.Write(ctx, w, http.StatusNotFound, "routing", "no_route", "not found")
			return
		case errors.Is(err, routing.ErrMethodNotAllowed):
			logger.InfoContext(ctx, "routing method not allowed", "method", r.Method, "path", r.URL.Path)
			errs.Write(ctx, w, http.StatusMethodNotAllowed, "routing", "method_not_allowed", "method not allowed")
			return
		case err != nil:
			logger.ErrorContext(ctx, "routing error", "err", err.Error())
			errs.Write(ctx, w, http.StatusInternalServerError, "routing", "internal", "internal error")
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
