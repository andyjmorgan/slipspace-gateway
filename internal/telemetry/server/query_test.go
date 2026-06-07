package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/config"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// fakeQueries is a programmable Queries (no DB).
type fakeQueries struct {
	summary      store.DashboardSummary
	summaryErr   error
	series       []store.DashboardSeriesBucket
	seriesErr    error
	events       []store.RequestEvent
	next         string
	eventsErr    error
	event        store.RequestEvent
	eventErr     error
	payloads     []store.Payload
	payErr       error
	session      []store.RequestEvent
	sessionErr   error
	sessions     []store.SessionSummary
	sessionsNext string
	sessionsErr  error
	facets       store.Facets
	facetsErr    error
	facetsHits   int
	// lastParams records the EventListParams of the most recent
	// ListEventsFiltered call so filter-plumbing tests can assert on it.
	lastParams store.EventListParams
	// lastSessionParams records the SessionListParams of the most recent
	// ListSessions call so the session-list filter-plumbing test can assert on it.
	lastSessionParams store.SessionListParams
}

func (f *fakeQueries) QueryDashboardSummary(context.Context, store.DashboardParams) (store.DashboardSummary, error) {
	return f.summary, f.summaryErr
}
func (f *fakeQueries) QueryDashboardSeries(context.Context, store.DashboardSeriesParams) ([]store.DashboardSeriesBucket, error) {
	return f.series, f.seriesErr
}
func (f *fakeQueries) ListEventsFiltered(_ context.Context, p store.EventListParams) ([]store.RequestEvent, string, error) {
	f.lastParams = p
	return f.events, f.next, f.eventsErr
}
func (f *fakeQueries) Facets(context.Context) (store.Facets, error) {
	f.facetsHits++
	return f.facets, f.facetsErr
}
func (f *fakeQueries) GetRequestEvent(context.Context, string) (store.RequestEvent, error) {
	return f.event, f.eventErr
}
func (f *fakeQueries) ListPayloads(context.Context, string) ([]store.Payload, error) {
	return f.payloads, f.payErr
}
func (f *fakeQueries) EventsBySession(context.Context, string) ([]store.RequestEvent, error) {
	return f.session, f.sessionErr
}
func (f *fakeQueries) ListSessions(_ context.Context, p store.SessionListParams) ([]store.SessionSummary, string, error) {
	f.lastSessionParams = p
	return f.sessions, f.sessionsNext, f.sessionsErr
}

