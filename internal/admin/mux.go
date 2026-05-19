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

// Prefix is the URL path prefix the console mounts under. Both the
// SPA and the control-plane API live below this prefix so the gateway
// can sit behind a shared ingress on the same host as the data plane
// (e.g. sluice.donkeywork.dev/admin/) without any path stripping
// on the ingress side — keeping the routing rules dumb.
const Prefix = "/admin"

// NewMux builds the management-console http.Handler.
//
// Routes (all under Prefix):
//   - GET  /admin/                       — SPA index (auto-redirect from /admin)
//   - GET  /admin/api/v1/auth/me         — auth probe; 200 + {"username":"admin"}
//   - GET  /admin/api/v1/dashboard/...   — DashboardSummary / Timeseries JSON
//   - All other /admin/* paths           — SPA static + index.html fallback
//
// HTTP Basic auth wraps the /admin/api/v1/* tree. The SPA's static
// assets are unauthenticated — the SPA itself drives the API calls
// behind auth, and a 401 from /admin/api/v1/auth/me sends the user
// back to the login page.
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

	// adminTree exposes the same routes the listener used to expose at
	// root; StripPrefix below converts incoming /admin/foo requests
	// into /foo before they reach this mux, so the inner handlers do
	// not need to know about the prefix.
	adminTree := http.NewServeMux()
	adminTree.Handle("/api/v1/", BasicAuth(opts.Password, apiMux))
	adminTree.Handle("/", InstrumentRoute(opts.Meters, "spa", SPAHandler()))

	root := http.NewServeMux()
	// "/admin/" matches /admin/ and anything below; ServeMux also
	// auto-redirects a bare /admin to /admin/ so the user never lands
	// on an empty SPA root.
	root.Handle(Prefix+"/", http.StripPrefix(Prefix, adminTree))

	return root
}
