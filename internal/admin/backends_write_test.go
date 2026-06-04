package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

const (
	beFixtureBackends = `backends:
  openai:
    base_url: https://api.openai.com
    protocols:
      chat:
        path: /v1/chat/completions
  spare:
    base_url: https://spare.example.com
    protocols:
      chat:
        path: /v1/chat/completions
`
	beFixturePolicy = `configurations:
  prod:
    credentials:
      openai: sk
    bindings:
      - protocol: chat
        models: ["gpt-*"]
        backend: openai
api_keys:
  - secret: sk_live_x
    name: k
    configuration: prod
    enabled: true
`
)

// newBackendsFixture writes a valid two-backend config to a temp dir and
// returns a store loaded from it plus the dir (for write handlers).
func newBackendsFixture(t *testing.T) (*config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"backends.yaml": beFixtureBackends,
		"policy.yaml":   beFixturePolicy,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	rc, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return config.NewStore(rc), dir
}

func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, http.NoBody)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestBackendsCreate(t *testing.T) {
	store, dir := newBackendsFixture(t)
	h := BackendsCreateHandler(store, dir)

	body := `{"name":"newbe","base_url":"https://new.example.com","protocols":{"chat":{"path":"/v1/chat/completions"}}}`
	rec := do(t, h, http.MethodPost, "/api/v1/config/backends", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body=%s)", rec.Code, rec.Body)
	}
	// Persisted to disk + swapped into the store.
	if _, ok := store.Snapshot().Backends["newbe"]; !ok {
		t.Errorf("new backend not in store snapshot")
	}
	reloaded, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Backends["newbe"].BaseURL != "https://new.example.com" {
		t.Errorf("new backend not persisted to disk")
	}
	// It must land in backends.yaml (where the other backends live), not policy.yaml.
	if reloaded.SourceFiles["backends"] != "backends.yaml" {
		t.Errorf("backends block not in backends.yaml: %v", reloaded.SourceFiles)
	}
}

func TestBackendsCreate_Conflict(t *testing.T) {
	store, dir := newBackendsFixture(t)
	h := BackendsCreateHandler(store, dir)
	rec := do(t, h, http.MethodPost, "/api/v1/config/backends",
		`{"name":"openai","base_url":"https://x","protocols":{"chat":{}}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestBackendsCreate_BadRequest(t *testing.T) {
	store, dir := newBackendsFixture(t)
	h := BackendsCreateHandler(store, dir)

	if rec := do(t, h, http.MethodPost, "/api/v1/config/backends", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("empty body status = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/backends", `{"base_url":"https://x","protocols":{"chat":{}}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing name status = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/backends", `{not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json status = %d, want 400", rec.Code)
	}
}

