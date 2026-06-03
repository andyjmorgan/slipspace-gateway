package controlplane

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/receipt"
)

type fakeEventReader struct {
	recent []configdb.RequestEvent
	one    configdb.RequestEvent
	getErr error
	limit  int // captured from the last ListRecentRequestEvents call

	filtered   []configdb.RequestEvent
	nextCursor string
	filterErr  error
	lastList   configdb.EventListParams // captured from the last ListEventsFiltered call

	stats     configdb.EventStats
	statsErr  error
	lastStats configdb.EventStatsParams // captured from the last QueryEventStats call
}

func (f *fakeEventReader) ListRecentRequestEvents(_ context.Context, limit int) ([]configdb.RequestEvent, error) {
	f.limit = limit
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.recent, nil
}

func (f *fakeEventReader) GetRequestEvent(_ context.Context, _ string) (configdb.RequestEvent, error) {
	return f.one, f.getErr
}

func (f *fakeEventReader) ListEventsFiltered(_ context.Context, p configdb.EventListParams) ([]configdb.RequestEvent, string, error) {
	f.lastList = p
	if f.filterErr != nil {
		return nil, "", f.filterErr
	}
	return f.filtered, f.nextCursor, nil
}

func (f *fakeEventReader) QueryEventStats(_ context.Context, p configdb.EventStatsParams) (configdb.EventStats, error) {
	f.lastStats = p
	if f.statsErr != nil {
		return configdb.EventStats{}, f.statsErr
	}
	return f.stats, nil
}

func obsReq(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
	return rec
}

func TestObservabilityHandler_ListEvents(t *testing.T) {
	store := &fakeEventReader{
		filtered: []configdb.RequestEvent{
			{CorrelationID: "c1", Model: "gpt-4o", TokensIn: 7, GenAIContent: []byte(`{"p":"hi"}`)},
		},
		nextCursor: "CURSOR2",
	}
	h := NewObservabilityHandler(store, nil, nil, nil)

	rec := obsReq(h, "/api/v1/observability/events?limit=25&configuration=production&status_class=2xx&from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z&cursor=C1")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	if store.lastList.Limit != 25 {
		t.Errorf("limit not parsed: got %d, want 25", store.lastList.Limit)
	}
	if store.lastList.Filter.Configuration != "production" || store.lastList.Filter.StatusClass != "2xx" {
		t.Errorf("filter not parsed: %+v", store.lastList.Filter)
	}
	if store.lastList.Cursor != "C1" {
		t.Errorf("cursor not parsed: %q", store.lastList.Cursor)
	}
	if store.lastList.From.IsZero() || store.lastList.To.IsZero() {
		t.Errorf("time range not parsed: %+v / %+v", store.lastList.From, store.lastList.To)
	}

	var env eventsEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Events) != 1 || env.Events[0].CorrelationID != "c1" || env.Events[0].Model != "gpt-4o" {
		t.Fatalf("events = %+v", env.Events)
	}
	if env.NextCursor != "CURSOR2" {
		t.Errorf("next_cursor = %q, want CURSOR2", env.NextCursor)
	}
	// gen_ai content embeds as JSON, not a base64 blob.
	if string(env.Events[0].GenAIContent) != `{"p":"hi"}` {
		t.Errorf("gen_ai_content = %s", env.Events[0].GenAIContent)
	}
}

func TestObservabilityHandler_ListEvents_EmptyPage(t *testing.T) {
	h := NewObservabilityHandler(&fakeEventReader{}, nil, nil, nil)
	rec := obsReq(h, "/api/v1/observability/events")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200", rec.Code)
	}
	var env eventsEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Empty page serializes events as [] (not null) and an empty cursor.
	if env.Events == nil || len(env.Events) != 0 || env.NextCursor != "" {
		t.Fatalf("env = %+v", env)
	}
}

func TestObservabilityHandler_ListEvents_BadLimitIgnored(t *testing.T) {
	store := &fakeEventReader{}
	h := NewObservabilityHandler(store, nil, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/events?limit=notanumber"); rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200", rec.Code)
	}
	if store.lastList.Limit != 0 {
		t.Errorf("bad limit should fall through to the store default (0), got %d", store.lastList.Limit)
	}
}

func TestObservabilityHandler_ListEvents_BadTimeRange(t *testing.T) {
	h := NewObservabilityHandler(&fakeEventReader{}, nil, nil, nil)
	for _, path := range []string{
		"/api/v1/observability/events?from=not-a-time",
		"/api/v1/observability/events?to=not-a-time",
	} {
		if rec := obsReq(h, path); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", path, rec.Code)
		}
	}
}

func TestObservabilityHandler_ListEvents_InvalidCursor(t *testing.T) {
	h := NewObservabilityHandler(&fakeEventReader{filterErr: configdb.ErrInvalidCursor}, nil, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/events?cursor=garbage"); rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d, want 400", rec.Code)
	}
}

