package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	connc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// richReader is the read slice of the request_events store the rich
// (gateway-shaped) dashboard + message-inspector surfaces need on top of the
// v1 eventReader: the fleet-wide DB rollups.
type richReader interface {
	QueryDashboardSummary(ctx context.Context, p configdb.DashboardParams) (configdb.DashboardSummary, error)
	QueryDashboardSeries(ctx context.Context, p configdb.DashboardSeriesParams) ([]configdb.DashboardSeriesBucket, error)
	QueryDashboardSeriesByProvider(ctx context.Context, p configdb.DashboardSeriesParams) ([]configdb.DashboardProviderSeriesBucket, error)
	ListEventsFiltered(ctx context.Context, p configdb.EventListParams) ([]configdb.RequestEvent, string, error)
}

// recordReader is the read slice of the request_bodies store the
// message-inspector body drill-in needs: the latest captured connector Record
// for one correlation id.
type recordReader interface {
	GetRequestRecord(ctx context.Context, correlationID string) (json.RawMessage, error)
}

// providerHealthErrorCeiling is the 5-minute error-rate threshold above which a
// provider's health chip flips from healthy to unhealthy — same 5% cut the
// gateway's dashboard uses.
const providerHealthErrorCeiling = 0.05

// recentHealthWindow is the trailing window the provider-health snapshot is
// computed over, regardless of the dashboard's outer window — the
// gateway's provider-health card is always a 5-minute view.
const recentHealthWindow = 5 * time.Minute

// timeseriesBuckets is the fixed number of buckets the timeseries endpoint
// splits the requested window into; the bucket width is window/buckets. A fixed
// point count keeps the chart's x-resolution stable across window sizes.
const timeseriesBuckets = 60

// registerRichRoutes adds the rich, gateway-shaped dashboard + message-inspector
// endpoints to the observability mux. They are additive surfaces over the same
// Postgres store the v1 /stats + /events endpoints read.
func (h *ObservabilityHandler) registerRichRoutes() {
	if h.rich == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/observability/dashboard/summary", h.dashboardSummary)
	h.mux.HandleFunc("GET /api/v1/observability/dashboard/timeseries", h.dashboardTimeseries)
	h.mux.HandleFunc("GET /api/v1/observability/messages/recent", h.messagesRecent)
	// The body route serves two independent sources: the spool-captured
	// connector Record (h.records) and the telemetry-native gen_ai_content on
	// the request_event (h.store). Register it whenever either can supply
	// content, so a telemetry-only deployment (SLUICE_OTEL_CAPTURE_CONTENT, no
	// connector spool) still surfaces the inspector content.
	if h.records != nil || h.store != nil {
		h.mux.HandleFunc("GET /api/v1/observability/messages/{event_id}/body", h.messageBody)
	}
}

// supportedWindows maps the ?window= label onto its duration. Unlike the
// gateway (which is capped by its in-memory snapshot ring), the DB-backed fleet
// view can serve arbitrarily long windows, so the allowlist is wider.
var supportedWindows = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"12h": 12 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

// parseWindow resolves the ?window= param to (label, duration). An empty value
// defaults to 24h; an unrecognized value is reported as ok=false so the handler
// can 400 rather than silently serve a different window than the label claims.
func parseWindow(raw string) (label string, d time.Duration, ok bool) {
	if raw == "" {
		return "24h", 24 * time.Hour, true
	}
	if dur, found := supportedWindows[raw]; found {
		return raw, dur, true
	}
	return "", 0, false
}

func (h *ObservabilityHandler) dashboardSummary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	label, window, ok := parseWindow(q.Get("window"))
	if !ok {
		writeError(w, http.StatusBadRequest, "window must be one of 1h/6h/12h/24h/7d/30d")
		return
	}
	to := time.Now().UTC()
	from := to.Add(-window)
	recentFrom := to.Add(-recentHealthWindow)
	if recentFrom.Before(from) {
		recentFrom = from
	}

	s, err := h.rich.QueryDashboardSummary(r.Context(), configdb.DashboardParams{
		From:       from,
		To:         to,
		RecentFrom: recentFrom,
		Filter:     parseFilter(q),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query dashboard summary")
		return
	}
	writeJSON(w, http.StatusOK, toDashboardSummary(label, from, to, window, s))
}

