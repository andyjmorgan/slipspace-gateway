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

	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

const (
	beFixtureProviders = `providers:
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
        provider: openai
api_keys:
  - secret: sk_live_x
    name: k
    configuration: prod
    enabled: true
`
)

// newProvidersFixture writes a valid two-provider config to a temp dir and
// returns a store loaded from it plus the dir (for write handlers).
func newProvidersFixture(t *testing.T) (*config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"providers.yaml": beFixtureProviders,
		"policy.yaml":    beFixturePolicy,
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

func TestProvidersCreate(t *testing.T) {
	store, dir := newProvidersFixture(t)
	h := ProvidersCreateHandler(store, dir)

	body := `{"name":"newbe","base_url":"https://new.example.com","protocols":{"chat":{"path":"/v1/chat/completions"}}}`
	rec := do(t, h, http.MethodPost, "/api/v1/config/providers", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body=%s)", rec.Code, rec.Body)
	}
	// Persisted to disk + swapped into the store.
	if _, ok := store.Snapshot().Providers["newbe"]; !ok {
		t.Errorf("new provider not in store snapshot")
	}
	reloaded, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Providers["newbe"].BaseURL != "https://new.example.com" {
		t.Errorf("new provider not persisted to disk")
	}
	// It must land in providers.yaml (where the other providers live), not policy.yaml.
	if reloaded.SourceFiles["providers"] != "providers.yaml" {
		t.Errorf("providers block not in providers.yaml: %v", reloaded.SourceFiles)
	}
}

func TestProvidersCreate_Conflict(t *testing.T) {
	store, dir := newProvidersFixture(t)
	h := ProvidersCreateHandler(store, dir)
	rec := do(t, h, http.MethodPost, "/api/v1/config/providers",
		`{"name":"openai","base_url":"https://x","protocols":{"chat":{}}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestProvidersCreate_BadRequest(t *testing.T) {
	store, dir := newProvidersFixture(t)
	h := ProvidersCreateHandler(store, dir)

	if rec := do(t, h, http.MethodPost, "/api/v1/config/providers", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("empty body status = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/providers", `{"base_url":"https://x","protocols":{"chat":{}}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing name status = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/providers", `{not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json status = %d, want 400", rec.Code)
	}
}