func newQueryServer(t *testing.T, q Queries) http.Handler {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("hunter2"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	console := config.Console{Username: "admin", PasswordHash: string(hash)}
	return New(console, stubPinger{}, q, nil, discardLogger()).Handler()
}

// get runs a GET through the handler and returns the recorder (no http.Response,
// so there's no body to close — keeps bodyclose quiet for the many call sites).
func get(t *testing.T, h http.Handler, path string, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if auth {
		req.SetBasicAuth("admin", "hunter2")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestQuery_RequiresAuth(t *testing.T) {
	h := newQueryServer(t, &fakeQueries{})
	if resp := get(t, h, "/api/v1/dashboard/summary", false); resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}
}

func TestQuery_NoQueriesDisablesRoutes(t *testing.T) {
	// queries nil -> route not registered -> falls through to the console shell.
	hash, _ := bcrypt.GenerateFromPassword([]byte("hunter2"), bcrypt.MinCost)
	h := New(config.Console{Username: "admin", PasswordHash: string(hash)}, stubPinger{}, nil, nil, discardLogger()).Handler()
	resp := get(t, h, "/api/v1/dashboard/summary", true)
	// The catch-all GET / console handler answers 200 with the shell text.
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
}

func TestEvents(t *testing.T) {
	q := &fakeQueries{events: []store.RequestEvent{{CorrelationID: "c"}}, next: "cur2"}
	h := newQueryServer(t, q)
	resp := get(t, h, "/api/v1/events?from=2026-01-01T00:00:00Z&limit=10", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var body struct {
		Events     []store.RequestEvent `json:"events"`
		NextCursor string               `json:"next_cursor"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Events) != 1 || body.NextCursor != "cur2" {
		t.Errorf("body = %+v", body)
	}
}

func TestEvents_BadParamsAndErrors(t *testing.T) {
	h := newQueryServer(t, &fakeQueries{})
	if resp := get(t, h, "/api/v1/events?from=x", true); resp.Code != http.StatusBadRequest {
		t.Fatalf("bad from: %d", resp.Code)
	}
	if resp := get(t, h, "/api/v1/events?to=x", true); resp.Code != http.StatusBadRequest {
		t.Fatalf("bad to: %d", resp.Code)
	}
	hCur := newQueryServer(t, &fakeQueries{eventsErr: store.ErrInvalidCursor})
	if resp := get(t, hCur, "/api/v1/events?cursor=bad", true); resp.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor: %d", resp.Code)
	}
	hErr := newQueryServer(t, &fakeQueries{eventsErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/events", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("db err: %d", resp.Code)
	}
}

func TestEventInspector(t *testing.T) {
	q := &fakeQueries{
		event:    store.RequestEvent{CorrelationID: "c"},
		payloads: []store.Payload{{Kind: store.KindRequestBody, Body: []byte(`{"a":1}`)}},
	}
	h := newQueryServer(t, q)
	resp := get(t, h, "/api/v1/events/c", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	// not found
	hNF := newQueryServer(t, &fakeQueries{eventErr: store.ErrRequestEventNotFound})
	if resp := get(t, hNF, "/api/v1/events/x", true); resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.Code)
	}
	// payload-not-found is tolerated (event still returned)
	hNoPay := newQueryServer(t, &fakeQueries{event: store.RequestEvent{CorrelationID: "c"}, payErr: store.ErrPayloadNotFound})
	if resp := get(t, hNoPay, "/api/v1/events/c", true); resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	// event error
	hErr := newQueryServer(t, &fakeQueries{eventErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/events/c", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
	// payload hard error
	hPayErr := newQueryServer(t, &fakeQueries{event: store.RequestEvent{CorrelationID: "c"}, payErr: errors.New("db")})
	if resp := get(t, hPayErr, "/api/v1/events/c", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}

func TestEventBody(t *testing.T) {
	q := &fakeQueries{payloads: []store.Payload{{Kind: store.KindResponseBody, Body: []byte(`{}`)}}}
	h := newQueryServer(t, q)
	if resp := get(t, h, "/api/v1/events/c/body", true); resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	hNF := newQueryServer(t, &fakeQueries{payErr: store.ErrPayloadNotFound})
	if resp := get(t, hNF, "/api/v1/events/c/body", true); resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.Code)
	}
	hErr := newQueryServer(t, &fakeQueries{payErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/events/c/body", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}

func TestMessages_FiltersAndPaging(t *testing.T) {
	q := &fakeQueries{events: []store.RequestEvent{{CorrelationID: "c1"}}, next: "cur2"}
	h := newQueryServer(t, q)
	resp := get(t, h,
		"/api/v1/messages?provider=openai&model=gpt-4o&configuration=default&protocol=chat_completions"+
			"&session_id=sess-1&correlation_id=corr-1&status_class=2xx&tags=eu&tags=pii"+
			"&from=2026-01-01T00:00:00Z&to=2026-02-01T00:00:00Z&cursor=cur1&limit=25", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var body struct {
		Entries    []adminc.MessageEntry `json:"entries"`
		NextCursor string                `json:"next_cursor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Entries) != 1 || body.NextCursor != "cur2" {
		t.Fatalf("body = %+v", body)
	}
	// Every filter dimension must have reached the store untouched.
	got := q.lastParams
	if got.Cursor != "cur1" || got.Limit != 25 {
		t.Errorf("cursor/limit not plumbed: %+v", got)
	}
	if got.From.IsZero() || got.To.IsZero() {
		t.Errorf("window not plumbed: %+v", got)
	}
	f := got.Filter
	if f.Provider != "openai" || f.Model != "gpt-4o" || f.Configuration != "default" ||
		f.Protocol != "chat_completions" || f.SessionID != "sess-1" || f.CorrelationID != "corr-1" ||
		f.StatusClass != "2xx" {
		t.Errorf("scalar filters not plumbed: %+v", f)
	}
	if len(f.Tags) != 2 || f.Tags[0] != "eu" || f.Tags[1] != "pii" {
		t.Errorf("tags not plumbed: %+v", f.Tags)
	}
}

func TestMessages_BadParamsAndErrors(t *testing.T) {
	h := newQueryServer(t, &fakeQueries{})
	if resp := get(t, h, "/api/v1/messages?from=x", true); resp.Code != http.StatusBadRequest {
		t.Fatalf("bad from: %d", resp.Code)
	}
	hCur := newQueryServer(t, &fakeQueries{eventsErr: store.ErrInvalidCursor})
	if resp := get(t, hCur, "/api/v1/messages?cursor=bad", true); resp.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor: %d", resp.Code)
	}
	hErr := newQueryServer(t, &fakeQueries{eventsErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/messages", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("db err: %d", resp.Code)
	}
	// A blank ?tags= must not become a predicate that matches nothing.
	q := &fakeQueries{}
	hBlank := newQueryServer(t, q)
	if resp := get(t, hBlank, "/api/v1/messages?tags=", true); resp.Code != http.StatusOK {
		t.Fatalf("blank tag: %d", resp.Code)
	}
	if len(q.lastParams.Filter.Tags) != 0 {
		t.Errorf("blank tag became a predicate: %+v", q.lastParams.Filter.Tags)
	}
}

func TestFacets(t *testing.T) {
	q := &fakeQueries{facets: store.Facets{
		Providers: []string{"openai"}, Models: []string{"gpt-4o"},
		Configurations: []string{"default"}, Protocols: []string{"chat_completions"},
		Tags: []string{"eu"},
	}}
	h := newQueryServer(t, q)
	resp := get(t, h, "/api/v1/facets", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var body struct {
		Providers      []string `json:"providers"`
		Models         []string `json:"models"`
		Configurations []string `json:"configurations"`
		Protocols      []string `json:"protocols"`
		Tags           []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Providers) != 1 || body.Providers[0] != "openai" || len(body.Tags) != 1 {
		t.Fatalf("body = %+v", body)
	}
	// Second call within the TTL is served from cache — the store is hit once.
	_ = get(t, h, "/api/v1/facets", true)
	if q.facetsHits != 1 {
		t.Errorf("facets store hits = %d, want 1 (cached)", q.facetsHits)
	}
}

func TestFacets_NilSlicesAndError(t *testing.T) {
	// A fresh store with no events yields nil slices; the API must render [].
	q := &fakeQueries{}
	h := newQueryServer(t, q)
	resp := get(t, h, "/api/v1/facets", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	if got := resp.Body.String(); !json.Valid([]byte(got)) ||
		!contains(got, `"providers":[]`) || !contains(got, `"tags":[]`) {
		t.Errorf("nil slices not rendered as []: %s", got)
	}
	hErr := newQueryServer(t, &fakeQueries{facetsErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/facets", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("db err: %d", resp.Code)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestSession(t *testing.T) {
	// EventsBySession returns oldest-first; the handler must preserve that order
	// and project each event onto the tagged MessageEntry shape (parsed tags,
	// not raw base64 Detail) with totals over the session.
	q := &fakeQueries{session: []store.RequestEvent{
		{CorrelationID: "1", SessionID: "s", Model: "claude-opus-4-8", StatusCode: 200, TokensIn: 100, TokensOut: 10,
			Detail: []byte(`{"tags":["Claude-Code"],"rules_fired":["tag-claude-code"]}`)},
		{CorrelationID: "2", SessionID: "s", Model: "claude-haiku-4-5", StatusCode: 404, TokensIn: 5, TokensOut: 0},
	}}
	h := newQueryServer(t, q)
	resp := get(t, h, "/api/v1/sessions/s", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var sv adminc.SessionView
	if err := json.NewDecoder(resp.Body).Decode(&sv); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sv.SessionID != "s" {
		t.Errorf("session_id = %q", sv.SessionID)
	}
	if sv.Totals.Requests != 2 || sv.Totals.Errors != 1 || sv.Totals.TokensIn != 105 || sv.Totals.TokensOut != 10 {
		t.Errorf("totals = %+v", sv.Totals)
	}
	if len(sv.Requests) != 2 || sv.Requests[0].CorrelationID != "1" || sv.Requests[1].CorrelationID != "2" {
		t.Fatalf("requests not oldest-first: %+v", sv.Requests)
	}
	if len(sv.Requests[0].Tags) != 1 || sv.Requests[0].Tags[0] != "Claude-Code" {
		t.Errorf("tags not parsed: %+v", sv.Requests[0].Tags)
	}
	if len(sv.Requests[0].RulesMatched) != 1 || sv.Requests[0].RulesMatched[0].RuleName != "tag-claude-code" {
		t.Errorf("rules not parsed: %+v", sv.Requests[0].RulesMatched)
	}
	// empty -> 404
	hEmpty := newQueryServer(t, &fakeQueries{})
	if resp := get(t, hEmpty, "/api/v1/sessions/s", true); resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.Code)
	}
	// error
	hErr := newQueryServer(t, &fakeQueries{sessionErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/sessions/s", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}

func TestSessions_List(t *testing.T) {
	// The list endpoint projects SessionSummary rows onto the tagged wire shape;
	// a nil Models slice must render as [] not null.
	q := &fakeQueries{
		sessions: []store.SessionSummary{
			{SessionID: "s1", Messages: 3, TotalTokens: 120, Models: []string{"claude-opus-4-8"},
				Started: time.Unix(10, 0), LastAt: time.Unix(40, 0)},
			{SessionID: "s2", Messages: 1, TotalTokens: 0, Models: nil,
				Started: time.Unix(5, 0), LastAt: time.Unix(5, 0)},
		},
		sessionsNext: "cur2",
	}
	h := newQueryServer(t, q)
	resp := get(t, h, "/api/v1/sessions?from=2026-01-01T00:00:00Z&to=2026-02-01T00:00:00Z&limit=10", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	if got := resp.Body.String(); !contains(got, `"models":[]`) {
		t.Errorf("nil Models not rendered as []: %s", got)
	}
	var sl adminc.SessionList
	if err := json.NewDecoder(resp.Body).Decode(&sl); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(sl.Sessions) != 2 || sl.NextCursor != "cur2" {
		t.Fatalf("list = %+v", sl)
	}
	if sl.Sessions[0].SessionID != "s1" || sl.Sessions[0].Messages != 3 || sl.Sessions[0].TotalTokens != 120 {
		t.Errorf("summary[0] = %+v", sl.Sessions[0])
	}
	// The window bounds reached the store as the params (proves plumbing).
	if q.lastSessionParams.From.IsZero() || q.lastSessionParams.To.IsZero() || q.lastSessionParams.Limit != 10 {
		t.Errorf("params not plumbed: %+v", q.lastSessionParams)
	}
}

func TestSessions_ListFilters(t *testing.T) {
	// configuration + repeated tags from the query string must reach ListSessions
	// via filterFromQuery — the same path the message browser uses.
	q := &fakeQueries{}
	h := newQueryServer(t, q)
	if resp := get(t, h, "/api/v1/sessions?configuration=prod&tags=a&tags=b", true); resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	f := q.lastSessionParams.Filter
	if f.Configuration != "prod" || len(f.Tags) != 2 || f.Tags[0] != "a" || f.Tags[1] != "b" {
		t.Errorf("filter not plumbed: %+v", f)
	}
}

func TestSessions_ListBadParamsAndErrors(t *testing.T) {
	h := newQueryServer(t, &fakeQueries{})
	if resp := get(t, h, "/api/v1/sessions?from=x", true); resp.Code != http.StatusBadRequest {
		t.Fatalf("bad from: %d", resp.Code)
	}
	hCur := newQueryServer(t, &fakeQueries{sessionsErr: store.ErrInvalidCursor})
	if resp := get(t, hCur, "/api/v1/sessions?cursor=bad", true); resp.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor: %d", resp.Code)
	}
	hErr := newQueryServer(t, &fakeQueries{sessionsErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/sessions", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("db err: %d", resp.Code)
	}
}
