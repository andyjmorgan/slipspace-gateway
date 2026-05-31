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
	cfgFixtureBackends = `backends:
  openai:
    base_url: https://api.openai.com
    protocols:
      chat:
        path: /v1/chat/completions
  anthropic:
    base_url: https://api.anthropic.com
    protocols:
      chat:
        path: /v1/chat/completions
`
	cfgFixturePolicy = `configurations:
  prod:
    credentials:
      openai: realsecret
    bindings:
      - protocol: chat
        models: ["gpt-*"]
        backend: openai
  spare:
    bindings:
      - protocol: chat
        models: ["o1*"]
        backend: openai
api_keys:
  - secret: sk_live_x
    name: k
    configuration: prod
    enabled: true
`
)

func newConfigurationsFixture(t *testing.T) (*config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"backends.yaml": cfgFixtureBackends,
		"policy.yaml":   cfgFixturePolicy,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	rc, err := config.LoadV2(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	return config.NewStore(rc), dir
}

func TestConfigurationsCreate(t *testing.T) {
	store, dir := newConfigurationsFixture(t)
	h := ConfigurationsCreateHandler(store, dir)
	body := `{"name":"newc","credentials":{"openai":"sk-new"},"bindings":[{"protocol":"chat","models":["gpt-*"],"backend":"openai"}]}`
	rec := do(t, h, http.MethodPost, "/api/v1/config/configurations", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body)
	}
	if store.Snapshot().Configurations["newc"].Credentials["openai"] != "sk-new" {
		t.Errorf("credential not stored")
	}
	reloaded, _ := config.LoadV2(context.Background(), dir)
	if _, ok := reloaded.Configurations["newc"]; !ok {
		t.Errorf("configuration not persisted to disk")
	}
}

