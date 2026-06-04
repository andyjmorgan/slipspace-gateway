package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// The telemetry console reuses the gateway admin SPA's observability
// components unchanged, so its API must emit the SAME JSON the gateway admin
// API does — the contracts/admin shapes. These handlers map the DB-backed
// store query results into those shapes. Paths live in the telemetry TS client
// (the components take an injected client), so only the shapes are load-bearing.

// allowedWindows maps the SPA's window tokens to durations. Unknown tokens fall
// back to 24h.
var allowedWindows = map[string]time.Duration{
	"15m": 15 * time.Minute,
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// parseWindow resolves the ?window token to (token, duration), defaulting to 24h.
func parseWindow(r *http.Request) (string, time.Duration) {
	tok := r.URL.Query().Get("window")
	if d, ok := allowedWindows[tok]; ok {
		return tok, d
	}
	return "24h", 24 * time.Hour
}

// handleObsSummary emits contracts/admin.DashboardSummary mapped from the store.
func (s *Server) handleObsSummary(w http.ResponseWriter, r *http.Request) {
	tok, dur := parseWindow(r)
	now := time.Now()
	from := now.Add(-dur)
	summary, err := s.queries.QueryDashboardSummary(r.Context(), store.DashboardParams{
		From: from, To: now, RecentFrom: now.Add(-5 * time.Minute), Filter: filterFromQuery(r),
	})
	if err != nil {
		s.queryError(w, "dashboard summary", err)
		return
	}
	writeJSON(w, http.StatusOK, mapSummary(summary, tok, now, dur))
}

// handleObsTimeseries emits contracts/admin.DashboardTimeseries for the named
// ?series over ?window, mapped from the store's bucketed scan.
func (s *Server) handleObsTimeseries(w http.ResponseWriter, r *http.Request) {
	_, dur := parseWindow(r)
	now := time.Now()
	from := now.Add(-dur)
	bucket := bucketFor(dur)
	buckets, err := s.queries.QueryDashboardSeries(r.Context(), store.DashboardSeriesParams{
		From: from, To: now, BucketSeconds: bucket, Filter: filterFromQuery(r),
	})
	if err != nil {
		s.queryError(w, "dashboard timeseries", err)
		return
	}
	series := r.URL.Query().Get("series")
	writeJSON(w, http.StatusOK, mapTimeseries(buckets, series, float64(bucket)))
}

// handleObsMessagesRecent emits contracts/admin.MessagesRecentResponse mapped
// from the most recent events (oldest-first, matching the gateway live feed).
func (s *Server) handleObsMessagesRecent(w http.ResponseWriter, r *http.Request) {
	limit := intParam(r, "limit", 200)
	events, _, err := s.queries.ListEventsFiltered(r.Context(), store.EventListParams{
		Filter: filterFromQuery(r), Limit: limit,
	})
	if err != nil {
		s.queryError(w, "messages recent", err)
		return
	}
	writeJSON(w, http.StatusOK, mapMessages(events, limit))
}

// handleObsMessageBody emits contracts/admin.MessageBodyDetail mapped from the
// captured payloads for a correlation id.
func (s *Server) handleObsMessageBody(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	payloads, err := s.queries.ListPayloads(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrPayloadNotFound) {
			writeError(w, http.StatusNotFound, "no body")
			return
		}
		s.queryError(w, "message body", err)
		return
	}
	writeJSON(w, http.StatusOK, mapBody(id, payloads))
}

// --- mappers: store shapes -> contracts/admin shapes ---

func mapSummary(s store.DashboardSummary, window string, now time.Time, dur time.Duration) adminc.DashboardSummary {
	secs := dur.Seconds()
	out := adminc.DashboardSummary{
		Window:           window,
		GeneratedAt:      now.UTC(),
		GatewayStartedAt: now.UTC(),
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
			RequestsPerSecond: ratePerSecond(s.Totals.Requests, secs),
			ErrorRate:         ratio(s.Totals.RequestsErrored, s.Totals.Requests),
		},
		LatencyMs:       adminc.DashboardLatency{P50: s.Latency.P50, P95: s.Latency.P95, P99: s.Latency.P99},
		ByProvider:      make([]adminc.DashboardProviderRow, 0, len(s.ByProvider)),
		ByEndpoint:      make([]adminc.DashboardEndpointRow, 0, len(s.ByEndpoint)),
		ByConfiguration: make([]adminc.DashboardConfigurationRow, 0, len(s.ByConfiguration)),
		ByModel:         make([]adminc.DashboardModelRow, 0, len(s.ByModel)),
		RulesFired:      make([]adminc.DashboardRuleFiredRow, 0, len(s.RulesFired)),
		TagsFired:       make([]adminc.DashboardTagFiredRow, 0, len(s.TagsFired)),
		ProviderHealth:  make([]adminc.DashboardProviderHealth, 0, len(s.ProviderHealth)),
	}
	for _, r := range s.ByProvider {
		out.ByProvider = append(out.ByProvider, adminc.DashboardProviderRow{Provider: r.Key, Requests: r.Requests, P95LatencyMs: r.P95LatencyMs, ErrorRate: r.ErrorRate})
	}
	for _, r := range s.ByEndpoint {
		out.ByEndpoint = append(out.ByEndpoint, adminc.DashboardEndpointRow{Provider: r.Provider, Endpoint: r.Endpoint, Requests: r.Requests, P95LatencyMs: r.P95LatencyMs, ErrorRate: r.ErrorRate})
	}
	for _, r := range s.ByConfiguration {
		out.ByConfiguration = append(out.ByConfiguration, adminc.DashboardConfigurationRow{Configuration: r.Key, Requests: r.Requests, P95LatencyMs: r.P95LatencyMs, ErrorRate: r.ErrorRate})
	}
	for _, r := range s.ByModel {
		out.ByModel = append(out.ByModel, adminc.DashboardModelRow{Model: r.Model, Provider: r.Provider, Requests: r.Requests, TokensIn: r.TokensIn, TokensOut: r.TokensOut})
	}
	for _, r := range s.RulesFired {
		out.RulesFired = append(out.RulesFired, adminc.DashboardRuleFiredRow{RuleName: r.Key, FireCount: r.Count, UsedByConfigurations: r.UsedByConfigurations})
	}
	for _, r := range s.TagsFired {
		out.TagsFired = append(out.TagsFired, adminc.DashboardTagFiredRow{Tag: r.Key, ApplyCount: r.Count, UsedByConfigurations: r.UsedByConfigurations})
	}
	for _, h := range s.ProviderHealth {
		out.ProviderHealth = append(out.ProviderHealth, adminc.DashboardProviderHealth{
			Provider: h.Provider, Healthy: h.ErrorRate == 0, ErrorRate5m: h.ErrorRate, Requests5m: h.Requests,
		})
	}
	return out
}

