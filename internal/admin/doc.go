// Package admin implements the management console's runtime: HTTP Basic
// auth, control-plane API handlers under /api/v1, and the embedded SPA
// served at the listener root. The package is consumed by cmd/gateway
// at startup, which constructs the admin mux behind a feature flag
// (gateway.admin.enabled) and runs it on a second http.Server bound to
// the configured admin.bind_addr.
//
// The data-plane listener never imports this package — admin requests
// are physically segregated by listener, not just by URL prefix.
package admin
