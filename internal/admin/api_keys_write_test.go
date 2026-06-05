package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

const (
	akFixtureProviders = `providers:
  openai:
    base_url: https://api.openai.com
    protocols:
      chat:
        path: /v1/chat/completions
`
	akFixturePolicy = `configurations:
  prod:
    credentials:
      openai: sk
    bindings:
      - protocol: chat
        models: ["gpt-*"]
        provider: openai
api_keys:
  - secret: sk_live_existing
    name: k1
    configuration: prod
    enabled: true
`
)

func newAPIKeysFixture(t *testing.T) (*config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"providers.yaml": akFixtureProviders,
		"policy.yaml":    akFixturePolicy,
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

func keyByName(store *config.Store, name string) (contractsAPIKey, bool) {
	for _, k := range store.Snapshot().APIKeys {
		if k.Name == name {
			return contractsAPIKey{Secret: k.Secret, Name: k.Name, Configuration: k.Configuration, Enabled: k.Enabled}, true
		}
	}
	return contractsAPIKey{}, false
}

type contractsAPIKey struct {
	Secret        string
	Name          string
	Configuration string
	Enabled       bool
}

func TestAPIKeysList_Detail_Redacted(t *testing.T) {
	store, _ := newAPIKeysFixture(t)
	rec := do(t, APIKeysListHandler(store), http.MethodGet, "/api/v1/config/api-keys", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "sk_live_existing") {
		t.Errorf("list leaked the plaintext secret: %s", rec.Body)
	}
	var list []APIKeyListItem
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Name != "k1" || list[0].Configuration != "prod" {
		t.Errorf("list = %+v", list)
	}

	d := do(t, APIKeyDetailHandler(store), http.MethodGet, "/api/v1/config/api-keys/k1", "")
	if d.Code != http.StatusOK || strings.Contains(d.Body.String(), "sk_live_existing") {
		t.Errorf("detail leaked or failed: %d %s", d.Code, d.Body)
	}
	if rec := do(t, APIKeyDetailHandler(store), http.MethodGet, "/api/v1/config/api-keys/ghost", ""); rec.Code != http.StatusNotFound {
		t.Errorf("detail miss = %d, want 404", rec.Code)
	}
}

func TestAPIKeysRead_Unavailable(t *testing.T) {
	if rec := do(t, APIKeysListHandler(nil), http.MethodGet, "/api/v1/config/api-keys", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("list unavailable = %d, want 503", rec.Code)
	}
	if rec := do(t, APIKeyDetailHandler(nil), http.MethodGet, "/api/v1/config/api-keys/x", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("detail unavailable = %d, want 503", rec.Code)
	}
}

func TestAPIKeysCreate_MintAndReveal(t *testing.T) {
	store, dir := newAPIKeysFixture(t)
	rec := do(t, APIKeysCreateHandler(store, dir), http.MethodPost, "/api/v1/config/api-keys",
		`{"name":"svc-a","configuration":"prod"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body)
	}
	var reveal APIKeyReveal
	if err := json.Unmarshal(rec.Body.Bytes(), &reveal); err != nil {
		t.Fatalf("decode reveal: %v", err)
	}
	if !strings.HasPrefix(reveal.Secret, "sk_live_") || len(reveal.Secret) < 16 {
		t.Errorf("minted secret looks wrong (len=%d)", len(reveal.Secret))
	}
	if !reveal.Enabled {
		t.Errorf("new key should default to enabled")
	}
	stored, ok := keyByName(store, "svc-a")
	if !ok || stored.Secret != reveal.Secret {
		t.Errorf("minted secret not stored")
	}
	reloaded, _ := config.Load(context.Background(), dir)
	if _, found := apiKeyIndexByRef(reloaded.APIKeys, "svc-a"); !found {
		t.Errorf("key not persisted to disk")
	}
}

func TestAPIKeys_AddressByMintedUUID(t *testing.T) {
	store, dir := newAPIKeysFixture(t)
	// Mint a key, then look up its id from the list and address it by UUID.
	do(t, APIKeysCreateHandler(store, dir), http.MethodPost, "/api/v1/config/api-keys", `{"name":"svc-a","configuration":"prod"}`)

	listRec := do(t, APIKeysListHandler(store), http.MethodGet, "/api/v1/config/api-keys", "")
	var list []APIKeyListItem
	_ = json.Unmarshal(listRec.Body.Bytes(), &list)
	var id string
	for _, it := range list {
		if it.Name == "svc-a" && it.ID != nil {
			id = it.ID.String()
		}
	}
	if id == "" {
		t.Fatalf("minted key has no id in the list: %+v", list)
	}

	// GET by UUID resolves the key.
	if rec := do(t, APIKeyDetailHandler(store), http.MethodGet, "/api/v1/config/api-keys/"+id, ""); rec.Code != http.StatusOK {
		t.Errorf("detail by uuid = %d, want 200", rec.Code)
	}
	// PATCH by UUID disables it.
	if rec := do(t, APIKeysPatchHandler(store, dir), http.MethodPatch, "/api/v1/config/api-keys/"+id, `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Errorf("patch by uuid = %d, want 200", rec.Code)
	}
	if k, _ := keyByName(store, "svc-a"); k.Enabled {
		t.Errorf("patch-by-uuid did not disable the key")
	}
	// DELETE by UUID removes it.
	if rec := do(t, APIKeysDeleteHandler(store, dir), http.MethodDelete, "/api/v1/config/api-keys/"+id, ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete by uuid = %d, want 204", rec.Code)
	}
}

func TestAPIKeysCreate_Errors(t *testing.T) {
	store, dir := newAPIKeysFixture(t)
	h := APIKeysCreateHandler(store, dir)
	if rec := do(t, h, http.MethodPost, "/api/v1/config/api-keys", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("empty = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/api-keys", `{bad`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/api-keys", `{"configuration":"prod"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing name = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/api-keys", `{"name":"k1","configuration":"prod"}`); rec.Code != http.StatusConflict {
		t.Errorf("dup name = %d, want 409", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/api-keys", `{"name":"k2","configuration":"ghost"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown config = %d, want 422", rec.Code)
	}
	big := `{"name":"big","configuration":"` + strings.Repeat("a", 300*1024) + `"}`
	if rec := do(t, h, http.MethodPost, "/api/v1/config/api-keys", big); rec.Code != http.StatusBadRequest {
		t.Errorf("oversized = %d, want 400", rec.Code)
	}
}

func TestAPIKeysCreate_DisabledAndDryRun(t *testing.T) {
	store, dir := newAPIKeysFixture(t)
	h := APIKeysCreateHandler(store, dir)

	rec := do(t, h, http.MethodPost, "/api/v1/config/api-keys", `{"name":"off","configuration":"prod","enabled":false}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if k, _ := keyByName(store, "off"); k.Enabled {
		t.Errorf("key should be created disabled")
	}

	dry := do(t, h, http.MethodPost, "/api/v1/config/api-keys?dry_run=true", `{"name":"dry","configuration":"prod"}`)
	var pr PreviewResult
	_ = json.Unmarshal(dry.Body.Bytes(), &pr)
	if !pr.Valid {
		t.Errorf("dry-run should be valid: %+v", pr)
	}
	if _, ok := keyByName(store, "dry"); ok {
		t.Errorf("dry-run must not persist")
	}
}

func TestAPIKeysReplace_SecretImmutable(t *testing.T) {
	store, dir := newAPIKeysFixture(t)
	h := APIKeysReplaceHandler(store, dir)

	rec := do(t, h, http.MethodPut, "/api/v1/config/api-keys/k1", `{"configuration":"prod","enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body)
	}
	k, _ := keyByName(store, "k1")
	if k.Secret != "sk_live_existing" {
		t.Errorf("PUT changed the secret: %q", k.Secret)
	}
	if k.Enabled {
		t.Errorf("PUT did not apply enabled=false")
	}
	if strings.Contains(rec.Body.String(), "sk_live_existing") {
		t.Errorf("PUT response leaked the secret")
	}

	if rec := do(t, h, http.MethodPut, "/api/v1/config/api-keys/k1", `{"name":"renamed","configuration":"prod","enabled":true}`); rec.Code != http.StatusConflict {
		t.Errorf("PUT rename should be 409, got %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/config/api-keys/ghost", `{"configuration":"prod","enabled":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("PUT miss = %d, want 404", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/config/api-keys/", `{"configuration":"prod"}`); rec.Code != http.StatusNotFound {
		t.Errorf("PUT empty name = %d, want 404", rec.Code)
	}
}

func TestAPIKeysPatch(t *testing.T) {
	store, dir := newAPIKeysFixture(t)
	h := APIKeysPatchHandler(store, dir)

	// disable only.
	if rec := do(t, h, http.MethodPatch, "/api/v1/config/api-keys/k1", `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("patch disable = %d (body=%s)", rec.Code, rec.Body)
	}
	k, _ := keyByName(store, "k1")
	if k.Enabled || k.Secret != "sk_live_existing" {
		t.Errorf("patch disable wrong: enabled=%v secret=%q", k.Enabled, k.Secret)
	}

	// rename.
	if rec := do(t, h, http.MethodPatch, "/api/v1/config/api-keys/k1", `{"name":"k1-renamed"}`); rec.Code != http.StatusOK {
		t.Fatalf("patch rename = %d (body=%s)", rec.Code, rec.Body)
	}
	if _, ok := keyByName(store, "k1-renamed"); !ok {
		t.Errorf("rename not applied")
	}
	renamed, _ := keyByName(store, "k1-renamed")
	if renamed.Secret != "sk_live_existing" {
		t.Errorf("rename changed the secret")
	}

	// missing key.
	if rec := do(t, h, http.MethodPatch, "/api/v1/config/api-keys/ghost", `{"enabled":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("patch miss = %d, want 404", rec.Code)
	}
	if rec := do(t, h, http.MethodPatch, "/api/v1/config/api-keys/", `{"enabled":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("patch empty name = %d, want 404", rec.Code)
	}
	if rec := do(t, h, http.MethodPatch, "/api/v1/config/api-keys/k1-renamed", `{bad`); rec.Code != http.StatusBadRequest {
		t.Errorf("patch bad json = %d, want 400", rec.Code)
	}
}

func TestAPIKeysPatch_RenameClash(t *testing.T) {
	store, dir := newAPIKeysFixture(t)
	// add a second key so a rename can clash.
	do(t, APIKeysCreateHandler(store, dir), http.MethodPost, "/api/v1/config/api-keys", `{"name":"k2","configuration":"prod"}`)

	rec := do(t, APIKeysPatchHandler(store, dir), http.MethodPatch, "/api/v1/config/api-keys/k2", `{"name":"k1"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("rename clash = %d, want 409", rec.Code)
	}
}

func TestAPIKeysDelete(t *testing.T) {
	store, dir := newAPIKeysFixture(t)
	h := APIKeysDeleteHandler(store, dir)
	if rec := do(t, h, http.MethodDelete, "/api/v1/config/api-keys/k1", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204 (body=%s)", rec.Code, rec.Body)
	}
	if _, ok := keyByName(store, "k1"); ok {
		t.Errorf("key not deleted")
	}
	if rec := do(t, h, http.MethodDelete, "/api/v1/config/api-keys/ghost", ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete miss = %d, want 404", rec.Code)
	}
	if rec := do(t, h, http.MethodDelete, "/api/v1/config/api-keys/", ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete empty name = %d, want 404", rec.Code)
	}
}

func TestAPIKeys_EmptyRefAndBadBodies(t *testing.T) {
	store, dir := newAPIKeysFixture(t)

	if rec := do(t, APIKeyDetailHandler(store), http.MethodGet, "/api/v1/config/api-keys/", ""); rec.Code != http.StatusNotFound {
		t.Errorf("detail empty ref = %d, want 404", rec.Code)
	}
	if rec := do(t, APIKeysReplaceHandler(store, dir), http.MethodPut, "/api/v1/config/api-keys/k1", `{bad`); rec.Code != http.StatusBadRequest {
		t.Errorf("replace bad json = %d, want 400", rec.Code)
	}
	bigPut := `{"configuration":"` + strings.Repeat("a", 300*1024) + `"}`
	if rec := do(t, APIKeysReplaceHandler(store, dir), http.MethodPut, "/api/v1/config/api-keys/k1", bigPut); rec.Code != http.StatusBadRequest {
		t.Errorf("replace too-large = %d, want 400", rec.Code)
	}
	bigPatch := `{"name":"` + strings.Repeat("a", 300*1024) + `"}`
	if rec := do(t, APIKeysPatchHandler(store, dir), http.MethodPatch, "/api/v1/config/api-keys/k1", bigPatch); rec.Code != http.StatusBadRequest {
		t.Errorf("patch too-large = %d, want 400", rec.Code)
	}
}

func TestAPIKeysWrite_DisabledAndPersistError(t *testing.T) {
	store, _ := newAPIKeysFixture(t)
	body := `{"name":"x","configuration":"prod"}`
	if rec := do(t, APIKeysCreateHandler(store, ""), http.MethodPost, "/api/v1/config/api-keys", body); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("create disabled = %d, want 503", rec.Code)
	}
	if rec := do(t, APIKeysReplaceHandler(store, ""), http.MethodPut, "/api/v1/config/api-keys/k1", `{"configuration":"prod","enabled":true}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("replace disabled = %d, want 503", rec.Code)
	}
	if rec := do(t, APIKeysPatchHandler(store, ""), http.MethodPatch, "/api/v1/config/api-keys/k1", `{"enabled":true}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("patch disabled = %d, want 503", rec.Code)
	}
	if rec := do(t, APIKeysDeleteHandler(store, ""), http.MethodDelete, "/api/v1/config/api-keys/k1", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("delete disabled = %d, want 503", rec.Code)
	}

	store2, _ := newAPIKeysFixture(t)
	badDir := filepath.Join(t.TempDir(), "missing")
	if rec := do(t, APIKeysCreateHandler(store2, badDir), http.MethodPost, "/api/v1/config/api-keys", `{"name":"x","configuration":"prod"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("create persist = %d, want 500", rec.Code)
	}
	if rec := do(t, APIKeysReplaceHandler(store2, badDir), http.MethodPut, "/api/v1/config/api-keys/k1", `{"configuration":"prod","enabled":true}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("replace persist = %d, want 500", rec.Code)
	}
	if rec := do(t, APIKeysPatchHandler(store2, badDir), http.MethodPatch, "/api/v1/config/api-keys/k1", `{"enabled":false}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("patch persist = %d, want 500", rec.Code)
	}
	if rec := do(t, APIKeysDeleteHandler(store2, badDir), http.MethodDelete, "/api/v1/config/api-keys/k1", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("delete persist = %d, want 500", rec.Code)
	}
}
