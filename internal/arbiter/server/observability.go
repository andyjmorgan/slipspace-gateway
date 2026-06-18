package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/arbiter/stitch"
	"github.com/andyjmorgan/sluice-gateway/internal/arbiter/store"
)

// The telemetry console reuses the gateway admin SPA's observability
// components unchanged, so its API must emit the SAME JSON the gateway admin
// API does — the contracts/admin shapes. These handlers map the DB-backed
// store query results into those shapes. Paths live in the telemetry TS client
// (the components take an injected client), so only the shapes are load-bearing.

// allowedWindows maps the SPA's window tokens to durations. Unknown tokens fall
// back to 1h.
var allowedWindows = map[string]time.Duration{
	"15m": 15 * time.Minute,
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

// parseWindow resolves the ?window token to (token, duration), defaulting to 1h
// — the recent slice operators look at most often, and the only window that
// stays meaningful right after a fresh deploy when little history exists.
func parseWindow(r *http.Request) (string, time.Duration) {
	tok := r.URL.Query().Get("window")
	if d, ok := allowedWindows[tok]; ok {
		return tok, d
	}
	return "1h", time.Hour
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

// handleObsSecurity emits contracts/admin.DashboardSecurity — the Arbiter
// posture + finding breakdown for the dashboard's security rows. When the
// scanner is disabled it returns enabled=false with zero counts and never
// touches the DB, so operators not running the scanner pay nothing.
func (s *Server) handleObsSecurity(w http.ResponseWriter, r *http.Request) {
	tok, dur := parseWindow(r)
	out := adminc.DashboardSecurity{
		Enabled:       s.scannerEnabled(),
		Window:        tok,
		ByCheckType:   []adminc.DashboardSecurityCount{},
		TopCategories: []adminc.DashboardSecurityCount{},
	}
	if out.Enabled {
		from, to := windowBounds(time.Now(), dur)
		sec, err := s.queries.QueryDashboardSecurity(r.Context(), from, to)
		if err != nil {
			s.queryError(w, "dashboard security", err)
			return
		}
		out.Scanned = sec.Scanned
		out.Flagged = sec.Flagged
		out.Partial = sec.Partial
		out.Clean = sec.Clean
		out.ByCheckType = mapSecurityCounts(sec.ByCheckType)
		out.TopCategories = mapSecurityCounts(sec.TopCategories)
	}
	writeJSON(w, http.StatusOK, out)
}

// scanAuditDefaultLimit / scanAuditMaxLimit bound the scan-failure panel: a
// sensible default page when ?limit is absent, and a hard cap so a hostile or
// stale query can't ask for an unbounded scan.
const (
	scanAuditDefaultLimit = 100
	scanAuditMaxLimit     = 500
)

// handleObsSecurityAudit emits contracts/admin.DashboardSecurityAudit — the
// append-only log of operational scan failures for the dashboard's scan-
// failures panel. When the scanner is disabled it returns an empty (non-nil)
// item list and never touches the DB, mirroring handleObsSecurity.
func (s *Server) handleObsSecurityAudit(w http.ResponseWriter, r *http.Request) {
	tok, dur := parseWindow(r)
	out := adminc.DashboardSecurityAudit{
		Window: tok,
		Items:  []adminc.ScanAuditEntry{},
	}
	if s.scannerEnabled() {
		limit := limitParam(r, scanAuditDefaultLimit)
		if limit <= 0 {
			limit = scanAuditDefaultLimit
		}
		if limit > scanAuditMaxLimit {
			limit = scanAuditMaxLimit
		}
		from, to := windowBounds(time.Now(), dur)
		entries, err := s.queries.ListScanAudit(r.Context(), from, to, limit)
		if err != nil {
			s.queryError(w, "dashboard security audit", err)
			return
		}
		out.Items = mapScanAudit(entries)
	}
	writeJSON(w, http.StatusOK, out)
}

// mapScanAudit maps the store scan-failure rows to the contract shape, always
// returning a non-nil slice so the JSON carries [] not null.
func mapScanAudit(in []store.ScanAuditEntry) []adminc.ScanAuditEntry {
	out := make([]adminc.ScanAuditEntry, 0, len(in))
	for _, e := range in {
		out = append(out, adminc.ScanAuditEntry{
			CorrelationID: e.CorrelationID,
			UnitID:        e.UnitID,
			CheckType:     e.CheckType,
			DetectorID:    e.DetectorID,
			Reason:        e.Reason,
			Attempts:      e.Attempts,
			Detail:        e.Detail,
			OccurredAt:    e.OccurredAt.UTC(),
		})
	}
	return out
}

// mapSecurityCounts maps the store breakdown rows to the contract shape,
// always returning a non-nil slice so the JSON carries [] not null.
func mapSecurityCounts(in []store.DashboardSecurityCount) []adminc.DashboardSecurityCount {
	out := make([]adminc.DashboardSecurityCount, 0, len(in))
	for _, c := range in {
		out = append(out, adminc.DashboardSecurityCount{Key: c.Key, Count: c.Count})
	}
	return out
}

// scannerEnabled reports whether the Arbiter scanner is on, read from the
// applied-config snapshot. nil snapshot (WithAppliedConfig never called) reads
// as disabled.
func (s *Server) scannerEnabled() bool {
	return s.appliedConfig != nil && s.appliedConfig.ScannerEnabled()
}

// handleObsMessagesRecent emits contracts/admin.MessagesRecentResponse mapped
// from the most recent events (oldest-first, matching the gateway live feed).
func (s *Server) handleObsMessagesRecent(w http.ResponseWriter, r *http.Request) {
	limit := limitParam(r, 200)
	events, _, err := s.queries.ListEventsFiltered(r.Context(), store.EventListParams{
		Filter: filterFromQuery(r), Limit: limit,
	})
	if err != nil {
		s.queryError(w, "messages recent", err)
		return
	}
	writeJSON(w, http.StatusOK, mapMessages(events, limit))
}

// handleObsMessageBody emits contracts/admin.MessageBodyDetail for a
// correlation id. The raw bodies/headers + assembled rollup come from the
// lazily-joined record (present iff reporting forwarding was on); the gen_ai
// content comes from the entity's span_event blob (present iff content capture
// was on). ?include=telemetry serves only the span side (gen_ai content + raw
// span), ?include=report only the record side (wire bodies + headers) — the
// console's bridge tabs fetch per side so pressing Telemetry never pulls the
// record and pressing Report never reads the span blob. Any other value (or
// none) serves both, the pre-split shape. 404 when nothing the request asked
// for exists.
func (s *Server) handleObsMessageBody(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	include := r.URL.Query().Get("include")
	wantSpan := include != "report"
	wantRecord := include != "telemetry"

	var genAI, spanEvent []byte
	spanFound := false
	if wantSpan {
		event, evErr := s.queries.GetRequestEvent(r.Context(), id)
		if evErr != nil && !errors.Is(evErr, store.ErrRequestEventNotFound) {
			s.queryError(w, "message body", evErr)
			return
		}
		if evErr == nil {
			spanFound = true
			if c := event.DecodeSpanFields().GenAIContent; len(c) > 0 {
				genAI = c
			}
			// The complete span verbatim, so the raw telemetry pane shows every
			// field on the span (not just the gen_ai content sub-object).
			spanEvent = event.SpanEvent
		}
	}

	var rec *cc.Record
	if wantRecord {
		var err error
		rec, err = s.recordFor(r.Context(), id)
		if err != nil {
			s.queryError(w, "message body", err)
			return
		}
	}
	// Nothing the caller asked for exists for this id.
	if rec == nil && !spanFound {
		writeError(w, http.StatusNotFound, "no body")
		return
	}
	writeJSON(w, http.StatusOK, mapBody(id, rec, genAI, spanEvent))
}

// recordFor fetches a correlation id's verbatim record blob and decodes it into
// a *cc.Record. Missing (reporting off / not arrived) or malformed is (nil, nil)
// so callers degrade to the entity-only view.
func (s *Server) recordFor(ctx context.Context, correlationID string) (*cc.Record, error) {
	body, err := s.queries.GetRecordBody(ctx, correlationID)
	if err != nil {
		if errors.Is(err, store.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var rec cc.Record
	if json.Unmarshal(body, &rec) != nil {
		return nil, nil
	}
	return &rec, nil
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
	sort, asc := sortFromQuery(r)
	filter := filterFromQuery(r)
	events, next, err := s.queries.ListEventsFiltered(r.Context(), store.EventListParams{
		From:   from,
		To:     to,
		Filter: filter,
		Cursor: q.Get("cursor"),
		Limit:  limitParam(r, 0),
		Sort:   sort,
		Asc:    asc,
	})
	if err != nil {
		if errors.Is(err, store.ErrInvalidCursor) {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		s.queryError(w, "messages", err)
		return
	}
	// total is the full matching count (filter + window, page-independent) so the
	// pager can show "X–Y of N".
	total, err := s.queries.CountEventsFiltered(r.Context(), store.EventCountParams{From: from, To: to, Filter: filter})
	if err != nil {
		s.queryError(w, "count messages", err)
		return
	}
	// Keep store order (newest-first) — this is a browser, not the live feed.
	entries := make([]adminc.MessageEntry, 0, len(events))
	for _, e := range events {
		entries = append(entries, mapEntry(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "next_cursor": next, "total": total})
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
		"protocols":      nonNil(f.Protocols),
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
		ByProvider:      make([]adminc.DashboardProviderRow, 0, len(s.ByProvider)),
		ByProtocol:      make([]adminc.DashboardProtocolRow, 0, len(s.ByProtocol)),
		ByConfiguration: make([]adminc.DashboardConfigurationRow, 0, len(s.ByConfiguration)),
		ByModel:         make([]adminc.DashboardModelRow, 0, len(s.ByModel)),
		RulesFired:      make([]adminc.DashboardRuleFiredRow, 0, len(s.RulesFired)),
		TagsFired:       make([]adminc.DashboardTagFiredRow, 0, len(s.TagsFired)),
		ProviderHealth:  make([]adminc.DashboardProviderHealth, 0, len(s.ProviderHealth)),
	}
	for _, r := range s.ByProvider {
		out.ByProvider = append(out.ByProvider, adminc.DashboardProviderRow{Provider: r.Key, Requests: r.Requests, ErrorRate: r.ErrorRate})
	}
	for _, r := range s.ByProtocol {
		out.ByProtocol = append(out.ByProtocol, adminc.DashboardProtocolRow{Provider: r.Provider, Protocol: r.Protocol, Requests: r.Requests, ErrorRate: r.ErrorRate})
	}
	for _, r := range s.ByConfiguration {
		out.ByConfiguration = append(out.ByConfiguration, adminc.DashboardConfigurationRow{Configuration: r.Key, Requests: r.Requests, ErrorRate: r.ErrorRate})
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

// mapSessionList projects a keyset page of session summaries onto the tagged
// SessionList wire shape. Models defaults to a non-nil slice so the JSON is
// `[]` rather than `null` for a session with no recorded model.
func mapSessionList(sessions []store.SessionSummary, nextCursor string) adminc.SessionList {
	out := adminc.SessionList{
		Sessions:   make([]adminc.SessionSummary, 0, len(sessions)),
		NextCursor: nextCursor,
	}
	for _, s := range sessions {
		out.Sessions = append(out.Sessions, mapSessionSummary(s))
	}
	return out
}

func mapSessionSummary(s store.SessionSummary) adminc.SessionSummary {
	models := s.Models
	if models == nil {
		models = []string{}
	}
	return adminc.SessionSummary{
		SessionID:    s.SessionID,
		Messages:     s.Messages,
		Subagents:    s.Subagents,
		TotalTokens:  s.TotalTokens,
		Models:       models,
		Tags:         nonNil(s.Tags),
		StartedAt:    s.Started.UTC(),
		LastActivity: s.LastAt.UTC(),
	}
}

// mapEntry projects an entity event onto the tagged MessageEntry shape. The
// scalars (ids, provider, model, configuration, protocol, status) come straight
// off the row's columns; everything else (source headers, method, outcome
// measurements, streaming, tags, rule chain, attempts) is decoded from the
// span_event blob via SpanFields. The whole span is in the blob (single-writer
// model), so the entry no longer depends on a Record having arrived.
func mapEntry(e store.RequestEvent) adminc.MessageEntry {
	f := e.DecodeSpanFields()
	entry := adminc.MessageEntry{
		EventID:              e.CorrelationID,
		At:                   e.ObservedAt.UTC(),
		CorrelationID:        e.CorrelationID,
		SessionID:            e.SessionID,
		SessionIDSource:      f.SessionIDSource,
		ConversationID:       e.ConversationID,
		ConversationIDSource: f.ConversationIDSource,
		ParentConversationID: e.ParentConversationID,
		AgentID:              e.AgentID,
		AgentIDSource:        f.AgentIDSource,
		UserID:               e.UserID,
		UserIDSource:         f.UserIDSource,
		Provider:             e.Provider,
		Protocol:             e.Protocol,
		Model:                e.Model,
		Method:               f.Method,
		Configuration:        e.Configuration,
		StatusCode:           e.StatusCode,
		DurationMs:           f.LatencyMs,
		Streaming:            f.Streaming,
		TokensIn:             int(f.TokensIn),
		TokensOut:            int(f.TokensOut),
		TokensCached:         int(f.TokensCached),
		TokensCacheCreation:  int(f.TokensCacheCreation),
		PolicyRef:            f.PolicyRef,
		Tags:                 f.Tags,
	}
	// Prefer the full rule chain (actions / terminated / error) when present;
	// fall back to the flat name list (older records carry names only).
	if len(f.RuleChain) > 0 {
		for _, r := range f.RuleChain {
			entry.RulesMatched = append(entry.RulesMatched, adminc.RuleHit{
				RuleName:       r.Name,
				ActionsApplied: r.ActionsApplied,
				Terminated:     r.Terminated,
				ErrorMessage:   r.ErrorMessage,
			})
		}
	} else {
		for _, r := range f.RulesFired {
			entry.RulesMatched = append(entry.RulesMatched, adminc.RuleHit{RuleName: r})
		}
	}
	for _, a := range f.Attempts {
		entry.Attempts = append(entry.Attempts, adminc.AttemptHit{
			Target:     a.Target,
			StartedAt:  time.Unix(0, a.StartedAtNs).UTC(),
			DurationMs: a.DurationMs,
			StatusCode: a.StatusCode,
			Error:      a.Error,
			Outcome:    a.Outcome,
		})
	}
	return entry
}

// mapBody projects the lazily-joined record (raw bodies/headers + assembled
// rollup) and the entity's gen_ai content onto MessageBodyDetail. A nil record
// (reporting off / not arrived) leaves the body/header fields empty; the gen_ai
// content still renders from the span blob.
func mapBody(correlationID string, rec *cc.Record, genAIContent, spanEvent []byte) adminc.MessageBodyDetail {
	detail := adminc.MessageBodyDetail{EventID: correlationID}
	if rec != nil {
		if !rec.Request.BodyOmitted && len(rec.Request.Body) > 0 {
			detail.Request = decodeInlineBody(rec.Request.Body)
			detail.RequestTotalBytes = int64(len(detail.Request))
		}
		if !rec.Response.BodyOmitted && len(rec.Response.Body) > 0 {
			detail.Response = decodeInlineBody(rec.Response.Body)
			detail.ResponseTotalBytes = int64(len(detail.Response))
		}
		if len(rec.Response.Assembled) > 0 {
			detail.ResponseAssembled = string(rec.Response.Assembled)
		}
		detail.AssemblyPartial = rec.Response.AssemblyPartial
		detail.RequestHeaders = expandHeaders(rec.Request.Headers)
		detail.ResponseHeaders = expandHeaders(rec.Response.Headers)
	}
	if len(genAIContent) > 0 {
		detail.GenAIContent = string(genAIContent)
	}
	// The whole span verbatim — every attribute, not just the gen_ai content —
	// so the console's raw telemetry pane can show all fields on the span.
	if len(spanEvent) > 0 {
		detail.SpanEvent = string(spanEvent)
	}
	return detail
}

// decodeInlineBody renders a Record inline body (the connector contract's
// RequestPart/ResponsePart Body, a json.RawMessage) back to display text. The
// gateway's jsonBodyOrEscaped (cmd/gateway/reporter.go) stores JSON bodies
// verbatim but wraps non-JSON bodies — SSE streams, plain text — as a JSON
// string token so they're valid inside the json.RawMessage field. Reverse that
// here: a JSON string token decodes back to its raw text, so a streamed
// response shows real `event:` / `data:` lines on the Raw stream tab instead of
// a quoted, escaped blob. JSON objects/arrays (non-streaming bodies) don't start
// with a quote and pass through unchanged; a malformed string token falls back
// to the raw bytes.
func decodeInlineBody(b []byte) string {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if json.Unmarshal(b, &s) == nil {
			return s
		}
	}
	return string(b)
}

// expandHeaders converts the Record's flat single-value header map
// ({name: value}) into the contract's multi-value shape ({name: [value]}).
// Returns nil for an empty map so the tab renders empty.
func expandHeaders(flat map[string]string) map[string][]string {
	if len(flat) == 0 {
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
