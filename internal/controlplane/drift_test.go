package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

type fakeFleet struct {
	gws []Gateway
	err error
}

func (f *fakeFleet) List(context.Context) ([]Gateway, error) { return f.gws, f.err }

type fakeActive struct {
	body []byte
	err  error
}

func (f *fakeActive) ActiveVersion(context.Context) (configdb.Version, error) {
	return configdb.Version{Body: f.body}, f.err
}

func driftReq(h http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/fleet/drift", http.NoBody))
	return rec
}

func TestDriftHandler_Statuses(t *testing.T) {
	resolved := providerTestStore().Snapshot()
	body, err := config.MarshalConfig(resolved)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	_, currentHash, err := config.MarshalClosure(resolved, "alpha")
	if err != nil {
		t.Fatalf("marshal closure: %v", err)
	}

	fleet := &fakeFleet{gws: []Gateway{
		{ID: "gw-current", Version: "v1", CachedConfigHashes: []string{currentHash}},
		{ID: "gw-drifted", Version: "v1", CachedConfigHashes: []string{"stale-hash"}},
		{ID: "gw-unknown", Version: "v1"},
	}}
	h := NewDriftHandler(fleet, &fakeActive{body: body})

	rec := driftReq(h)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	var rows []driftRow
	if err := json.NewDecoder(rec.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.GatewayID] = r.Status
	}
	want := map[string]string{
		"gw-current": DriftCurrent,
		"gw-drifted": DriftStale,
		"gw-unknown": DriftUnknown,
	}
	for id, status := range want {
		if got[id] != status {
			t.Errorf("%s = %q, want %q", id, got[id], status)
		}
	}
}

func TestDriftHandler_NoActiveVersionIsAllDrifted(t *testing.T) {
	fleet := &fakeFleet{gws: []Gateway{{ID: "gw", CachedConfigHashes: []string{"h"}}}}
	h := NewDriftHandler(fleet, &fakeActive{err: configdb.ErrNoActiveConfig})

	rec := driftReq(h)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200", rec.Code)
	}
	var rows []driftRow
	_ = json.NewDecoder(rec.Body).Decode(&rows)
	if len(rows) != 1 || rows[0].Status != DriftStale {
		t.Fatalf("rows = %+v (no published config -> a held hash is drifted)", rows)
	}
}

func TestDriftHandler_Errors(t *testing.T) {
	body, _ := config.MarshalConfig(providerTestStore().Snapshot())

	if rec := driftReq(NewDriftHandler(&fakeFleet{err: errors.New("db down")}, &fakeActive{body: body})); rec.Code != http.StatusInternalServerError {
		t.Errorf("fleet error = %d, want 500", rec.Code)
	}
	if rec := driftReq(NewDriftHandler(&fakeFleet{}, &fakeActive{err: errors.New("db down")})); rec.Code != http.StatusInternalServerError {
		t.Errorf("active error = %d, want 500", rec.Code)
	}
	if rec := driftReq(NewDriftHandler(&fakeFleet{}, &fakeActive{body: []byte("{{{bad")})); rec.Code != http.StatusInternalServerError {
		t.Errorf("unresolvable active = %d, want 500", rec.Code)
	}
}