// seriesValue extracts the named curve's value from a bucket. perSecond scales
// the request count by the bucket width for the rps curve.
func seriesValue(b store.DashboardSeriesBucket, name string, bucketSecs float64) float64 {
	switch name {
	case "rps":
		if bucketSecs <= 0 {
			return 0
		}
		return float64(b.Requests) / bucketSecs
	case "error_rate":
		return ratio(b.Errored, b.Requests)
	case "p50":
		return float64(b.P50LatencyMs)
	case "p95":
		return float64(b.P95LatencyMs)
	case "p99":
		return float64(b.P99LatencyMs)
	case "tokens_in":
		return float64(b.TokensIn)
	case "tokens_out":
		return float64(b.TokensOut)
	default: // "requests"
		return float64(b.Requests)
	}
}

func seriesUnit(name string) string {
	switch name {
	case "rps":
		return "req/s"
	case "error_rate":
		return "%"
	case "p50", "p95", "p99":
		return "ms"
	default:
		return ""
	}
}

func mapTimeseries(buckets []store.DashboardSeriesBucket, name string, bucketSecs float64) adminc.DashboardTimeseries {
	if name == "" {
		name = "requests"
	}
	points := make([]adminc.DashboardPoint, 0, len(buckets))
	for _, b := range buckets {
		points = append(points, adminc.DashboardPoint{Timestamp: b.Ts.UTC(), Value: seriesValue(b, name, bucketSecs)})
	}
	return adminc.DashboardTimeseries{Series: []adminc.DashboardSeries{{Name: name, Unit: seriesUnit(name), Points: points}}}
}

func mapMessages(events []store.RequestEvent, capacity int) adminc.MessagesRecentResponse {
	// ListEventsFiltered returns newest-first; the contract wants oldest-first.
	entries := make([]adminc.MessageEntry, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		entries = append(entries, mapEntry(events[i]))
	}
	return adminc.MessagesRecentResponse{Capacity: capacity, Entries: entries}
}

func mapEntry(e store.RequestEvent) adminc.MessageEntry {
	entry := adminc.MessageEntry{
		EventID:             e.CorrelationID,
		At:                  e.ObservedAt.UTC(),
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
	if len(e.Detail) > 0 {
		var d store.EventDetail
		if json.Unmarshal(e.Detail, &d) == nil {
			entry.Tags = d.Tags
			for _, r := range d.RulesFired {
				entry.RulesMatched = append(entry.RulesMatched, adminc.RuleHit{RuleName: r})
			}
		}
	}
	return entry
}

func mapBody(correlationID string, payloads []store.Payload) adminc.MessageBodyDetail {
	detail := adminc.MessageBodyDetail{EventID: correlationID}
	for _, p := range payloads {
		switch p.Kind {
		case store.KindRequestBody:
			detail.Request = string(p.Body)
			detail.RequestTotalBytes = int64(len(p.Body))
		case store.KindResponseBody:
			detail.Response = string(p.Body)
			detail.ResponseTotalBytes = int64(len(p.Body))
		case store.KindSSERollup:
			detail.ResponseAssembled = string(p.Body)
		}
	}
	return detail
}

// bucketFor picks a bucket width (seconds) giving a readable number of points
// for the window — roughly 60 buckets.
func bucketFor(dur time.Duration) int {
	secs := int(dur.Seconds()) / 60
	if secs < 1 {
		secs = 1
	}
	return secs
}

func ratePerSecond(count int64, windowSecs float64) float64 {
	if windowSecs <= 0 {
		return 0
	}
	return float64(count) / windowSecs
}

func ratio(num, den int64) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}
