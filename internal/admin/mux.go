package admin

import (
	"net/http"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// MuxOptions carries the inputs NewMux needs beyond the auth credential.
// Everything below feeds the live dashboard handler; pass zero values
// when admin features are disabled and the dashboard handler will return
// empty-shaped responses.
type MuxOptions struct {
	// Password is the operator credential for HTTP Basic auth.
	Password string

	// Meters is the observability instrument bundle. Required.
	Meters *observability.Meters

	// Snapshotter is the in-process metric snapshot store. Required —
	// the dashboard handler reads from it to compute windowed totals,
	// rates, and quantiles.
	Snapshotter *observability.Snapshotter

	// Providers is the list of configured provider names. Drives the
	// provider-health card so every configured provider gets a row,
	// even ones with zero traffic in the window.
	Providers []string

	// RuleAttachments maps rule_name → configurations that reference
	// it, joined into the "Rules fired" rows so the dashboard shows
	// where each rule is wired.
	RuleAttachments map[string][]string

	// FiveMinWindow is the duration the provider-health card reads
	// over. Defaults to 5m when zero.
	FiveMinWindow time.Duration

	// DashboardWindow is the default summary window when a request
	// omits the ?window= query. Defaults to 24h when zero.
	DashboardWindow time.Duration

	// GatewayStartedAt is the wall-clock instant the gateway process
	// began. Embedded in every dashboard-summary response so the SPA
	// can render uptime. Zero falls back to time.Now() at NewMux
	// construction — fine in tests, slightly off in real startup since
	// process init happens before this call, so cmd/gateway captures
	// the actual start before observability/bus/router are wired.
	GatewayStartedAt time.Time
}

// NewMux builds the management-console http.Handler.
//
// Routes:
//   - GET  /api/v1/auth/me           — auth probe; returns 200 with {"username":"admin"}
//   - GET  /api/v1/dashboard/summary — DashboardSummary JSON, real data from the snapshotter
//   - All other paths                — SPA static + index.html fallback
//
// HTTP Basic auth wraps the /api/v1/* tree. The SPA's static assets at
// root are unauthenticated — the SPA itself triggers the API calls
// behind auth, and a 401 from /api/v1/auth/me sends the user back to
// the login page.
//
// Each route is wrapped in InstrumentRoute so gateway.admin.requests.total
// carries a stable {route, status} label set rather than picking the
// URL up at random cardinality.
func NewMux(opts MuxOptions) http.Handler {
	if opts.FiveMinWindow == 0 {
		opts.FiveMinWindow = 5 * time.Minute
	}
	if opts.DashboardWindow == 0 {
		opts.DashboardWindow = 24 * time.Hour
	}
	if opts.GatewayStartedAt.IsZero() {
		opts.GatewayStartedAt = time.Now()
	}

	apiMux := http.NewServeMux()
	apiMux.Handle("/api/v1/auth/me",
		InstrumentRoute(opts.Meters, "/api/v1/auth/me", AuthMeHandler()),
	)
	apiMux.Handle("/api/v1/dashboard/summary",
		InstrumentRoute(opts.Meters, "/api/v1/dashboard/summary",
			DashboardSummaryHandler(opts.Snapshotter, opts.Providers, opts.RuleAttachments, opts.DashboardWindow, opts.FiveMinWindow, opts.GatewayStartedAt),
		),
	)
	apiMux.Handle("/api/v1/dashboard/timeseries",
		InstrumentRoute(opts.Meters, "/api/v1/dashboard/timeseries",
			TimeseriesHandler(opts.Snapshotter),
		),
	)

	root := http.NewServeMux()
	root.Handle("/api/v1/", BasicAuth(opts.Password, apiMux))
	root.Handle("/", InstrumentRoute(opts.Meters, "spa", SPAHandler()))

	return root
}
