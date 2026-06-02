//go:build e2e

package configdb_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

func adminReq(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, http.NoBody)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// TestConfigAdmin_EndToEndWithPostgres drives the config write API against real
// Postgres: seed entities, publish (which validates + live-reloads the served
// store), then CRUD an entity and read the version history through the handler.
func TestConfigAdmin_EndToEndWithPostgres(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	resolved, err := config.LoadV2(ctx, "../../../config-dev")
	if err != nil {
		t.Skipf("config-dev not loadable: %v", err)
	}
	entities, err := controlplane.EntityFromConfig(resolved)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entities {
		if err := db.UpsertEntity(ctx, e.Kind, e.Name, e.Body, "seed"); err != nil {
			t.Fatalf("seed upsert: %v", err)
		}
	}

	live := config.NewStore(&config.ResolvedConfigV2{})
	h := controlplane.NewConfigAdminHandler(db, live, nil)

	// Publish: composes + validates + activates + live-reloads.
	if rec := adminReq(h, http.MethodPost, "/api/v1/config/publish", ""); rec.Code != http.StatusOK {
		t.Fatalf("publish = %d: %s", rec.Code, rec.Body)
	}
	if snap := live.Snapshot(); snap == nil || len(snap.Configurations) == 0 {
		t.Fatal("publish did not live-reload the served store")
	}

	// CRUD a fresh entity through the handler.
	if rec := adminReq(h, http.MethodPut, "/api/v1/config/entities/backend/extra", `{"base_url":"http://extra:11434"}`); rec.Code != http.StatusOK {
		t.Fatalf("put = %d: %s", rec.Code, rec.Body)
	}
	if rec := adminReq(h, http.MethodGet, "/api/v1/config/entities/backend/extra", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "extra:11434") {
		t.Fatalf("get = %d: %s", rec.Code, rec.Body)
	}
	if rec := adminReq(h, http.MethodDelete, "/api/v1/config/entities/backend/extra", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}

	// Version history + audit are queryable.
	if rec := adminReq(h, http.MethodGet, "/api/v1/config/versions", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"active":true`) {
		t.Fatalf("versions = %d: %s", rec.Code, rec.Body)
	}
	if rec := adminReq(h, http.MethodGet, "/api/v1/config/changes?limit=5", ""); rec.Code != http.StatusOK {
		t.Fatalf("changes = %d", rec.Code)
	}
}
