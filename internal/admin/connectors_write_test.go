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
	connFixtureProviders = `providers:
  openai:
    base_url: https://api.openai.com
    protocols:
      chat:
        path: /v1/chat/completions
`
	connFixturePolicy = `configurations:
  prod:
    credentials:
      openai: sk
    bindings:
      - protocol: chat
        models: ["gpt-*"]
        provider: openai
    connector_bindings:
      - connector: bound
        sampling: 1.0
api_keys:
  - secret: sk_live_x
    name: k
    configuration: prod
    enabled: true
connectors:
  - name: bound
    type: webhook
    url: https://example.com/bound
    secret_ref: env:BOUND
    timeout_ms: 5000
  - name: spare
    type: webhook
    url: https://example.com/spare
    secret_ref: env:SPARE
    timeout_ms: 5000
`
)

func newConnectorsFixture(t *testing.T) (*config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"providers.yaml": connFixtureProviders,
		"policy.yaml":    connFixturePolicy,
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

const validConnectorJSON = `{"name":"newc","type":"webhook","url":"https://example.com/new","secret_ref":"env:NEW","timeout_ms":5000}`

func TestConnectorsList_Detail(t *testing.T) {
	store, _ := newConnectorsFixture(t)
	rec := do(t, ConnectorsListHandler(store), http.MethodGet, "/api/v1/config/connectors", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 2 || list[0]["name"] != "bound" {
		t.Errorf("list = %+v, want 2 sorted [bound, spare]", list)
	}

	if rec := do(t, ConnectorDetailHandler(store), http.MethodGet, "/api/v1/config/connectors/spare", ""); rec.Code != http.StatusOK {
		t.Errorf("detail status = %d", rec.Code)
	}
	if rec := do(t, ConnectorDetailHandler(store), http.MethodGet, "/api/v1/config/connectors/ghost", ""); rec.Code != http.StatusNotFound {
		t.Errorf("detail miss = %d, want 404", rec.Code)
	}
}

func TestConnectorsRead_Unavailable(t *testing.T) {
	if rec := do(t, ConnectorsListHandler(nil), http.MethodGet, "/api/v1/config/connectors", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("list unavailable = %d, want 503", rec.Code)
	}
	if rec := do(t, ConnectorDetailHandler(nil), http.MethodGet, "/api/v1/config/connectors/x", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("detail unavailable = %d, want 503", rec.Code)
	}
}

func TestConnectorsCreate(t *testing.T) {
	store, dir := newConnectorsFixture(t)
	rec := do(t, ConnectorsCreateHandler(store, dir), http.MethodPost, "/api/v1/config/connectors", validConnectorJSON)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body)
	}
	if _, ok := indexOfConnector(store.Snapshot().Connectors, "newc"); !ok {
		t.Errorf("connector not in store")
	}
	reloaded, _ := config.Load(context.Background(), dir)
	if _, ok := indexOfConnector(reloaded.Connectors, "newc"); !ok {
		t.Errorf("connector not persisted to disk")
	}
}

