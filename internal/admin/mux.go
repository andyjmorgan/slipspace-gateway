package admin

import (
	"net/http"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/observability/livefeed"
)

// CircuitBreakerStateSource is re-exported from the policies handler
// so callers can pass any in-process BreakerStore (or a stub for tests)
// through MuxOptions without importing the resilience middleware here.
// See policies.go for the interface contract.

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

	// TagAttachments maps tag string → configurations whose rule
	// chain attaches that tag via AddTagAction. Joined into the
	// "Tags fired" rows so operators see which configurations
	// produce each tag, mirroring the Rules-fired panel.
	TagAttachments map[string][]string

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

	// Store owns the live ResolvedConfig the gateway is currently
	// serving. The read-only config endpoints under /api/v1/config/*
	// marshal redacted projections of store.Snapshot() straight out of
	// the structure on every request, so a Replace driven by the admin
	// write path (Phase 2) is visible to subsequent reads atomically.
	// Nil disables those endpoints — they return 503 rather than panic,
	// so a partial wiring still boots.
	Store *config.Store

	// LiveFeed is the in-process ring of completed requests that backs
	// the /api/v1/messages/* endpoints. Nil disables the endpoints
	// (they return 503), letting the gateway boot cleanly when
	// SLUICE_ADMIN_LIVE_FEED_CAPACITY=0.
	LiveFeed *livefeed.Ring

	// BodyStore is the byte-bounded LRU of per-event captured bodies
	// backing GET /api/v1/messages/{event_id}/body. Nil disables the
	// endpoint (returns 503) — the live-tail pane still works on
	// metadata alone.
	BodyStore *livefeed.BodyStore

	// BreakerStates is the read interface used by the policies
	// endpoint to project per-target circuit-breaker state. Nil is
	// safe — every target will report state="unknown" and the SPA
	// renders that as an inert badge.
	BreakerStates CircuitBreakerStateSource

	// ConfigDir is the SLUICE_CONFIG_DIR path the gateway loaded its
	// YAML from. Used by the redacted-config-export endpoints to
	// enumerate and read the same files via config.ListConfigFiles.
	// Empty disables the export endpoints (503).
	ConfigDir string

	// Hostname is os.Hostname() captured at startup. Embedded in the
	// MANIFEST.txt entry of every export bundle so an operator can
	// attribute a snapshot back to its pod. Empty renders as an empty
	// "Hostname:" line in the manifest — informational, not load-bearing.
	Hostname string
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
//   - GET  /admin/                            — SPA index (auto-redirect from /admin)
//   - GET  /admin/api/v1/version              — unauthenticated; binary version
//   - GET  /admin/api/v1/auth/me              — auth probe; 200 + {"username":"admin"}
//   - GET  /admin/api/v1/dashboard/...        — DashboardSummary / Timeseries JSON
//   - GET  /admin/api/v1/config/api-keys/reveal?configuration=&name=
//   - GET  /admin/api/v1/config/configurations[/{name}]
//   - GET  /admin/api/v1/config/rules[/{name}]
//   - GET  /admin/api/v1/config/providers[/{name}]
//   - GET  /admin/api/v1/config/routes        — flattened route index
//   - All other /admin/* paths                — SPA static + index.html fallback
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
			DashboardSummaryHandler(opts.Snapshotter, opts.Providers, opts.RuleAttachments, opts.TagAttachments, opts.DashboardWindow, opts.FiveMinWindow, opts.GatewayStartedAt),
		),
	)
	apiMux.Handle("/api/v1/dashboard/timeseries",
		InstrumentRoute(opts.Meters, "/api/v1/dashboard/timeseries",
			TimeseriesHandler(opts.Snapshotter),
		),
	)
	// Read-only config inspection. The handlers snapshot the store on
	// every request and project the snapshot onto redacted DTOs — every
	// secret (api-key Secret, upstream credentials) is replaced by a
	// last-4/length stub before it leaves the package.
	configList := ConfigurationsListHandler(opts.Store)
	configDetail := ConfigurationDetailHandler(opts.Store)
	rulesList := RulesListHandler(opts.Store)
	ruleDetail := RuleDetailHandler(opts.Store)
	backendsList := BackendsListHandler(opts.Store)
	backendDetail := BackendDetailHandler(opts.Store)
	bindingsAll := BindingsHandler(opts.Store)
	apiKeysReveal := APIKeysRevealHandler(opts.Store)
	apiMux.Handle("/api/v1/config/api-keys/reveal",
		InstrumentRoute(opts.Meters, "/api/v1/config/api-keys/reveal", apiKeysReveal),
	)
	// Configurations surface — read (GET) plus the write API (POST/PUT/DELETE).
	// Credentials are masked on read and write-back-if-delivered on write;
	// api_keys are never accepted here (managed only via /api-keys).
	configCreate := ConfigurationsCreateHandler(opts.Store, opts.ConfigDir)
	configReplace := ConfigurationsReplaceHandler(opts.Store, opts.ConfigDir)
	configDelete := ConfigurationsDeleteHandler(opts.Store, opts.ConfigDir)
	apiMux.Handle("GET /api/v1/config/configurations",
		InstrumentRoute(opts.Meters, "/api/v1/config/configurations", configList),
	)
	apiMux.Handle("POST /api/v1/config/configurations",
		InstrumentRoute(opts.Meters, "/api/v1/config/configurations", configCreate),
	)
	apiMux.Handle("GET /api/v1/config/configurations/{name}",
		InstrumentRoute(opts.Meters, "/api/v1/config/configurations/{name}", configDetail),
	)
	apiMux.Handle("PUT /api/v1/config/configurations/{name}",
		InstrumentRoute(opts.Meters, "/api/v1/config/configurations/{name}", configReplace),
	)
	apiMux.Handle("DELETE /api/v1/config/configurations/{name}",
		InstrumentRoute(opts.Meters, "/api/v1/config/configurations/{name}", configDelete),
	)
	// Rules surface — read (GET) plus the Phase 2 write API
	// (POST/PUT/DELETE). Method-routed patterns let GET and POST share
	// the same path under Go 1.22 ServeMux; the write handlers 503
	// gracefully when ConfigDir is empty (admin write disabled by
	// deployment).
	rulesCreate := RulesCreateHandler(opts.Store, opts.ConfigDir)
	rulesReplace := RulesReplaceHandler(opts.Store, opts.ConfigDir)
	rulesDelete := RulesDeleteHandler(opts.Store, opts.ConfigDir)
	apiMux.Handle("GET /api/v1/config/rules",
		InstrumentRoute(opts.Meters, "/api/v1/config/rules", rulesList),
	)
	apiMux.Handle("POST /api/v1/config/rules",
		InstrumentRoute(opts.Meters, "/api/v1/config/rules", rulesCreate),
	)
	apiMux.Handle("GET /api/v1/config/rules/{name}",
		InstrumentRoute(opts.Meters, "/api/v1/config/rules/{name}", ruleDetail),
	)
	apiMux.Handle("PUT /api/v1/config/rules/{name}",
		InstrumentRoute(opts.Meters, "/api/v1/config/rules/{name}", rulesReplace),
	)
	apiMux.Handle("DELETE /api/v1/config/rules/{name}",
		InstrumentRoute(opts.Meters, "/api/v1/config/rules/{name}", rulesDelete),
	)
	// Backends surface — read (GET) plus the write API (POST/PUT/DELETE),
	// method-routed under the same paths; write handlers 503 when ConfigDir
	// is empty (admin write disabled by deployment).
	backendsCreate := BackendsCreateHandler(opts.Store, opts.ConfigDir)
	backendsReplace := BackendsReplaceHandler(opts.Store, opts.ConfigDir)
	backendsDelete := BackendsDeleteHandler(opts.Store, opts.ConfigDir)
	apiMux.Handle("GET /api/v1/config/backends",
		InstrumentRoute(opts.Meters, "/api/v1/config/backends", backendsList),
	)
	apiMux.Handle("POST /api/v1/config/backends",
		InstrumentRoute(opts.Meters, "/api/v1/config/backends", backendsCreate),
	)
	apiMux.Handle("GET /api/v1/config/backends/{name}",
		InstrumentRoute(opts.Meters, "/api/v1/config/backends/{name}", backendDetail),
	)
	apiMux.Handle("PUT /api/v1/config/backends/{name}",
		InstrumentRoute(opts.Meters, "/api/v1/config/backends/{name}", backendsReplace),
	)
	apiMux.Handle("DELETE /api/v1/config/backends/{name}",
		InstrumentRoute(opts.Meters, "/api/v1/config/backends/{name}", backendsDelete),
	)
	// Groups surface — editable CRUD under /config/groups (the richer
	// live-circuit-breaker projection stays on /api/v1/policies).
	groupsList := GroupsListHandler(opts.Store)
	groupDetail := GroupDetailHandler(opts.Store)
	groupsCreate := GroupsCreateHandler(opts.Store, opts.ConfigDir)
	groupsReplace := GroupsReplaceHandler(opts.Store, opts.ConfigDir)
	groupsDelete := GroupsDeleteHandler(opts.Store, opts.ConfigDir)
	apiMux.Handle("GET /api/v1/config/groups",
		InstrumentRoute(opts.Meters, "/api/v1/config/groups", groupsList),
	)
	apiMux.Handle("POST /api/v1/config/groups",
		InstrumentRoute(opts.Meters, "/api/v1/config/groups", groupsCreate),
	)
	apiMux.Handle("GET /api/v1/config/groups/{name}",
		InstrumentRoute(opts.Meters, "/api/v1/config/groups/{name}", groupDetail),
	)
	apiMux.Handle("PUT /api/v1/config/groups/{name}",
		InstrumentRoute(opts.Meters, "/api/v1/config/groups/{name}", groupsReplace),
	)
	apiMux.Handle("DELETE /api/v1/config/groups/{name}",
		InstrumentRoute(opts.Meters, "/api/v1/config/groups/{name}", groupsDelete),
	)
	apiMux.Handle("/api/v1/config/bindings",
		InstrumentRoute(opts.Meters, "/api/v1/config/bindings", bindingsAll),
	)
	// Settings page — redacted config export. Both endpoints share the
	// same redactor; the files endpoint backs the tabbed inspector view,
	// the download endpoint streams the ZIP bundle.
	apiMux.Handle("/api/v1/config/export/files",
		InstrumentRoute(opts.Meters, "/api/v1/config/export/files",
			ConfigExportFilesHandler(opts.ConfigDir),
		),
	)
	apiMux.Handle("/api/v1/config/export/download",
		InstrumentRoute(opts.Meters, "/api/v1/config/export/download",
			ConfigExportDownloadHandler(opts.ConfigDir, opts.Hostname, opts.Meters),
		),
	)
	// Live-messages pane endpoints. Both handlers degrade to 503 when
	// LiveFeed is nil — the SPA reads that as "feature disabled" and
	// hides the pane.
	apiMux.Handle("/api/v1/messages/recent",
		InstrumentRoute(opts.Meters, "/api/v1/messages/recent", MessagesRecentHandler(opts.LiveFeed)),
	)
	apiMux.Handle("/api/v1/messages/stream",
		InstrumentRoute(opts.Meters, "/api/v1/messages/stream", MessagesStreamHandler(opts.LiveFeed)),
	)
	// Per-event body endpoint uses the 1.22+ servemux placeholder
	// syntax so r.PathValue picks the event_id without a trim-prefix
	// dance. The longest-match rule keeps the exact /recent and
	// /stream routes above winning over this pattern.
	apiMux.Handle("/api/v1/messages/{event_id}/body",
		InstrumentRoute(opts.Meters, "/api/v1/messages/{event_id}/body", MessageBodyHandler(opts.BodyStore)),
	)
	// Read-only resilience policies surface. Powers the SPA's
	// /policies page and the per-target circuit-state badges.
	apiMux.Handle("/api/v1/policies",
		InstrumentRoute(opts.Meters, "/api/v1/policies", PoliciesHandler(opts.Store, opts.BreakerStates)),
	)

	// adminTree exposes the same routes the listener used to expose at
	// root; StripPrefix below converts incoming /admin/foo requests
	// into /foo before they reach this mux, so the inner handlers do
	// not need to know about the prefix.
	// /api/v1/version sits OUTSIDE the BasicAuth tree so the SPA's
	// login screen can render the gateway's version pre-credential.
	// Everything else under /api/v1/ stays behind BasicAuth.
	publicAPI := http.NewServeMux()
	publicAPI.Handle("/api/v1/version",
		InstrumentRoute(opts.Meters, "/api/v1/version", VersionHandler()),
	)
	publicAPI.Handle("/api/v1/", BasicAuth(opts.Password, apiMux))

	adminTree := http.NewServeMux()
	adminTree.Handle("/api/v1/", publicAPI)
	adminTree.Handle("/", InstrumentRoute(opts.Meters, "spa", SPAHandler()))

	root := http.NewServeMux()
	// "/admin/" matches /admin/ and anything below; ServeMux also
	// auto-redirects a bare /admin to /admin/ so the user never lands
	// on an empty SPA root.
	root.Handle(Prefix+"/", http.StripPrefix(Prefix, adminTree))

	return root
}