// toDashboardSummary maps the configdb rollup onto the gateway's
// contracts/admin.DashboardSummary so the lifted SPA decodes it unchanged.
//
// GatewayStartedAt has no fleet analog (there is no single gateway process), so
// it is set to the window start — the most honest value for the SPA's
// "up since" tick, which in the fleet view reads as "window opened at".
// RequestsPerSecond is computed over the whole window from the headline count.
func toDashboardSummary(label string, from, to time.Time, window time.Duration, s configdb.DashboardSummary) adminc.DashboardSummary {
	out := adminc.DashboardSummary{
		Window:           label,
		GeneratedAt:      to,
		GatewayStartedAt: from,
		Totals: adminc.DashboardTotals{
			Requests:            s.Totals.Requests,
			RequestsSuccess:     s.Totals.RequestsSuccess,
			RequestsErrored:     s.Totals.RequestsErrored,
			TokensIn:            s.Totals.TokensIn,
			TokensOut:           s.Totals.TokensOut,
			TokensCached:        s.Totals.TokensCached,
			TokensCacheCreation: s.Totals.TokensCacheCreation,
		},
		Rates: adminc.DashboardRates{
			RequestsPerSecond: perSecond(s.Totals.Requests, window),
			ErrorRate:         ratio(s.Totals.RequestsErrored, s.Totals.Requests),
		},
		LatencyMs: adminc.DashboardLatency{
			P50: s.Latency.P50,
			P95: s.Latency.P95,
			P99: s.Latency.P99,
		},
		ByProvider:      make([]adminc.DashboardProviderRow, 0, len(s.ByProvider)),
		ByEndpoint:      make([]adminc.DashboardEndpointRow, 0, len(s.ByEndpoint)),
		ByConfiguration: make([]adminc.DashboardConfigurationRow, 0, len(s.ByConfiguration)),
		ByModel:         make([]adminc.DashboardModelRow, 0, len(s.ByModel)),
		RulesFired:      make([]adminc.DashboardRuleFiredRow, 0, len(s.RulesFired)),
		TagsFired:       make([]adminc.DashboardTagFiredRow, 0, len(s.TagsFired)),
		ProviderHealth:  make([]adminc.DashboardProviderHealth, 0, len(s.ProviderHealth)),
	}
	for _, row := range s.ByProvider {
		out.ByProvider = append(out.ByProvider, adminc.DashboardProviderRow{
			Provider:     row.Key,
			Requests:     row.Requests,
			P95LatencyMs: row.P95LatencyMs,
			ErrorRate:    row.ErrorRate,
		})
	}
	for _, row := range s.ByEndpoint {
		out.ByEndpoint = append(out.ByEndpoint, adminc.DashboardEndpointRow{
			Provider:     row.Provider,
			Endpoint:     row.Endpoint,
			Requests:     row.Requests,
			P95LatencyMs: row.P95LatencyMs,
			ErrorRate:    row.ErrorRate,
		})
	}
	for _, row := range s.ByConfiguration {
		out.ByConfiguration = append(out.ByConfiguration, adminc.DashboardConfigurationRow{
			Configuration: row.Key,
			Requests:      row.Requests,
			P95LatencyMs:  row.P95LatencyMs,
			ErrorRate:     row.ErrorRate,
		})
	}
	for _, row := range s.ByModel {
		out.ByModel = append(out.ByModel, adminc.DashboardModelRow{
			Model:     row.Model,
			Provider:  row.Provider,
			Requests:  row.Requests,
			TokensIn:  row.TokensIn,
			TokensOut: row.TokensOut,
		})
	}
	for _, row := range s.RulesFired {
		out.RulesFired = append(out.RulesFired, adminc.DashboardRuleFiredRow{
			RuleName:             row.Key,
			FireCount:            row.Count,
			UsedByConfigurations: row.UsedByConfigurations,
		})
	}
	for _, row := range s.TagsFired {
		out.TagsFired = append(out.TagsFired, adminc.DashboardTagFiredRow{
			Tag:                  row.Key,
			ApplyCount:           row.Count,
			UsedByConfigurations: row.UsedByConfigurations,
		})
	}
	for _, row := range s.ProviderHealth {
		out.ProviderHealth = append(out.ProviderHealth, adminc.DashboardProviderHealth{
			Provider:    row.Provider,
			Healthy:     row.ErrorRate <= providerHealthErrorCeiling,
			ErrorRate5m: row.ErrorRate,
			Requests5m:  row.Requests,
		})
	}
	return out
}

