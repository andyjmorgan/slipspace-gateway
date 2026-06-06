package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/stitch"
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

// clockSkewMargin pads the window's upper bound. observed_at is assigned by
// Postgres (now()) while the window's `to` is the service's clock; when the DB
// runs slightly ahead, a just-arrived event would otherwise fall after `to` and
// vanish from "recent" views. A small forward margin keeps such events visible.
const clockSkewMargin = time.Minute

// windowBounds returns the [from, to) the dashboard queries over, applying the
// clock-skew margin to the upper bound.
func windowBounds(now time.Time, dur time.Duration) (from, to time.Time) {
	return now.Add(-dur), now.Add(clockSkewMargin)
}

// handleObsSummary emits contracts/admin.DashboardSummary mapped from the store.
func (s *Server) handleObsSummary(w http.ResponseWriter, r *http.Request) {
	tok, dur := parseWindow(r)
	now := time.Now()
	from, to := windowBounds(now, dur)
	summary, err := s.queries.QueryDashboardSummary(r.Context(), store.DashboardParams{
		From: from, To: to, RecentFrom: now.Add(-5 * time.Minute), Filter: filterFromQuery(r),
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
	from, to := windowBounds(now, dur)
	bucket := bucketFor(dur)
	buckets, err := s.queries.QueryDashboardSeries(r.Context(), store.DashboardSeriesParams{
		From: from, To: to, BucketSeconds: bucket, Filter: filterFromQuery(r),
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
	// gen_ai content + the assembly-partial flag live on the request_events
	// row, not a payload — fetch the event best-effort so the inspector's
	// GenAI tab and the "partial" badge can render alongside the bodies. A
	// missing event just leaves both empty.
	var (
		genAI   []byte
		partial bool
	)
	if ev, evErr := s.queries.GetRequestEvent(r.Context(), id); evErr == nil {
		genAI = ev.GenAIContent
		if len(ev.Detail) > 0 {
			var d store.EventDetail
			if json.Unmarshal(ev.Detail, &d) == nil {
				partial = d.AssemblyPartial
			}
		}
	}
	writeJSON(w, http.StatusOK, mapBody(id, payloads, genAI, partial))
}

// handleObsMessages serves the message browser: a filtered, keyset-paged page of
// events in the rich contracts/admin.MessageEntry shape (so the inspector
// decodes them unchanged), newest-first. Unlike /messages/recent (the dashboard
// live feed) it honors the full filter set, an optional time window, and a
// cursor, and returns next_cursor for forward paging.
func (s *Server) handleObsMessages(w http.ResponseWriter, r *http.Request) {
	from, to, bad := parseWindowBounds(r)
	if bad != "" {
		writeError(w, http.StatusBadRequest, "invalid "+bad)
		return
	}
	q := r.URL.Query()
	events, next, err := s.queries.ListEventsFiltered(r.Context(), store.EventListParams{
		From:   from,
		To:     to,
		Filter: filterFromQuery(r),
		Cursor: q.Get("cursor"),
		Limit:  intParam(r, "limit", 0),
	})
	if err != nil {
		if errors.Is(err, store.ErrInvalidCursor) {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		s.queryError(w, "messages", err)
		return
	}
	// Keep store order (newest-first) — this is a browser, not the live feed.
	entries := make([]adminc.MessageEntry, 0, len(events))
	for _, e := range events {
		entries = append(entries, mapEntry(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "next_cursor": next})
}

// handleFacets emits the distinct dropdown values for the message browser. The
// values come from cachedFacets, so repeated dropdown opens don't rescan the
// event table within the TTL.
func (s *Server) handleFacets(w http.ResponseWriter, r *http.Request) {
	f, err := s.cachedFacets(r.Context())
	if err != nil {
		s.queryError(w, "facets", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"providers":      nonNil(f.Providers),
		"models":         nonNil(f.Models),
		"configurations": nonNil(f.Configurations),
		"endpoints":      nonNil(f.Endpoints),
		"tags":           nonNil(f.Tags),
	})
}

// nonNil renders a nil slice as [] rather than null so the SPA can map over it
// without a guard.
func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// facetsTTL bounds how stale the dropdown values may be. Short enough that a new
// provider/model/tag shows up promptly, long enough to absorb a burst of
// dropdown opens with a single scan.
const facetsTTL = 30 * time.Second

// facetsCache memoizes the distinct dropdown values behind a mutex. The lock is
// held across the refresh query so a burst of concurrent opens collapses to one
// scan rather than a thundering herd.
type facetsCache struct {
	mu      sync.Mutex
	value   store.Facets
	expires time.Time
}

// cachedFacets returns the facets, refreshing from the store when the cache has
// expired.
func (s *Server) cachedFacets(ctx context.Context) (store.Facets, error) {
	s.facets.mu.Lock()
	defer s.facets.mu.Unlock()
	if time.Now().Before(s.facets.expires) {
		return s.facets.value, nil
	}
	f, err := s.queries.Facets(ctx)
	if err != nil {
		return store.Facets{}, err
	}
	s.facets.value = f
	s.facets.expires = time.Now().Add(facetsTTL)
	return f, nil
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

// mapSession projects a session's events onto the tagged SessionView wire
// shape: each request mapped through mapEntry (so tags/rules/attempts are
// parsed, not raw base64 JSONB), totals from stitch.BuildSessionView. Events
// stay oldest-first — the graphs plot cumulative/timeline series straight off
// the slice; the table reverses client-side.
func mapSession(sessionID string, events []store.RequestEvent) adminc.SessionView {
	base := stitch.BuildSessionView(sessionID, events)
	out := adminc.SessionView{
		SessionID: sessionID,
		Totals: adminc.SessionTotals{
			Requests:  base.Totals.Requests,
			Errors:    base.Totals.Errors,
			TokensIn:  base.Totals.TokensIn,
			TokensOut: base.Totals.TokensOut,
		},
		Requests: make([]adminc.MessageEntry, 0, len(events)),
	}
	for _, e := range events {
		out.Requests = append(out.Requests, mapEntry(e))
	}
	return out
}

func mapEntry(e store.RequestEvent) adminc.MessageEntry {
	entry := adminc.MessageEntry{
		EventID:             e.CorrelationID,
		At:                  e.ObservedAt.UTC(),
		CorrelationID:       e.CorrelationID,
		SessionID:           e.SessionID,
		SessionIDSource:     e.SessionIDSource,
		Provider:            e.Provider,
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
			// Prefer the full rule chain (actions / terminated / error) the
			// Record feed carries; fall back to the flat name list when only
			// names are present (e.g. an older record).
			if len(d.RuleChain) > 0 {
				for _, r := range d.RuleChain {
					entry.RulesMatched = append(entry.RulesMatched, adminc.RuleHit{
						RuleName:       r.Name,
						ActionsApplied: r.ActionsApplied,
						Terminated:     r.Terminated,
						ErrorMessage:   r.ErrorMessage,
					})
				}
			} else {
				for _, r := range d.RulesFired {
					entry.RulesMatched = append(entry.RulesMatched, adminc.RuleHit{RuleName: r})
				}
			}
			for _, a := range d.Attempts {
				entry.Attempts = append(entry.Attempts, adminc.AttemptHit{
					Target:     a.Target,
					StartedAt:  time.Unix(0, a.StartedAtNs).UTC(),
					DurationMs: a.DurationMs,
					StatusCode: a.StatusCode,
					Error:      a.Error,
					Outcome:    a.Outcome,
				})
			}
		}
	}
	return entry
}

func mapBody(correlationID string, payloads []store.Payload, genAIContent []byte, assemblyPartial bool) adminc.MessageBodyDetail {
	detail := adminc.MessageBodyDetail{EventID: correlationID}
	for _, p := range payloads {
		switch p.Kind {
		case store.KindRequestBody:
			detail.Request = decodeInlineBody(p.Body)
			detail.RequestTotalBytes = int64(len(detail.Request))
		case store.KindResponseBody:
			detail.Response = decodeInlineBody(p.Body)
			detail.ResponseTotalBytes = int64(len(detail.Response))
		case store.KindSSERollup:
			detail.ResponseAssembled = string(p.Body)
		case store.KindRequestHeaders:
			detail.RequestHeaders = decodeHeaders(p.Body)
		case store.KindResponseHeaders:
			detail.ResponseHeaders = decodeHeaders(p.Body)
		}
	}
	if len(genAIContent) > 0 {
		detail.GenAIContent = string(genAIContent)
	}
	// Only meaningful alongside an assembled rollup; harmless otherwise.
	detail.AssemblyPartial = assemblyPartial
	return detail
}

// decodeInlineBody renders a Record inline-body payload (the connector
// contract's RequestPart/ResponsePart Body) back to display text. The gateway's
// jsonBodyOrEscaped (cmd/gateway/reporter.go) stores JSON bodies verbatim but
// wraps non-JSON bodies — SSE streams, plain text — as a JSON string token so
// they're valid inside the json.RawMessage field. Reverse that here: a JSON
// string token decodes back to its raw text, so a streamed response shows real
// `event:` / `data:` lines on the Raw stream tab instead of a quoted, escaped
// blob. JSON objects/arrays (non-streaming bodies) don't start with a quote and
// pass through unchanged; a malformed string token falls back to the raw bytes.
func decodeInlineBody(b []byte) string {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if json.Unmarshal(b, &s) == nil {
			return s
		}
	}
	return string(b)
}

// decodeHeaders parses a Record-feed header payload — a JSON object of
// single-value headers ({name: value}) — into the contract's multi-value
// shape ({name: [value]}). Returns nil on malformed input so the tab simply
// renders empty rather than erroring.
func decodeHeaders(body []byte) map[string][]string {
	var flat map[string]string
	if json.Unmarshal(body, &flat) != nil || len(flat) == 0 {
		return nil
	}
	out := make(map[string][]string, len(flat))
	for k, v := range flat {
		out[k] = []string{v}
	}
	return out
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