func TestObservabilityHandler_ListEvents_StoreError(t *testing.T) {
	h := NewObservabilityHandler(&fakeEventReader{filterErr: errors.New("db down")}, nil, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/events"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500", rec.Code)
	}
}

func TestObservabilityHandler_Stats(t *testing.T) {
	store := &fakeEventReader{stats: configdb.EventStats{
		Totals: configdb.StatTotals{Requests: 10, Errors: 2, ErrorRate: 0.2, AvgLatencyMs: 100, P95LatencyMs: 180, TokensIn: 50, TokensOut: 70},
		Series: []configdb.StatBucket{{Requests: 10, Status2xx: 8, Status4xx: 1, Status5xx: 1}},
		Top:    []configdb.StatGroup{{Key: "gpt-4o", Requests: 10, ErrorRate: 0.2}},
	}}
	h := NewObservabilityHandler(store, nil, nil, nil)

	rec := obsReq(h, "/api/v1/observability/stats?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z&bucket=300&group_by=model&model=gpt-4o&status_class=5xx")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	if store.lastStats.BucketSeconds != 300 || store.lastStats.GroupBy != "model" {
		t.Errorf("params not parsed: %+v", store.lastStats)
	}
	if store.lastStats.Filter.Model != "gpt-4o" || store.lastStats.Filter.StatusClass != "5xx" {
		t.Errorf("filter not parsed: %+v", store.lastStats.Filter)
	}

	var resp statsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.BucketSeconds != 300 || resp.Totals.Requests != 10 || resp.Totals.ErrorRate != 0.2 {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.Series) != 1 || resp.Series[0].Status2xx != 8 {
		t.Fatalf("series = %+v", resp.Series)
	}
	if len(resp.Top) != 1 || resp.Top[0].Key != "gpt-4o" {
		t.Fatalf("top = %+v", resp.Top)
	}
}

func TestObservabilityHandler_Stats_BadParams(t *testing.T) {
	h := NewObservabilityHandler(&fakeEventReader{}, nil, nil, nil)
	for name, path := range map[string]string{
		"missing from/to":  "/api/v1/observability/stats?bucket=300",
		"missing bucket":   "/api/v1/observability/stats?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z",
		"zero bucket":      "/api/v1/observability/stats?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z&bucket=0",
		"bad bucket":       "/api/v1/observability/stats?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z&bucket=abc",
		"to before from":   "/api/v1/observability/stats?from=2026-06-02T00:00:00Z&to=2026-06-01T00:00:00Z&bucket=300",
		"bad status_class": "/api/v1/observability/stats?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z&bucket=300&status_class=6xx",
		"bad group_by":     "/api/v1/observability/stats?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z&bucket=300&group_by=region",
	} {
		t.Run(name, func(t *testing.T) {
			if rec := obsReq(h, path); rec.Code != http.StatusBadRequest {
				t.Fatalf("= %d, want 400", rec.Code)
			}
		})
	}
}

func TestObservabilityHandler_Stats_StoreError(t *testing.T) {
	h := NewObservabilityHandler(&fakeEventReader{statsErr: errors.New("db down")}, nil, nil, nil)
	path := "/api/v1/observability/stats?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z&bucket=300"
	if rec := obsReq(h, path); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500", rec.Code)
	}
}

func TestObservabilityHandler_GetEvent(t *testing.T) {
	store := &fakeEventReader{one: configdb.RequestEvent{
		CorrelationID:       "c9",
		Backend:             "openai",
		Method:              "POST",
		UpstreamStatus:      502,
		TokensCached:        64,
		TokensCacheCreation: 8,
		SessionID:           "bundle-9",
		SessionIDSource:     "X-Agentling-Task-Id",
		APIKeyName:          "internal-svc",
		PolicyRef:           "failover-pool",
		Streaming:           true,
		Detail:              []byte(`{"tags":["env:prod"],"rules_fired":["redirect-claude"]}`),
	}}
	h := NewObservabilityHandler(store, nil, nil, nil)

	rec := obsReq(h, "/api/v1/observability/events/c9")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	var v eventView
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v.CorrelationID != "c9" || v.Backend != "openai" {
		t.Errorf("view = %+v", v)
	}
	if v.Method != "POST" || v.UpstreamStatus != 502 || !v.Streaming {
		t.Errorf("enrichment scalars = %+v", v)
	}
	if v.TokensCached != 64 || v.TokensCacheCreation != 8 {
		t.Errorf("cache tokens = (%d,%d)", v.TokensCached, v.TokensCacheCreation)
	}
	if v.SessionID != "bundle-9" || v.SessionIDSource != "X-Agentling-Task-Id" {
		t.Errorf("session = (%q,%q)", v.SessionID, v.SessionIDSource)
	}
	if v.APIKeyName != "internal-svc" || v.PolicyRef != "failover-pool" {
		t.Errorf("apikey/policy = (%q,%q)", v.APIKeyName, v.PolicyRef)
	}
	// detail embeds as JSON, not a base64 blob.
	if string(v.Detail) != `{"tags":["env:prod"],"rules_fired":["redirect-claude"]}` {
		t.Errorf("detail = %s", v.Detail)
	}
}