func (h *ObservabilityHandler) dashboardTimeseries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	label, window, ok := parseWindow(q.Get("window"))
	if !ok {
		writeError(w, http.StatusBadRequest, "window must be one of 1h/6h/12h/24h/7d/30d")
		return
	}
	_ = label
	series := strings.TrimSpace(q.Get("series"))
	if series == "" {
		series = q.Get("metric")
	}
	if !validSeries[series] {
		writeError(w, http.StatusBadRequest, "series must be one of rps/error_rate/latency/tokens_per_second/rps_by_provider_top5/error_rate_by_provider_top5")
		return
	}

	to := time.Now().UTC()
	from := to.Add(-window)
	bucketSeconds := int(window.Seconds()) / timeseriesBuckets
	if bucketSeconds < 1 {
		bucketSeconds = 1
	}
	params := configdb.DashboardSeriesParams{
		From:          from,
		To:            to,
		BucketSeconds: bucketSeconds,
		Filter:        parseFilter(q),
	}

	if perProviderSeries[series] {
		buckets, err := h.rich.QueryDashboardSeriesByProvider(r.Context(), params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "query dashboard timeseries")
			return
		}
		writeJSON(w, http.StatusOK, toProviderTimeseries(series, bucketSeconds, buckets))
		return
	}

	buckets, err := h.rich.QueryDashboardSeries(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query dashboard timeseries")
		return
	}
	writeJSON(w, http.StatusOK, toTimeseries(series, bucketSeconds, buckets))
}

// Series names the DB-backed endpoint serves. They match the gateway admin
// dashboard's contract so the shared SPA component's ?series= queries resolve
// against either backend identically.
const (
	seriesRPS                     = "rps"
	seriesErrorRate               = "error_rate"
	seriesLatency                 = "latency"
	seriesTokensPerSecond         = "tokens_per_second" //nolint:gosec // series name (gateway.tokens.*), not a credential
	seriesRPSByProviderTop5       = "rps_by_provider_top5"
	seriesErrorRateByProviderTop5 = "error_rate_by_provider_top5"
)

// topNProviders caps the per-provider timeseries panels at the five busiest
// providers over the window — matching the gateway admin handler's cap so both
// backends render the same legend.
const topNProviders = 5

// validSeries is the allowlist of timeseries names the DB-backed endpoint
// serves. latency carries p50/p95/p99 as three curves; tokens_per_second
// carries input/output as two curves; the *_by_provider_top5 series carry one
// curve per top-N provider.
var validSeries = map[string]bool{
	seriesRPS:                     true,
	seriesErrorRate:               true,
	seriesLatency:                 true,
	seriesTokensPerSecond:         true,
	seriesRPSByProviderTop5:       true,
	seriesErrorRateByProviderTop5: true,
}

// perProviderSeries marks the series that resolve to one curve per top-N
// provider and so read from the by-provider scan rather than the all-provider
// bucket scan.
var perProviderSeries = map[string]bool{
	seriesRPSByProviderTop5:       true,
	seriesErrorRateByProviderTop5: true,
}