func TestBackendsCreate_Invalid422(t *testing.T) {
	store, dir := newBackendsFixture(t)
	h := BackendsCreateHandler(store, dir)
	// No protocols and no passthrough -> validateBackends rejects.
	rec := do(t, h, http.MethodPost, "/api/v1/config/backends",
		`{"name":"empty","base_url":"https://x"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body=%s)", rec.Code, rec.Body)
	}
}

func TestBackendsCreate_DryRun(t *testing.T) {
	store, dir := newBackendsFixture(t)
	h := BackendsCreateHandler(store, dir)

	rec := do(t, h, http.MethodPost, "/api/v1/config/backends?dry_run=true",
		`{"name":"dry","base_url":"https://x","protocols":{"chat":{}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want 200", rec.Code)
	}
	var pr PreviewResult
	if err := json.Unmarshal(rec.Body.Bytes(), &pr); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if !pr.Valid {
		t.Errorf("dry-run should be valid, got %+v", pr)
	}
	if _, ok := store.Snapshot().Backends["dry"]; ok {
		t.Errorf("dry-run must not persist the backend")
	}

	// An invalid candidate reports invalid without persisting.
	bad := do(t, h, http.MethodPost, "/api/v1/config/backends?dry_run=true",
		`{"name":"bad","base_url":"https://x"}`)
	var badPR PreviewResult
	_ = json.Unmarshal(bad.Body.Bytes(), &badPR)
	if badPR.Valid || badPR.Error == "" {
		t.Errorf("invalid dry-run should report invalid, got %+v", badPR)
	}
}

func TestBackendsReplace(t *testing.T) {
	store, dir := newBackendsFixture(t)
	h := BackendsReplaceHandler(store, dir)

	rec := do(t, h, http.MethodPut, "/api/v1/config/backends/spare",
		`{"base_url":"https://moved.example.com","protocols":{"chat":{"path":"/v1/chat/completions"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body)
	}
	if store.Snapshot().Backends["spare"].BaseURL != "https://moved.example.com" {
		t.Errorf("replace not applied")
	}
}

func TestBackendsReplace_NotFound(t *testing.T) {
	store, dir := newBackendsFixture(t)
	h := BackendsReplaceHandler(store, dir)
	rec := do(t, h, http.MethodPut, "/api/v1/config/backends/ghost",
		`{"base_url":"https://x","protocols":{"chat":{}}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestBackendsReplace_RenameRejected(t *testing.T) {
	store, dir := newBackendsFixture(t)
	h := BackendsReplaceHandler(store, dir)
	rec := do(t, h, http.MethodPut, "/api/v1/config/backends/spare",
		`{"name":"renamed","base_url":"https://x","protocols":{"chat":{}}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestBackendsReplace_DryRun(t *testing.T) {
	store, dir := newBackendsFixture(t)
	h := BackendsReplaceHandler(store, dir)
	rec := do(t, h, http.MethodPut, "/api/v1/config/backends/spare?dry_run=true",
		`{"base_url":"https://preview.example.com","protocols":{"chat":{}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want 200", rec.Code)
	}
	if store.Snapshot().Backends["spare"].BaseURL != "https://spare.example.com" {
		t.Errorf("dry-run replace must not mutate the store")
	}
}

func TestBackendsReplace_EmptyName(t *testing.T) {
	store, dir := newBackendsFixture(t)
	h := BackendsReplaceHandler(store, dir)
	if rec := do(t, h, http.MethodPut, "/api/v1/config/backends/", `{"protocols":{"chat":{}}}`); rec.Code != http.StatusNotFound {
		t.Errorf("empty name status = %d, want 404", rec.Code)
	}
}

func TestBackendsReplace_Disabled(t *testing.T) {
	store, _ := newBackendsFixture(t)
	h := BackendsReplaceHandler(store, "")
	if rec := do(t, h, http.MethodPut, "/api/v1/config/backends/spare", `{"protocols":{"chat":{}}}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestBackendsDelete_EmptyNameAndDisabled(t *testing.T) {
	store, dir := newBackendsFixture(t)
	if rec := do(t, BackendsDeleteHandler(store, dir), http.MethodDelete, "/api/v1/config/backends/", ""); rec.Code != http.StatusNotFound {
		t.Errorf("empty name status = %d, want 404", rec.Code)
	}
	if rec := do(t, BackendsDeleteHandler(store, ""), http.MethodDelete, "/api/v1/config/backends/spare", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("disabled status = %d, want 503", rec.Code)
	}
}

func TestBackendsDelete(t *testing.T) {
	store, dir := newBackendsFixture(t)
	h := BackendsDeleteHandler(store, dir)

	// "spare" is unreferenced -> deletable.
	rec := do(t, h, http.MethodDelete, "/api/v1/config/backends/spare", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body)
	}
	if _, ok := store.Snapshot().Backends["spare"]; ok {
		t.Errorf("backend not deleted from store")
	}
	reloaded, _ := config.Load(context.Background(), dir)
	if _, ok := reloaded.Backends["spare"]; ok {
		t.Errorf("backend not deleted from disk")
	}
}

func TestBackendsDelete_Referenced(t *testing.T) {
	store, dir := newBackendsFixture(t)
	h := BackendsDeleteHandler(store, dir)

	// "openai" is referenced by a binding + a credential -> 409 with referrers.
	rec := do(t, h, http.MethodDelete, "/api/v1/config/backends/openai", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body)
	}
	var ce ConflictError
	if err := json.Unmarshal(rec.Body.Bytes(), &ce); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if len(ce.UsedBy) == 0 {
		t.Errorf("conflict should name referrers, got %+v", ce)
	}
}

func TestBackendsDelete_NotFound(t *testing.T) {
	store, dir := newBackendsFixture(t)
	h := BackendsDeleteHandler(store, dir)
	if rec := do(t, h, http.MethodDelete, "/api/v1/config/backends/ghost", ""); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestBackendsWrite_Disabled(t *testing.T) {
	store, _ := newBackendsFixture(t)
	// No config dir -> write surface disabled -> 503.
	h := BackendsCreateHandler(store, "")
	rec := do(t, h, http.MethodPost, "/api/v1/config/backends",
		`{"name":"x","base_url":"https://x","protocols":{"chat":{}}}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
