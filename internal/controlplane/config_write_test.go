package controlplane

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// fakeStore is an in-memory configStore for handler unit tests.
type fakeStore struct {
	entities  map[string]configdb.Entity
	order     []string
	published []configdb.Version
	activeID  string
	changes   []configdb.Change

	errList     error
	errGet      error
	errUpsert   error
	errDelete   error
	errPublish  error
	errActive   error
	errActivate error
	errVersions error
}

func newFakeStore() *fakeStore {
	return &fakeStore{entities: map[string]configdb.Entity{}}
}

func fkey(kind, name string) string { return kind + "/" + name }

func (f *fakeStore) UpsertEntity(_ context.Context, kind, name string, body []byte, by string) error {
	if f.errUpsert != nil {
		return f.errUpsert
	}
	k := fkey(kind, name)
	if _, ok := f.entities[k]; !ok {
		f.order = append(f.order, k)
	}
	f.entities[k] = configdb.Entity{Kind: kind, Name: name, Body: append([]byte(nil), body...), UpdatedBy: by}
	f.changes = append([]configdb.Change{{Kind: kind, Name: name, Op: "update", ChangedBy: by}}, f.changes...)
	return nil
}

func (f *fakeStore) GetEntity(_ context.Context, kind, name string) (configdb.Entity, error) {
	if f.errGet != nil {
		return configdb.Entity{}, f.errGet
	}
	e, ok := f.entities[fkey(kind, name)]
	if !ok {
		return configdb.Entity{}, configdb.ErrEntityNotFound
	}
	return e, nil
}

func (f *fakeStore) ListEntities(context.Context) ([]configdb.Entity, error) {
	if f.errList != nil {
		return nil, f.errList
	}
	out := make([]configdb.Entity, 0, len(f.order))
	for _, k := range f.order {
		out = append(out, f.entities[k])
	}
	return out, nil
}

func (f *fakeStore) DeleteEntity(_ context.Context, kind, name, _ string) error {
	if f.errDelete != nil {
		return f.errDelete
	}
	k := fkey(kind, name)
	if _, ok := f.entities[k]; !ok {
		return configdb.ErrEntityNotFound
	}
	delete(f.entities, k)
	return nil
}

func (f *fakeStore) ListChanges(_ context.Context, limit int) ([]configdb.Change, error) {
	if limit > 0 && limit < len(f.changes) {
		return f.changes[:limit], nil
	}
	return f.changes, nil
}

func (f *fakeStore) Publish(_ context.Context, body []byte, by string) (configdb.Version, error) {
	if f.errPublish != nil {
		return configdb.Version{}, f.errPublish
	}
	v := configdb.Version{ID: fmt.Sprintf("v%d", len(f.published)+1), Hash: "hash", Body: append([]byte(nil), body...), PublishedBy: by}
	f.published = append(f.published, v)
	f.activeID = v.ID
	return v, nil
}

func (f *fakeStore) ActiveVersion(context.Context) (configdb.Version, error) {
	if f.errActive != nil {
		return configdb.Version{}, f.errActive
	}
	for _, v := range f.published {
		if v.ID == f.activeID {
			return v, nil
		}
	}
	return configdb.Version{}, configdb.ErrNoActiveConfig
}

func (f *fakeStore) ActivateVersion(_ context.Context, id string) error {
	if f.errActivate != nil {
		return f.errActivate
	}
	for _, v := range f.published {
		if v.ID == id {
			f.activeID = id
			return nil
		}
	}
	return configdb.ErrVersionNotFound
}

func (f *fakeStore) ListVersions(context.Context) ([]configdb.VersionMeta, error) {
	if f.errVersions != nil {
		return nil, f.errVersions
	}
	out := make([]configdb.VersionMeta, 0, len(f.published))
	for _, v := range f.published {
		out = append(out, configdb.VersionMeta{ID: v.ID, Hash: v.Hash, Active: v.ID == f.activeID})
	}
	return out, nil
}

func newAdmin(t *testing.T, store configStore) (*ConfigAdminHandler, *config.Store) {
	t.Helper()
	live := config.NewStore(&config.ResolvedConfigV2{})
	return NewConfigAdminHandler(store, live, nil), live
}