func TestObservabilityHandler_GetEvent_NotFound(t *testing.T) {
	h := NewObservabilityHandler(&fakeEventReader{getErr: configdb.ErrRequestEventNotFound}, nil, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/events/ghost"); rec.Code != http.StatusNotFound {
		t.Fatalf("= %d, want 404", rec.Code)
	}
}

func TestObservabilityHandler_GetEvent_StoreError(t *testing.T) {
	h := NewObservabilityHandler(&fakeEventReader{getErr: errors.New("boom")}, nil, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/events/x"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500", rec.Code)
	}
}

type fakeBodyReader struct {
	bodies []configdb.RequestBody
	err    error
}

func (f *fakeBodyReader) ListRequestBodies(context.Context, string) ([]configdb.RequestBody, error) {
	return f.bodies, f.err
}

func TestObservabilityHandler_GetBodies(t *testing.T) {
	bodies := &fakeBodyReader{bodies: []configdb.RequestBody{
		{CorrelationID: "c1", InstanceID: "gw-1", Seq: 1, TsNs: 100, Body: []byte(`{"k":"v"}`)},
		{CorrelationID: "c1", InstanceID: "gw-1", Seq: 2, TsNs: 200, Body: []byte(`{"k":"w"}`)},
	}}
	h := NewObservabilityHandler(nil, bodies, nil, nil)

	rec := obsReq(h, "/api/v1/observability/bodies/c1")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	var views []bodyView
	if err := json.NewDecoder(rec.Body).Decode(&views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(views) != 2 || views[0].Seq != 1 || views[1].Seq != 2 {
		t.Fatalf("views = %+v", views)
	}
	// The captured body embeds as JSON, not a base64 blob.
	if string(views[0].Body) != `{"k":"v"}` {
		t.Errorf("body[0] = %s", views[0].Body)
	}
}

func TestObservabilityHandler_GetBodies_NotFound(t *testing.T) {
	h := NewObservabilityHandler(nil, &fakeBodyReader{err: configdb.ErrRequestBodyNotFound}, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/bodies/ghost"); rec.Code != http.StatusNotFound {
		t.Fatalf("= %d, want 404", rec.Code)
	}
}

func TestObservabilityHandler_GetBodies_StoreError(t *testing.T) {
	h := NewObservabilityHandler(nil, &fakeBodyReader{err: errors.New("db down")}, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/bodies/x"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500", rec.Code)
	}
}

type fakeReceiptLister struct {
	chain []receipt.Receipt
	err   error
}

func (f *fakeReceiptLister) ListReceipts(context.Context, string) ([]receipt.Receipt, error) {
	return f.chain, f.err
}

func obsVerifySigner() *receipt.Ed25519Signer {
	seed := make([]byte, ed25519.SeedSize)
	return receipt.NewEd25519Signer("k", ed25519.NewKeyFromSeed(seed))
}

func TestObservabilityHandler_VerifyChain_Valid(t *testing.T) {
	signer := obsVerifySigner()
	var prev []byte
	var chain []receipt.Receipt
	for i := 1; i <= 3; i++ {
		rc := receipt.Chain(prev, receipt.Record{GatewayID: "gw-1", Seq: uint64(i), Payload: []byte("x")}, signer)
		chain = append(chain, rc)
		prev = rc.Hash
	}
	h := NewObservabilityHandler(nil, nil, &fakeReceiptLister{chain: chain}, signer.Public())

	rec := obsReq(h, "/api/v1/observability/receipts/gw-1/verify")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	var v chainVerifyView
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !v.Verified || v.Count != 3 || v.GatewayID != "gw-1" {
		t.Fatalf("verify view = %+v", v)
	}
}

func TestObservabilityHandler_VerifyChain_Tampered(t *testing.T) {
	signer := obsVerifySigner()
	rc := receipt.Chain(nil, receipt.Record{GatewayID: "gw-1", Seq: 1, Payload: []byte("x")}, signer)
	rc.Payload = []byte("tampered")
	h := NewObservabilityHandler(nil, nil, &fakeReceiptLister{chain: []receipt.Receipt{rc}}, signer.Public())

	rec := obsReq(h, "/api/v1/observability/receipts/gw-1/verify")
	var v chainVerifyView
	_ = json.NewDecoder(rec.Body).Decode(&v)
	if rec.Code != http.StatusOK || v.Verified || v.Error == "" {
		t.Fatalf("tampered chain: code=%d view=%+v", rec.Code, v)
	}
}

func TestObservabilityHandler_VerifyChain_StoreError(t *testing.T) {
	h := NewObservabilityHandler(nil, nil, &fakeReceiptLister{err: errors.New("db down")}, obsVerifySigner().Public())
	if rec := obsReq(h, "/api/v1/observability/receipts/gw-1/verify"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500", rec.Code)
	}
}
