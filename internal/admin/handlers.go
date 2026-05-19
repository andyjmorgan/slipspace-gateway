package admin

import (
	"encoding/json"
	"net/http"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// AuthMeHandler returns 200 with a minimal JSON body when the request
// is authenticated. The handler runs behind BasicAuth, so reaching it
// implies the credentials matched. The SPA polls this on app load to
// validate cached credentials.
func AuthMeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{
			"username": adminc.Username,
		})
	})
}

// DashboardSummaryHandler returns a DashboardSummary derived from the
// snapshotter's bounded ring. Two windows are read: the requested
// dashboard window (?window=1h|24h, defaults to dashboardWindow) for
// totals/rates/quantiles/by-provider/by-model/rules-fired, and a
// separate 5-minute window for the provider-health card.
//
// When the snapshotter ring has fewer than two samples (process just
// started), the handler returns a valid but zero-valued summary —
// every field is shaped correctly so the SPA renders without errors.
func DashboardSummaryHandler(snap *observability.Snapshotter, providers []string, ruleAttachments map[string][]string, dashboardWindow, fiveMinWindow time.Duration, gatewayStartedAt time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		window := parseWindow(r.URL.Query().Get("window"), dashboardWindow)
		summary := computeSummary(snap, providers, ruleAttachments, window, fiveMinWindow)
		summary.GatewayStartedAt = gatewayStartedAt
		writeJSON(w, summary)
	})
}

// supportedWindows is the closed set of ?window= values the dashboard
// handlers accept. Anything outside this set silently falls back to
// the default. Capped at 24h because the snapshotter's ring covers
// roughly 24h at the production 5-minute cadence — wider windows would
// silently truncate to ring contents and lie about what they returned.
var supportedWindows = map[string]time.Duration{
	"1h":  time.Hour,
	"24h": 24 * time.Hour,
}

// parseWindow validates ?window= against the allowlist. Empty or
// unrecognised values return the default.
func parseWindow(raw string, fallback time.Duration) time.Duration {
	if d, ok := supportedWindows[raw]; ok {
		return d
	}
	return fallback
}

// computeSummary is the snapshotter-aware path that backs the
// dashboard handler. Split out from the handler so tests can drive it
// directly without spinning up an http.Server.
func computeSummary(snap *observability.Snapshotter, providers []string, ruleAttachments map[string][]string, dashboardWindow, fiveMinWindow time.Duration) adminc.DashboardSummary {
	if snap == nil {
		return emptySummary(providers, dashboardWindow)
	}
	start, end, realised, ok := snap.WindowEnds(dashboardWindow)
	if !ok {
		return emptySummary(providers, dashboardWindow)
	}
	var fiveMinStart, fiveMinEnd *observability.Sample
	if s, e, _, ok := snap.WindowEnds(fiveMinWindow); ok {
		fiveMinStart = &s
		fiveMinEnd = &e
	}
	return BuildDashboardSummary(start, end, realised, providers, ruleAttachments, fiveMinStart, fiveMinEnd)
}

// emptySummary returns the zero-valued shape so the SPA always decodes
// a valid response, even on first paint before any samples have landed.
func emptySummary(providers []string, window time.Duration) adminc.DashboardSummary {
	health := make([]adminc.DashboardProviderHealth, 0, len(providers))
	for _, p := range providers {
		health = append(health, adminc.DashboardProviderHealth{Provider: p, Healthy: true})
	}
	// Every slice field is initialised to a zero-length slice rather
	// than left nil — the SPA reads `.length` and `.map(...)` on these
	// without optional chaining, so a nil here lands as JSON null and
	// blows up the dashboard on first paint. New slice fields added to
	// DashboardSummary MUST be initialised here too.
	return adminc.DashboardSummary{
		Window:          formatWindow(window),
		GeneratedAt:     time.Now().UTC(),
		ByProvider:      []adminc.DashboardProviderRow{},
		ByEndpoint:      []adminc.DashboardEndpointRow{},
		ByConfiguration: []adminc.DashboardConfigurationRow{},
		ByModel:         []adminc.DashboardModelRow{},
		RulesFired:      []adminc.DashboardRuleFiredRow{},
		ProviderHealth:  health,
	}
}

// writeJSON serialises body as JSON with HTTP 200. Error paths use
// http.Error / http.NotFound directly — keeping this single-purpose
// avoids a dead-code parameter that the linter flags.
func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(body)
}