func TestConfigurationsCreate_Errors(t *testing.T) {
	store, dir := newConfigurationsFixture(t)
	h := ConfigurationsCreateHandler(store, dir)
	if rec := do(t, h, http.MethodPost, "/api/v1/config/configurations", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("empty body = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/configurations", `{bad`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/configurations", `{"bindings":[]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing name = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/configurations", `{"name":"prod"}`); rec.Code != http.StatusConflict {
		t.Errorf("dup = %d, want 409", rec.Code)
	}
	// Credential for a backend that does not exist -> 422.
	if rec := do(t, h, http.MethodPost, "/api/v1/config/configurations", `{"name":"bad","credentials":{"ghost":"x"}}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown-backend credential = %d, want 422", rec.Code)
	}
	// Oversized body -> 400.
	big := `{"name":"big","tags":{"x":"` + strings.Repeat("a", 300*1024) + `"}}`
	if rec := do(t, h, http.MethodPost, "/api/v1/config/configurations", big); rec.Code != http.StatusBadRequest {
		t.Errorf("oversized = %d, want 400", rec.Code)
	}
}

// TestConfigurations_CredentialWriteBack is the load-bearing secret test: a
// masked round-trip (null credential) keeps the stored secret; a real value
// rotates it; "" sets a no-credential backend.
func TestConfigurations_CredentialWriteBack(t *testing.T) {
	store, dir := newConfigurationsFixture(t)
	h := ConfigurationsReplaceHandler(store, dir)
	binding := `"bindings":[{"protocol":"chat","models":["gpt-*"],"backend":"openai"}]`

	// null credential = unchanged -> stored secret preserved.
	rec := do(t, h, http.MethodPut, "/api/v1/config/configurations/prod",
		`{"credentials":{"openai":null},`+binding+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unchanged status = %d (body=%s)", rec.Code, rec.Body)
	}
	if got := store.Snapshot().Configurations["prod"].Credentials["openai"]; got != "realsecret" {
		t.Errorf("masked round-trip wiped the secret: got %q, want realsecret", got)
	}

	// real value -> rotated.
	do(t, h, http.MethodPut, "/api/v1/config/configurations/prod",
		`{"credentials":{"openai":"rotated"},`+binding+`}`)
	if got := store.Snapshot().Configurations["prod"].Credentials["openai"]; got != "rotated" {
		t.Errorf("rotation failed: got %q", got)
	}

	// "" -> no-credential backend.
	do(t, h, http.MethodPut, "/api/v1/config/configurations/prod",
		`{"credentials":{"openai":""},`+binding+`}`)
	if got := store.Snapshot().Configurations["prod"].Credentials["openai"]; got != "" {
		t.Errorf("explicit empty failed: got %q", got)
	}

	// The HTTP response masks the secret (never returns plaintext).
	final := do(t, h, http.MethodPut, "/api/v1/config/configurations/prod",
		`{"credentials":{"openai":"topsecret"},`+binding+`}`)
	if strings.Contains(final.Body.String(), "topsecret") {
		t.Errorf("response leaked the plaintext secret: %s", final.Body)
	}
}

// TestConfigurations_ApiKeysIgnored asserts a config write payload carrying an
// api_keys field does not mutate keys — keys are managed only via /api-keys.
func TestConfigurations_ApiKeysIgnored(t *testing.T) {
	store, dir := newConfigurationsFixture(t)
	before := len(store.Snapshot().APIKeys)
	rec := do(t, ConfigurationsCreateHandler(store, dir), http.MethodPost, "/api/v1/config/configurations",
		`{"name":"withkeys","bindings":[{"protocol":"chat","models":["gpt-*"],"backend":"openai"}],"api_keys":[{"secret":"sk_live_evil","name":"injected","configuration":"withkeys","enabled":true}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body)
	}
	if after := len(store.Snapshot().APIKeys); after != before {
		t.Errorf("api_keys in config payload mutated keys: before=%d after=%d", before, after)
	}
	if _, ok := store.Snapshot().SecretIndex["sk_live_evil"]; ok {
		t.Errorf("injected api key was accepted from a config payload")
	}
}

func TestConfigurationsReplace_Misc(t *testing.T) {
	store, dir := newConfigurationsFixture(t)
	h := ConfigurationsReplaceHandler(store, dir)
	bind := `"bindings":[{"protocol":"chat","models":["o1*"],"backend":"openai"}]`

	if rec := do(t, h, http.MethodPut, "/api/v1/config/configurations/ghost", `{`+bind+`}`); rec.Code != http.StatusNotFound {
		t.Errorf("404 expected, got %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/config/configurations/spare", `{"name":"renamed",`+bind+`}`); rec.Code != http.StatusConflict {
		t.Errorf("rename should be 409, got %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/config/configurations/", `{`+bind+`}`); rec.Code != http.StatusNotFound {
		t.Errorf("empty name should be 404, got %d", rec.Code)
	}
	// dry-run must not mutate.
	if rec := do(t, h, http.MethodPut, "/api/v1/config/configurations/spare?dry_run=true", `{"tags":{"x":"y"},`+bind+`}`); rec.Code != http.StatusOK {
		t.Errorf("dry-run = %d, want 200", rec.Code)
	}
	if _, ok := store.Snapshot().Configurations["spare"].Tags["x"]; ok {
		t.Errorf("dry-run mutated the store")
	}
}

func TestConfigurationsDelete(t *testing.T) {
	store, dir := newConfigurationsFixture(t)
	h := ConfigurationsDeleteHandler(store, dir)

	// "spare" has no api keys -> deletable.
	if rec := do(t, h, http.MethodDelete, "/api/v1/config/configurations/spare", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete spare = %d, want 204 (body=%s)", rec.Code, rec.Body)
	}
	if _, ok := store.Snapshot().Configurations["spare"]; ok {
		t.Errorf("spare not deleted")
	}

	// "prod" is referenced by an api key -> 409.
	rec := do(t, h, http.MethodDelete, "/api/v1/config/configurations/prod", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete prod = %d, want 409", rec.Code)
	}
	var ce ConflictError
	_ = json.Unmarshal(rec.Body.Bytes(), &ce)
	if len(ce.UsedBy) == 0 {
		t.Errorf("conflict should name referrers: %+v", ce)
	}

	if rec := do(t, h, http.MethodDelete, "/api/v1/config/configurations/ghost", ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing delete = %d, want 404", rec.Code)
	}
}

func TestConfigurationsWrite_DisabledAndPersistError(t *testing.T) {
	store, _ := newConfigurationsFixture(t)
	if rec := do(t, ConfigurationsCreateHandler(store, ""), http.MethodPost, "/api/v1/config/configurations", `{"name":"x"}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("create disabled = %d, want 503", rec.Code)
	}
	if rec := do(t, ConfigurationsReplaceHandler(store, ""), http.MethodPut, "/api/v1/config/configurations/prod", `{}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("replace disabled = %d, want 503", rec.Code)
	}
	if rec := do(t, ConfigurationsDeleteHandler(store, ""), http.MethodDelete, "/api/v1/config/configurations/spare", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("delete disabled = %d, want 503", rec.Code)
	}

	store2, _ := newConfigurationsFixture(t)
	badDir := filepath.Join(t.TempDir(), "missing")
	if rec := do(t, ConfigurationsCreateHandler(store2, badDir), http.MethodPost, "/api/v1/config/configurations",
		`{"name":"x","bindings":[{"protocol":"chat","models":["z*"],"backend":"openai"}]}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("create persist error = %d, want 500", rec.Code)
	}
	if rec := do(t, ConfigurationsReplaceHandler(store2, badDir), http.MethodPut, "/api/v1/config/configurations/prod",
		`{"bindings":[{"protocol":"chat","models":["gpt-*"],"backend":"openai"}]}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("replace persist error = %d, want 500", rec.Code)
	}
	if rec := do(t, ConfigurationsDeleteHandler(store2, badDir), http.MethodDelete, "/api/v1/config/configurations/spare", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("delete persist error = %d, want 500", rec.Code)
	}
}
