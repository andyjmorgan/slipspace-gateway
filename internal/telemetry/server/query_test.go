package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/config"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// fakeQueries is a programmable Queries (no DB).
type fakeQueries struct {
	summary    store.DashboardSummary
	summaryErr error
	series     []store.DashboardSeriesBucket
	seriesErr  error
	events     []store.RequestEvent
	next       string
	eventsErr  error
	event      store.RequestEvent
	eventErr   error
	payloads   []store.Payload
	payErr     error
	session    []store.RequestEvent
	sessionErr error
}

func (f *fakeQueries) QueryDashboardSummary(context.Context, store.DashboardParams) (store.DashboardSummary, error) {
	return f.summary, f.summaryErr
}
func (f *fakeQueries) QueryDashboardSeries(context.Context, store.DashboardSeriesParams) ([]store.DashboardSeriesBucket, error) {
	return f.series, f.seriesErr
}
func (f *fakeQueries) ListEventsFiltered(context.Context, store.EventListParams) ([]store.RequestEvent, string, error) {
	return f.events, f.next, f.eventsErr
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

func TestSession(t *testing.T) {
	q := &fakeQueries{session: []store.RequestEvent{{CorrelationID: "1", SessionID: "s"}}}
	h := newQueryServer(t, q)
	if resp := get(t, h, "/api/v1/sessions/s", true); resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
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