func do(h *ConfigAdminHandler, method, path, body string) *httptest.ResponseRecorder {
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

// seedConfigDevEntities loads config-dev into the fake so publish composes a
// valid config.
func seedConfigDevEntities(t *testing.T, f *fakeStore) {
	t.Helper()
	resolved, err := config.LoadV2(context.Background(), "../../config-dev")
	if err != nil {
		t.Skipf("config-dev not loadable: %v", err)
	}
	entities, err := EntityFromConfig(resolved)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entities {
		_ = f.UpsertEntity(context.Background(), e.Kind, e.Name, e.Body, "seed")
	}
}

func TestConfigAdmin_EntityCRUD(t *testing.T) {
	h, _ := newAdmin(t, newFakeStore())

	// Create.
	if rec := do(h, http.MethodPut, "/api/v1/config/entities/backend/openai", `{"base_url":"https://api.openai.com"}`); rec.Code != http.StatusOK {
		t.Fatalf("put = %d: %s", rec.Code, rec.Body)
	}
	// Get.
	if rec := do(h, http.MethodGet, "/api/v1/config/entities/backend/openai", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "api.openai.com") {
		t.Fatalf("get = %d: %s", rec.Code, rec.Body)
	}
	// Get missing.
	if rec := do(h, http.MethodGet, "/api/v1/config/entities/backend/ghost", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("get missing = %d", rec.Code)
	}
	// List.
	if rec := do(h, http.MethodGet, "/api/v1/config/entities", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "openai") {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body)
	}
	// Delete.
	if rec := do(h, http.MethodDelete, "/api/v1/config/entities/backend/openai", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	// Delete missing.
	if rec := do(h, http.MethodDelete, "/api/v1/config/entities/backend/ghost", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing = %d", rec.Code)
	}
}

func TestConfigAdmin_PutValidation(t *testing.T) {
	h, _ := newAdmin(t, newFakeStore())

	if rec := do(h, http.MethodPut, "/api/v1/config/entities/backend/x", `{{{not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodPut, "/api/v1/config/entities/bogus/x", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind = %d, want 400", rec.Code)
	}
	big := `{"base_url":"` + strings.Repeat("a", maxEntityBodyBytes+10) + `"}`
	if rec := do(h, http.MethodPut, "/api/v1/config/entities/backend/x", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large = %d, want 413", rec.Code)
	}
}

func TestConfigAdmin_PublishValidatesAndReloads(t *testing.T) {
	f := newFakeStore()
	seedConfigDevEntities(t, f)
	h, live := newAdmin(t, f)

	rec := do(h, http.MethodPost, "/api/v1/config/publish", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "published") {
		t.Fatalf("publish = %d: %s", rec.Code, rec.Body)
	}
	// Live-reloaded: the served store now holds config-dev's configurations.
	if snap := live.Snapshot(); snap == nil || len(snap.Configurations) == 0 {
		t.Fatal("publish did not live-reload the served config")
	}
}

func TestConfigAdmin_PublishRejectsInvalidComposite(t *testing.T) {
	f := newFakeStore()
	// A configuration whose binding references a backend that doesn't exist —
	// composes structurally but fails validation.
	_ = f.UpsertEntity(context.Background(), KindConfiguration, "bad",
		[]byte(`{"bindings":[{"protocol":"chat","models":["x"],"backend":"ghost"}]}`), "t")
	h, _ := newAdmin(t, f)

	rec := do(h, http.MethodPost, "/api/v1/config/publish", "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid") {
		t.Fatalf("publish invalid = %d: %s", rec.Code, rec.Body)
	}
}

func TestConfigAdmin_VersionsAndRollback(t *testing.T) {
	f := newFakeStore()
	seedConfigDevEntities(t, f)
	h, _ := newAdmin(t, f)

	do(h, http.MethodPost, "/api/v1/config/publish", "")
	do(h, http.MethodPost, "/api/v1/config/publish", "") // dedup-less fake: second version

	if rec := do(h, http.MethodGet, "/api/v1/config/versions", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "v1") {
		t.Fatalf("versions = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "/api/v1/config/versions/v1/activate", ""); rec.Code != http.StatusOK {
		t.Fatalf("activate = %d: %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "/api/v1/config/versions/ghost/activate", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("activate ghost = %d, want 404", rec.Code)
	}
}

func TestConfigAdmin_Changes(t *testing.T) {
	f := newFakeStore()
	_ = f.UpsertEntity(context.Background(), KindBackend, "a", []byte("{}"), "t")
	_ = f.UpsertEntity(context.Background(), KindBackend, "b", []byte("{}"), "t")
	h, _ := newAdmin(t, f)

	if rec := do(h, http.MethodGet, "/api/v1/config/changes", ""); rec.Code != http.StatusOK {
		t.Fatalf("changes = %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/v1/config/changes?limit=1", ""); rec.Code != http.StatusOK {
		t.Fatalf("changes limit = %d", rec.Code)
	}
}

func TestConfigAdmin_ErrorMapping(t *testing.T) {
	boom := errors.New("boom")

	t.Run("list 500", func(t *testing.T) {
		h, _ := newAdmin(t, &fakeStore{entities: map[string]configdb.Entity{}, errList: boom})
		if rec := do(h, http.MethodGet, "/api/v1/config/entities", ""); rec.Code != http.StatusInternalServerError {
			t.Fatalf("= %d, want 500", rec.Code)
		}
	})
	t.Run("get 500", func(t *testing.T) {
		h, _ := newAdmin(t, &fakeStore{entities: map[string]configdb.Entity{}, errGet: boom})
		if rec := do(h, http.MethodGet, "/api/v1/config/entities/backend/x", ""); rec.Code != http.StatusInternalServerError {
			t.Fatalf("= %d, want 500", rec.Code)
		}
	})
	t.Run("upsert 500", func(t *testing.T) {
		h, _ := newAdmin(t, &fakeStore{entities: map[string]configdb.Entity{}, errUpsert: boom})
		if rec := do(h, http.MethodPut, "/api/v1/config/entities/backend/x", `{"base_url":"u"}`); rec.Code != http.StatusInternalServerError {
			t.Fatalf("= %d, want 500", rec.Code)
		}
	})
	t.Run("delete 500", func(t *testing.T) {
		f := &fakeStore{entities: map[string]configdb.Entity{"backend/x": {Kind: "backend", Name: "x"}}, errDelete: boom}
		h, _ := newAdmin(t, f)
		if rec := do(h, http.MethodDelete, "/api/v1/config/entities/backend/x", ""); rec.Code != http.StatusInternalServerError {
			t.Fatalf("= %d, want 500", rec.Code)
		}
	})
	t.Run("publish store 500", func(t *testing.T) {
		f := newFakeStore()
		seedConfigDevEntities(t, f)
		f.errPublish = boom
		h, _ := newAdmin(t, f)
		if rec := do(h, http.MethodPost, "/api/v1/config/publish", ""); rec.Code != http.StatusInternalServerError {
			t.Fatalf("= %d, want 500", rec.Code)
		}
	})
}

func TestConfigAdmin_NilLiveStoreSkipsReload(t *testing.T) {
	f := newFakeStore()
	seedConfigDevEntities(t, f)
	h := NewConfigAdminHandler(f, nil, nil) // nil liveStore
	if rec := do(h, http.MethodPost, "/api/v1/config/publish", ""); rec.Code != http.StatusOK {
		t.Fatalf("publish with nil liveStore = %d: %s", rec.Code, rec.Body)
	}
}

func TestConfigAdmin_MoreErrorPaths(t *testing.T) {
	boom := errors.New("boom")

	t.Run("versions 500", func(t *testing.T) {
		h, _ := newAdmin(t, &fakeStore{entities: map[string]configdb.Entity{}, errVersions: boom})
		if rec := do(h, http.MethodGet, "/api/v1/config/versions", ""); rec.Code != http.StatusInternalServerError {
			t.Fatalf("= %d, want 500", rec.Code)
		}
	})
	t.Run("activate 500", func(t *testing.T) {
		h, _ := newAdmin(t, &fakeStore{entities: map[string]configdb.Entity{}, errActivate: boom})
		if rec := do(h, http.MethodPost, "/api/v1/config/versions/v1/activate", ""); rec.Code != http.StatusInternalServerError {
			t.Fatalf("= %d, want 500", rec.Code)
		}
	})
	t.Run("publish list 500", func(t *testing.T) {
		h, _ := newAdmin(t, &fakeStore{entities: map[string]configdb.Entity{}, errList: boom})
		if rec := do(h, http.MethodPost, "/api/v1/config/publish", ""); rec.Code != http.StatusInternalServerError {
			t.Fatalf("= %d, want 500", rec.Code)
		}
	})
	t.Run("publish compose 400", func(t *testing.T) {
		f := newFakeStore()
		// A stored entity with a malformed body fails composition.
		_ = f.UpsertEntity(context.Background(), KindBackend, "x", []byte("{{{"), "t")
		h, _ := newAdmin(t, f)
		if rec := do(h, http.MethodPost, "/api/v1/config/publish", ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("= %d, want 400", rec.Code)
		}
	})
	t.Run("put read error 400", func(t *testing.T) {
		h, _ := newAdmin(t, newFakeStore())
		req := httptest.NewRequest(http.MethodPut, "/api/v1/config/entities/backend/x", errReader{})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("= %d, want 400", rec.Code)
		}
	})
}

func TestConfigAdmin_ActorHeaderAndReloadLogging(t *testing.T) {
	// X-Sluice-Actor header drives the actor branch.
	h, _ := newAdmin(t, newFakeStore())
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/entities/backend/x", strings.NewReader(`{"base_url":"u"}`))
	req.Header.Set("X-Sluice-Actor", "alice")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put with actor = %d", rec.Code)
	}

	// A non-nil logger + a reload that can't read the active version exercises
	// the reload error path and logWarn body — publish still succeeds.
	f := newFakeStore()
	seedConfigDevEntities(t, f)
	f.errActive = errors.New("boom")
	logged := NewConfigAdminHandler(f, config.NewStore(&config.ResolvedConfigV2{}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if rec := do(logged, http.MethodPost, "/api/v1/config/publish", ""); rec.Code != http.StatusOK {
		t.Fatalf("publish (reload fails) = %d: %s", rec.Code, rec.Body)
	}
}