// toTimeseries derives the requested curve set from the per-bucket scan. Each
// point's value is the per-second rate (rps, tokens) or the bucket's quantile
// (latency) / percentage (error_rate) — matching the gateway's units so the SPA
// axis labels stay correct.
func toTimeseries(series string, bucketSeconds int, buckets []configdb.DashboardSeriesBucket) adminc.DashboardTimeseries {
	secs := float64(bucketSeconds)
	switch series {
	case "rps":
		pts := make([]adminc.DashboardPoint, 0, len(buckets))
		for _, b := range buckets {
			pts = append(pts, adminc.DashboardPoint{Timestamp: b.Ts, Value: float64(b.Requests) / secs})
		}
		return single("Requests per second", "req/s", pts)
	case "error_rate":
		pts := make([]adminc.DashboardPoint, 0, len(buckets))
		for _, b := range buckets {
			var pct float64
			if b.Requests > 0 {
				pct = float64(b.Errored) / float64(b.Requests) * 100
			}
			pts = append(pts, adminc.DashboardPoint{Timestamp: b.Ts, Value: pct})
		}
		return single("Error rate", "%", pts)
	case "tokens_per_second":
		in := make([]adminc.DashboardPoint, 0, len(buckets))
		out := make([]adminc.DashboardPoint, 0, len(buckets))
		for _, b := range buckets {
			in = append(in, adminc.DashboardPoint{Timestamp: b.Ts, Value: float64(b.TokensIn) / secs})
			out = append(out, adminc.DashboardPoint{Timestamp: b.Ts, Value: float64(b.TokensOut) / secs})
		}
		return adminc.DashboardTimeseries{Series: []adminc.DashboardSeries{
			{Name: "Tokens in", Unit: "tok/s", Labels: map[string]string{"kind": "input"}, Points: in},
			{Name: "Tokens out", Unit: "tok/s", Labels: map[string]string{"kind": "output"}, Points: out},
		}}
	default: // latency
		p50 := make([]adminc.DashboardPoint, 0, len(buckets))
		p95 := make([]adminc.DashboardPoint, 0, len(buckets))
		p99 := make([]adminc.DashboardPoint, 0, len(buckets))
		for _, b := range buckets {
			p50 = append(p50, adminc.DashboardPoint{Timestamp: b.Ts, Value: float64(b.P50LatencyMs)})
			p95 = append(p95, adminc.DashboardPoint{Timestamp: b.Ts, Value: float64(b.P95LatencyMs)})
			p99 = append(p99, adminc.DashboardPoint{Timestamp: b.Ts, Value: float64(b.P99LatencyMs)})
		}
		return adminc.DashboardTimeseries{Series: []adminc.DashboardSeries{
			{Name: "p50", Unit: "ms", Labels: map[string]string{"quantile": "p50"}, Points: p50},
			{Name: "p95", Unit: "ms", Labels: map[string]string{"quantile": "p95"}, Points: p95},
			{Name: "p99", Unit: "ms", Labels: map[string]string{"quantile": "p99"}, Points: p99},
		}}
	}
}

func single(name, unit string, pts []adminc.DashboardPoint) adminc.DashboardTimeseries {
	return adminc.DashboardTimeseries{Series: []adminc.DashboardSeries{{Name: name, Unit: unit, Points: pts}}}
}

// toProviderTimeseries builds one curve per top-N provider from the
// (bucket, provider) scan. Providers are ranked by total request volume over
// the window (matching the gateway admin handler), then each chosen provider
// gets a zero-filled point at every bucket so the lines share an x-axis. The
// per-curve Name/Labels/Unit mirror the gateway exactly so the shared SPA
// component colours the legend the same way against either backend.
func toProviderTimeseries(series string, bucketSeconds int, buckets []configdb.DashboardProviderSeriesBucket) adminc.DashboardTimeseries {
	secs := float64(bucketSeconds)
	cells, bucketTimes, totals := indexProviderBuckets(buckets)
	providers := topProvidersByTotal(totals, topNProviders)

	out := adminc.DashboardTimeseries{Series: make([]adminc.DashboardSeries, 0, len(providers))}
	for _, provider := range providers {
		unit := "req/s"
		if series == seriesErrorRateByProviderTop5 {
			unit = "%"
		}
		s := adminc.DashboardSeries{
			Name:   provider,
			Unit:   unit,
			Labels: map[string]string{"provider": provider},
			Points: make([]adminc.DashboardPoint, 0, len(bucketTimes)),
		}
		for _, ts := range bucketTimes {
			cell := cells[provider][ts]
			var value float64
			if series == seriesErrorRateByProviderTop5 {
				if cell.Requests > 0 {
					value = float64(cell.Errored) / float64(cell.Requests) * 100
				}
			} else {
				value = float64(cell.Requests) / secs
			}
			s.Points = append(s.Points, adminc.DashboardPoint{Timestamp: ts, Value: value})
		}
		out.Series = append(out.Series, s)
	}
	return out
}

// providerCell is one provider's request/error counts inside one bucket.
type providerCell struct {
	Requests int64
	Errored  int64
}

// indexProviderBuckets pivots the flat (bucket, provider) scan into a
// provider→bucket→cell lookup, the sorted distinct bucket timestamps, and each
// provider's window-total request count for top-N ranking. Bucket timestamps
// come only from buckets that saw traffic; an empty window yields no curves,
// matching the gateway's "awaiting data" empty-series behaviour.
func indexProviderBuckets(buckets []configdb.DashboardProviderSeriesBucket) (cells map[string]map[time.Time]providerCell, bucketTimes []time.Time, totals map[string]int64) {
	cells = map[string]map[time.Time]providerCell{}
	totals = map[string]int64{}
	seenBucket := map[time.Time]struct{}{}
	for _, b := range buckets {
		if cells[b.Provider] == nil {
			cells[b.Provider] = map[time.Time]providerCell{}
		}
		cells[b.Provider][b.Ts] = providerCell{Requests: b.Requests, Errored: b.Errored}
		totals[b.Provider] += b.Requests
		if _, ok := seenBucket[b.Ts]; !ok {
			seenBucket[b.Ts] = struct{}{}
			bucketTimes = append(bucketTimes, b.Ts)
		}
	}
	sort.Slice(bucketTimes, func(i, j int) bool { return bucketTimes[i].Before(bucketTimes[j]) })
	return cells, bucketTimes, totals
}