func TestConnectorsCreate_Errors(t *testing.T) {
	store, dir := newConnectorsFixture(t)
	h := ConnectorsCreateHandler(store, dir)
	if rec := do(t, h, http.MethodPost, "/api/v1/config/connectors", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("empty = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/connectors", `{bad`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/connectors", `{"type":"webhook"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing name = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/connectors", `{"name":"bound","type":"webhook","url":"https://example.com/x","secret_ref":"env:X","timeout_ms":5000}`); rec.Code != http.StatusConflict {
		t.Errorf("dup = %d, want 409", rec.Code)
	}
	// type webhook missing url/secret_ref/timeout -> Validate fails -> 422.
	if rec := do(t, h, http.MethodPost, "/api/v1/config/connectors", `{"name":"bad","type":"webhook"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid connector = %d, want 422", rec.Code)
	}
	big := `{"name":"big","type":"webhook","url":"https://example.com/x","secret_ref":"env:X","timeout_ms":5000,"prefix":"` + strings.Repeat("a", 300*1024) + `"}`
	if rec := do(t, h, http.MethodPost, "/api/v1/config/connectors", big); rec.Code != http.StatusBadRequest {
		t.Errorf("oversized = %d, want 400", rec.Code)
	}
}

func TestConnectorsCreate_DryRun(t *testing.T) {
	store, dir := newConnectorsFixture(t)
	rec := do(t, ConnectorsCreateHandler(store, dir), http.MethodPost, "/api/v1/config/connectors?dry_run=true", validConnectorJSON)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run = %d", rec.Code)
	}
	var pr PreviewResult
	_ = json.Unmarshal(rec.Body.Bytes(), &pr)
	if !pr.Valid {
		t.Errorf("dry-run should be valid: %+v", pr)
	}
	if _, ok := indexOfConnector(store.Snapshot().Connectors, "newc"); ok {
		t.Errorf("dry-run must not persist")
	}
}

func TestConnectorsReplace(t *testing.T) {
	store, dir := newConnectorsFixture(t)
	h := ConnectorsReplaceHandler(store, dir)

	rec := do(t, h, http.MethodPut, "/api/v1/config/connectors/spare",
		`{"type":"webhook","url":"https://example.com/moved","secret_ref":"env:SPARE","timeout_ms":9000}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body)
	}
	idx, _ := indexOfConnector(store.Snapshot().Connectors, "spare")
	if store.Snapshot().Connectors[idx].TimeoutMS != 9000 {
		t.Errorf("replace not applied")
	}

	// Body omits name so it adopts the URL name -> the 404 path (not the rename 409).
	if rec := do(t, h, http.MethodPut, "/api/v1/config/connectors/ghost",
		`{"type":"webhook","url":"https://example.com/x","secret_ref":"env:X","timeout_ms":5000}`); rec.Code != http.StatusNotFound {
		t.Errorf("404 expected, got %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/config/connectors/spare", `{"name":"renamed","type":"webhook","url":"https://example.com/x","secret_ref":"env:X","timeout_ms":5000}`); rec.Code != http.StatusConflict {
		t.Errorf("rename should be 409, got %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/config/connectors/", validConnectorJSON); rec.Code != http.StatusNotFound {
		t.Errorf("empty name should be 404, got %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/config/connectors/spare?dry_run=true",
		`{"type":"webhook","url":"https://example.com/dry","secret_ref":"env:SPARE","timeout_ms":1000}`); rec.Code != http.StatusOK {
		t.Errorf("dry-run = %d, want 200", rec.Code)
	}
}

func TestConnectorsDelete(t *testing.T) {
	store, dir := newConnectorsFixture(t)
	h := ConnectorsDeleteHandler(store, dir)

	// "spare" unreferenced -> deletable.
	if rec := do(t, h, http.MethodDelete, "/api/v1/config/connectors/spare", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete spare = %d, want 204 (body=%s)", rec.Code, rec.Body)
	}
	if _, ok := indexOfConnector(store.Snapshot().Connectors, "spare"); ok {
		t.Errorf("spare not deleted")
	}

	// "bound" referenced by a configuration's connector_bindings -> 409.
	rec := do(t, h, http.MethodDelete, "/api/v1/config/connectors/bound", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete bound = %d, want 409", rec.Code)
	}
	var ce ConflictError
	_ = json.Unmarshal(rec.Body.Bytes(), &ce)
	if len(ce.UsedBy) == 0 {
		t.Errorf("conflict should name referrers: %+v", ce)
	}

	if rec := do(t, h, http.MethodDelete, "/api/v1/config/connectors/ghost", ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing = %d, want 404", rec.Code)
	}
}

func TestConnectorsWrite_DisabledAndPersistError(t *testing.T) {
	store, _ := newConnectorsFixture(t)
	if rec := do(t, ConnectorsCreateHandler(store, ""), http.MethodPost, "/api/v1/config/connectors", validConnectorJSON); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("create disabled = %d, want 503", rec.Code)
	}
	if rec := do(t, ConnectorsReplaceHandler(store, ""), http.MethodPut, "/api/v1/config/connectors/spare", validConnectorJSON); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("replace disabled = %d, want 503", rec.Code)
	}
	if rec := do(t, ConnectorsDeleteHandler(store, ""), http.MethodDelete, "/api/v1/config/connectors/spare", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("delete disabled = %d, want 503", rec.Code)
	}

	store2, _ := newConnectorsFixture(t)
	badDir := filepath.Join(t.TempDir(), "missing")
	if rec := do(t, ConnectorsCreateHandler(store2, badDir), http.MethodPost, "/api/v1/config/connectors", validConnectorJSON); rec.Code != http.StatusInternalServerError {
		t.Errorf("create persist = %d, want 500", rec.Code)
	}
	if rec := do(t, ConnectorsReplaceHandler(store2, badDir), http.MethodPut, "/api/v1/config/connectors/spare",
		`{"type":"webhook","url":"https://example.com/x","secret_ref":"env:X","timeout_ms":5000}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("replace persist = %d, want 500", rec.Code)
	}
	if rec := do(t, ConnectorsDeleteHandler(store2, badDir), http.MethodDelete, "/api/v1/config/connectors/spare", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("delete persist = %d, want 500", rec.Code)
	}
}