func TestProvidersCreate_Invalid422(t *testing.T) {
	store, dir := newProvidersFixture(t)
	h := ProvidersCreateHandler(store, dir)
	// No protocols and no passthrough -> validateProviders rejects.
	rec := do(t, h, http.MethodPost, "/api/v1/config/providers",
		`{"name":"empty","base_url":"https://x"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body=%s)", rec.Code, rec.Body)
	}
}

func TestProvidersCreate_DryRun(t *testing.T) {
	store, dir := newProvidersFixture(t)
	h := ProvidersCreateHandler(store, dir)

	rec := do(t, h, http.MethodPost, "/api/v1/config/providers?dry_run=true",
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
	if _, ok := store.Snapshot().Providers["dry"]; ok {
		t.Errorf("dry-run must not persist the provider")
	}

	// An invalid candidate reports invalid without persisting.
	bad := do(t, h, http.MethodPost, "/api/v1/config/providers?dry_run=true",
		`{"name":"bad","base_url":"https://x"}`)
	var badPR PreviewResult
	_ = json.Unmarshal(bad.Body.Bytes(), &badPR)
	if badPR.Valid || badPR.Error == "" {
		t.Errorf("invalid dry-run should report invalid, got %+v", badPR)
	}
}

func TestProvidersReplace(t *testing.T) {
	store, dir := newProvidersFixture(t)
	h := ProvidersReplaceHandler(store, dir)

	rec := do(t, h, http.MethodPut, "/api/v1/config/providers/spare",
		`{"base_url":"https://moved.example.com","protocols":{"chat":{"path":"/v1/chat/completions"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body)
	}
	if store.Snapshot().Providers["spare"].BaseURL != "https://moved.example.com" {
		t.Errorf("replace not applied")
	}
}

func TestProvidersReplace_NotFound(t *testing.T) {
	store, dir := newProvidersFixture(t)
	h := ProvidersReplaceHandler(store, dir)
	rec := do(t, h, http.MethodPut, "/api/v1/config/providers/ghost",
		`{"base_url":"https://x","protocols":{"chat":{}}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestProvidersReplace_RenameRejected(t *testing.T) {
	store, dir := newProvidersFixture(t)
	h := ProvidersReplaceHandler(store, dir)
	rec := do(t, h, http.MethodPut, "/api/v1/config/providers/spare",
		`{"name":"renamed","base_url":"https://x","protocols":{"chat":{}}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestProvidersReplace_DryRun(t *testing.T) {
	store, dir := newProvidersFixture(t)
	h := ProvidersReplaceHandler(store, dir)
	rec := do(t, h, http.MethodPut, "/api/v1/config/providers/spare?dry_run=true",
		`{"base_url":"https://preview.example.com","protocols":{"chat":{}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want 200", rec.Code)
	}
	if store.Snapshot().Providers["spare"].BaseURL != "https://spare.example.com" {
		t.Errorf("dry-run replace must not mutate the store")
	}
}

func TestProvidersReplace_EmptyName(t *testing.T) {
	store, dir := newProvidersFixture(t)
	h := ProvidersReplaceHandler(store, dir)
	if rec := do(t, h, http.MethodPut, "/api/v1/config/providers/", `{"protocols":{"chat":{}}}`); rec.Code != http.StatusNotFound {
		t.Errorf("empty name status = %d, want 404", rec.Code)
	}
}

func TestProvidersReplace_Disabled(t *testing.T) {
	store, _ := newProvidersFixture(t)
	h := ProvidersReplaceHandler(store, "")
	if rec := do(t, h, http.MethodPut, "/api/v1/config/providers/spare", `{"protocols":{"chat":{}}}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestProvidersDelete_EmptyNameAndDisabled(t *testing.T) {
	store, dir := newProvidersFixture(t)
	if rec := do(t, ProvidersDeleteHandler(store, dir), http.MethodDelete, "/api/v1/config/providers/", ""); rec.Code != http.StatusNotFound {
		t.Errorf("empty name status = %d, want 404", rec.Code)
	}
	if rec := do(t, ProvidersDeleteHandler(store, ""), http.MethodDelete, "/api/v1/config/providers/spare", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("disabled status = %d, want 503", rec.Code)
	}
}

func TestProvidersDelete(t *testing.T) {
	store, dir := newProvidersFixture(t)
	h := ProvidersDeleteHandler(store, dir)

	// "spare" is unreferenced -> deletable.
	rec := do(t, h, http.MethodDelete, "/api/v1/config/providers/spare", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body)
	}
	if _, ok := store.Snapshot().Providers["spare"]; ok {
		t.Errorf("provider not deleted from store")
	}
	reloaded, _ := config.Load(context.Background(), dir)
	if _, ok := reloaded.Providers["spare"]; ok {
		t.Errorf("provider not deleted from disk")
	}
}

func TestProvidersDelete_Referenced(t *testing.T) {
	store, dir := newProvidersFixture(t)
	h := ProvidersDeleteHandler(store, dir)

	// "openai" is referenced by a binding + a credential -> 409 with referrers.
	rec := do(t, h, http.MethodDelete, "/api/v1/config/providers/openai", "")
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

func TestProvidersDelete_NotFound(t *testing.T) {
	store, dir := newProvidersFixture(t)
	h := ProvidersDeleteHandler(store, dir)
	if rec := do(t, h, http.MethodDelete, "/api/v1/config/providers/ghost", ""); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestProvidersWrite_Disabled(t *testing.T) {
	store, _ := newProvidersFixture(t)
	// No config dir -> write surface disabled -> 503.
	h := ProvidersCreateHandler(store, "")
	rec := do(t, h, http.MethodPost, "/api/v1/config/providers",
		`{"name":"x","base_url":"https://x","protocols":{"chat":{}}}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