// topProvidersByTotal returns at most n provider names ranked by descending
// window-total request count, ties broken by name for determinism. Providers
// with no requests are dropped. Mirrors the gateway admin handler's ranking.
func topProvidersByTotal(totals map[string]int64, n int) []string {
	type entry struct {
		name  string
		total int64
	}
	entries := make([]entry, 0, len(totals))
	for name, total := range totals {
		if total <= 0 {
			continue
		}
		entries = append(entries, entry{name: name, total: total})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].total != entries[j].total {
			return entries[i].total > entries[j].total
		}
		return entries[i].name < entries[j].name
	})
	if len(entries) > n {
		entries = entries[:n]
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.name)
	}
	return out
}

// messagesRecentLimitDefault / Max bound the ?limit= of /messages/recent. The
// default matches the gateway ring's typical capacity; the max caps the DB scan.
const (
	messagesRecentLimitDefault = 200
	messagesRecentLimitMax     = 1000
)

func (h *ObservabilityHandler) messagesRecent(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := messagesRecentLimitDefault
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = n
	}
	if limit > messagesRecentLimitMax {
		limit = messagesRecentLimitMax
	}

	events, _, err := h.rich.ListEventsFiltered(r.Context(), configdb.EventListParams{
		Filter: parseFilter(q),
		Limit:  limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list messages")
		return
	}
	// ListEventsFiltered returns newest-first; the SPA appends oldest-first, so
	// reverse into arrival order to match the gateway ring's contract.
	out := adminc.MessagesRecentResponse{
		Capacity: limit,
		Entries:  make([]adminc.MessageEntry, 0, len(events)),
	}
	for i := len(events) - 1; i >= 0; i-- {
		out.Entries = append(out.Entries, toMessageEntry(events[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// toMessageEntry projects an enriched request_events row onto the gateway's
// MessageEntry wire shape. EventID is the correlation id — the fleet store has
// no separate ring UUID, and correlation id is the stable join key the body
// drill-in endpoint reads.
//
// Attempts is always empty: phase-1 ingestion does not yet decode the connector
// Record's per-attempt resilience breakdown into request_events.detail (the
// detail envelope carries only tags + rules_fired). It is wired through here so
// the SPA's expansion table appears the moment a later phase populates it.
func toMessageEntry(e configdb.RequestEvent) adminc.MessageEntry {
	out := adminc.MessageEntry{
		EventID:             e.CorrelationID,
		At:                  e.ObservedAt,
		CorrelationID:       e.CorrelationID,
		SessionID:           e.SessionID,
		SessionIDSource:     e.SessionIDSource,
		Provider:            e.Backend,
		Endpoint:            e.Protocol,
		Model:               e.Model,
		Method:              e.Method,
		Configuration:       e.Configuration,
		StatusCode:          e.StatusCode,
		DurationMs:          e.LatencyMs,
		Streaming:           e.Streaming,
		TokensIn:            int(e.TokensIn),
		TokensOut:           int(e.TokensOut),
		TokensCached:        int(e.TokensCached),
		TokensCacheCreation: int(e.TokensCacheCreation),
		PolicyRef:           e.PolicyRef,
	}
	var detail configdb.EventDetail
	if len(e.Detail) > 0 {
		_ = json.Unmarshal(e.Detail, &detail)
	}
	if len(detail.Tags) > 0 {
		out.Tags = append(out.Tags, detail.Tags...)
	}
	if len(detail.RulesFired) > 0 {
		out.RulesMatched = make([]adminc.RuleHit, 0, len(detail.RulesFired))
		for _, name := range detail.RulesFired {
			out.RulesMatched = append(out.RulesMatched, adminc.RuleHit{RuleName: name})
		}
	}
	return out
}

// messageBody serves the inspector's per-request drill-in. It draws from two
// independent sources keyed on the same correlation id: the spool-captured
// connector Record (the request/response body envelope) and the
// telemetry-native gen_ai_content on the request_event (bounded prompt/response
// + tool content the gateway put on the span). Either may be absent; the
// response is the merge of whatever is present. 404 only when neither source
// has anything for the id.
func (h *ObservabilityHandler) messageBody(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")

	var detail adminc.MessageBodyDetail
	detail.EventID = eventID
	haveBody := false

	if h.records != nil {
		raw, err := h.records.GetRequestRecord(r.Context(), eventID)
		switch {
		case errors.Is(err, configdb.ErrRequestBodyNotFound):
			// No spool record — fall through to telemetry-only content.
		case err != nil:
			writeError(w, http.StatusInternalServerError, "get message body")
			return
		default:
			d, perr := recordToBodyDetail(eventID, raw)
			if perr != nil {
				writeError(w, http.StatusInternalServerError, "decode captured record")
				return
			}
			detail = d
			haveBody = true
		}
	}

	genAIContent := h.genAIContent(r.Context(), eventID)
	if len(genAIContent) > 0 {
		detail.GenAIContent = genAIContent
	}

	if !haveBody && len(detail.GenAIContent) == 0 {
		writeError(w, http.StatusNotFound, "no captured body for event id")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// genAIContent returns the bounded gen_ai content the gateway captured on the
// request span for one correlation id, or nil when the event is absent, carries
// no content, or the store is unavailable. A missing event is not an error here
// — the connector Record may still carry bodies — so lookup failures degrade to
// nil rather than failing the request.
func (h *ObservabilityHandler) genAIContent(ctx context.Context, eventID string) json.RawMessage {
	if h.store == nil {
		return nil
	}
	e, err := h.store.GetRequestEvent(ctx, eventID)
	if err != nil {
		return nil
	}
	if len(e.GenAIContent) == 0 {
		return nil
	}
	return json.RawMessage(e.GenAIContent)
}

// recordToBodyDetail maps a captured connector Record onto the gateway's
// MessageBodyDetail. The Record's inline Request/Response bodies are raw JSON;
// they surface verbatim as the request/response strings. Headers carry over as
// single-value-per-key maps widened to the wire's multi-value shape. Truncation
// is driven off the Record's explicit BodyOmitted / BodyTruncated signals — not
// a len(Body) < BodyBytes comparison, which falsely fires for a complete body
// whose inline JSON is compacted relative to the raw wire bytes BodyBytes
// counts. For a streamed response the Record also carries the accumulator's
// reassembly (ResponsePart.Assembled), which surfaces as ResponseAssembled so
// the inspector's rollup view renders the non-streaming JSON while Response
// keeps the raw SSE bytes.
func recordToBodyDetail(eventID string, raw json.RawMessage) (adminc.MessageBodyDetail, error) {
	var rec connc.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return adminc.MessageBodyDetail{}, err
	}
	out := adminc.MessageBodyDetail{
		EventID:            eventID,
		Request:            string(rec.Request.Body),
		RequestTotalBytes:  int64(rec.Request.BodyBytes),
		RequestTruncated:   rec.Request.BodyOmitted || rec.Request.BodyTruncated,
		Response:           string(rec.Response.Body),
		ResponseTotalBytes: int64(rec.Response.BodyBytes),
		ResponseTruncated:  rec.Response.BodyOmitted || rec.Response.BodyTruncated,
		ResponseAssembled:  rec.Response.Assembled,
		AssemblyPartial:    rec.Response.AssemblyPartial,
		RequestHeaders:     widenHeaders(rec.Request.Headers),
		ResponseHeaders:    widenHeaders(rec.Response.Headers),
	}
	if out.RequestTotalBytes == 0 && len(rec.Request.Body) > 0 {
		out.RequestTotalBytes = int64(len(rec.Request.Body))
	}
	if out.ResponseTotalBytes == 0 && len(rec.Response.Body) > 0 {
		out.ResponseTotalBytes = int64(len(rec.Response.Body))
	}
	return out, nil
}

// widenHeaders converts the connector Record's flat single-value header
// snapshot into the wire's {name: [value]} shape. Returns nil for an empty map
// so the field omits.
func widenHeaders(h map[string]string) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, v := range h {
		out[k] = []string{v}
	}
	return out
}

func perSecond(count int64, window time.Duration) float64 {
	if window <= 0 {
		return 0
	}
	return float64(count) / window.Seconds()
}

func ratio(num, denom int64) float64 {
	if denom == 0 {
		return 0
	}
	return float64(num) / float64(denom)
}
