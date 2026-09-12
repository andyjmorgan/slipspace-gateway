// Package admin implements the management console's runtime: HTTP Basic
// auth, the console API handlers, and the embedded SPA. The whole tree is
// mounted under the /admin prefix (const Prefix) and served with
// http.StripPrefix, so inner handlers register bare /api/v1/... paths
// while clients see /admin/api/v1/... and the SPA at /admin/. The package
// is consumed by cmd/gateway at startup, which constructs the admin mux
// behind a feature flag (admin.enabled) and runs it on a second
// http.Server bound to the configured admin.bind_addr.
//
// The data-plane listener never imports this package — admin requests
// are physically segregated by listener, not just by URL prefix.
package admin
