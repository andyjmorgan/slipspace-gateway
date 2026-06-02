package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

type fakeEventReader struct {
	recent []configdb.RequestEvent
	one    configdb.RequestEvent
	getErr error
	limit  int // captured from the last ListRecentRequestEvents call
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

func obsReq(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
	return rec
}

func TestObservabilityHandler_ListEvents(t *testing.T) {
	store := &fakeEventReader{recent: []configdb.RequestEvent{
		{CorrelationID: "c1", Model: "gpt-4o", TokensIn: 7, GenAIContent: []byte(`{"p":"hi"}`)},
	}}
	h := NewObservabilityHandler(store)

	rec := obsReq(h, "/api/v1/observability/events?limit=25")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	if store.limit != 25 {
		t.Errorf("limit not parsed: got %d, want 25", store.limit)
	}
	var views []eventView
	if err := json.NewDecoder(rec.Body).Decode(&views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(views) != 1 || views[0].CorrelationID != "c1" || views[0].Model != "gpt-4o" {
		t.Fatalf("views = %+v", views)
	}
	// gen_ai content embeds as JSON, not a base64 blob.
	if string(views[0].GenAIContent) != `{"p":"hi"}` {
		t.Errorf("gen_ai_content = %s", views[0].GenAIContent)
	}
}

func TestObservabilityHandler_ListEvents_BadLimitIgnored(t *testing.T) {
	store := &fakeEventReader{}
	h := NewObservabilityHandler(store)
	if rec := obsReq(h, "/api/v1/observability/events?limit=notanumber"); rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200", rec.Code)
	}
	if store.limit != 0 {
		t.Errorf("bad limit should fall through to the store default (0), got %d", store.limit)
	}
}

func TestObservabilityHandler_ListEvents_StoreError(t *testing.T) {
	h := NewObservabilityHandler(&fakeEventReader{getErr: errors.New("db down")})
	if rec := obsReq(h, "/api/v1/observability/events"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500", rec.Code)
	}
}

func TestObservabilityHandler_GetEvent(t *testing.T) {
	store := &fakeEventReader{one: configdb.RequestEvent{CorrelationID: "c9", Backend: "openai"}}
	h := NewObservabilityHandler(store)

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
}

func TestObservabilityHandler_GetEvent_NotFound(t *testing.T) {
	h := NewObservabilityHandler(&fakeEventReader{getErr: configdb.ErrRequestEventNotFound})
	if rec := obsReq(h, "/api/v1/observability/events/ghost"); rec.Code != http.StatusNotFound {
		t.Fatalf("= %d, want 404", rec.Code)
	}
}

func TestObservabilityHandler_GetEvent_StoreError(t *testing.T) {
	h := NewObservabilityHandler(&fakeEventReader{getErr: errors.New("boom")})
	if rec := obsReq(h, "/api/v1/observability/events/x"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500", rec.Code)
	}
}
