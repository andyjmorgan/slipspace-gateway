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
	grpFixtureBackends = `backends:
  openai:
    base_url: https://api.openai.com
    protocols:
      chat:
        path: /v1/chat/completions
groups:
  bound:
    mode: failover
    targets:
      - backend: openai
  spare:
    mode: failover
    targets:
      - backend: openai
`
	grpFixturePolicy = `configurations:
  prod:
    credentials:
      openai: sk
    bindings:
      - protocol: chat
        models: ["gpt-*"]
        group: bound
api_keys:
  - secret: sk_live_x
    name: k
    configuration: prod
    enabled: true
`
)

func newGroupsFixture(t *testing.T) (*config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"backends.yaml": grpFixtureBackends,
		"policy.yaml":   grpFixturePolicy,
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

func TestGroupsList_Detail(t *testing.T) {
	store, _ := newGroupsFixture(t)
	rec := do(t, GroupsListHandler(store), http.MethodGet, "/api/v1/config/groups", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list []groupView
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 2 || list[0].Name != "bound" {
		t.Errorf("list = %+v, want 2 sorted [bound, spare]", list)
	}

	d := do(t, GroupDetailHandler(store), http.MethodGet, "/api/v1/config/groups/spare", "")
	if d.Code != http.StatusOK {
		t.Errorf("detail status = %d", d.Code)
	}
	miss := do(t, GroupDetailHandler(store), http.MethodGet, "/api/v1/config/groups/ghost", "")
	if miss.Code != http.StatusNotFound {
		t.Errorf("detail miss status = %d, want 404", miss.Code)
	}
}

func TestGroupsCreate(t *testing.T) {
	store, dir := newGroupsFixture(t)
	h := GroupsCreateHandler(store, dir)
	rec := do(t, h, http.MethodPost, "/api/v1/config/groups",
		`{"name":"newg","mode":"load_balance","targets":[{"backend":"openai"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body)
	}
	if _, ok := store.Snapshot().Groups["newg"]; !ok {
		t.Errorf("group not in store")
	}
	reloaded, _ := config.LoadV2(context.Background(), dir)
	if _, ok := reloaded.Groups["newg"]; !ok {
		t.Errorf("group not persisted to disk")
	}
}

func TestGroupsCreate_Conflict(t *testing.T) {
	store, dir := newGroupsFixture(t)
	rec := do(t, GroupsCreateHandler(store, dir), http.MethodPost, "/api/v1/config/groups",
		`{"name":"bound","mode":"failover","targets":[{"backend":"openai"}]}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestGroupsCreate_BadAndInvalid(t *testing.T) {
	store, dir := newGroupsFixture(t)
	h := GroupsCreateHandler(store, dir)
	if rec := do(t, h, http.MethodPost, "/api/v1/config/groups", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("empty body = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/groups", `{bad`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json = %d, want 400", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/config/groups", `{"mode":"failover","targets":[{"backend":"openai"}]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing name = %d, want 400", rec.Code)
	}
	// No targets -> validateGroups rejects -> 422.
	if rec := do(t, h, http.MethodPost, "/api/v1/config/groups", `{"name":"empty","mode":"failover"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("no targets = %d, want 422", rec.Code)
	}
	// Target backend that does not exist -> 422.
	if rec := do(t, h, http.MethodPost, "/api/v1/config/groups", `{"name":"ghosttarget","mode":"failover","targets":[{"backend":"nope"}]}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown target backend = %d, want 422", rec.Code)
	}
}

func TestGroupsCreate_DryRun(t *testing.T) {
	store, dir := newGroupsFixture(t)
	rec := do(t, GroupsCreateHandler(store, dir), http.MethodPost, "/api/v1/config/groups?dry_run=true",
		`{"name":"dry","mode":"failover","targets":[{"backend":"openai"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d", rec.Code)
	}
	var pr PreviewResult
	_ = json.Unmarshal(rec.Body.Bytes(), &pr)
	if !pr.Valid {
		t.Errorf("dry-run should be valid: %+v", pr)
	}
	if _, ok := store.Snapshot().Groups["dry"]; ok {
		t.Errorf("dry-run must not persist")
	}
}

func TestGroupsReplace(t *testing.T) {
	store, dir := newGroupsFixture(t)
	h := GroupsReplaceHandler(store, dir)
	rec := do(t, h, http.MethodPut, "/api/v1/config/groups/spare",
		`{"mode":"load_balance","targets":[{"backend":"openai","weight":5}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body)
	}
	if store.Snapshot().Groups["spare"].Mode != "load_balance" {
		t.Errorf("replace not applied")
	}

	if rec := do(t, h, http.MethodPut, "/api/v1/config/groups/ghost", `{"mode":"failover","targets":[{"backend":"openai"}]}`); rec.Code != http.StatusNotFound {
		t.Errorf("404 expected, got %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/config/groups/spare", `{"name":"renamed","mode":"failover","targets":[{"backend":"openai"}]}`); rec.Code != http.StatusConflict {
		t.Errorf("rename should be 409, got %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/config/groups/", `{"mode":"failover","targets":[{"backend":"openai"}]}`); rec.Code != http.StatusNotFound {
		t.Errorf("empty name should be 404, got %d", rec.Code)
	}
}

func TestGroupsDelete(t *testing.T) {
	store, dir := newGroupsFixture(t)
	h := GroupsDeleteHandler(store, dir)

	// "spare" unreferenced -> deletable.
	if rec := do(t, h, http.MethodDelete, "/api/v1/config/groups/spare", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204 (body=%s)", rec.Code, rec.Body)
	}
	if _, ok := store.Snapshot().Groups["spare"]; ok {
		t.Errorf("group not deleted")
	}

	// "bound" referenced by a binding -> 409.
	rec := do(t, h, http.MethodDelete, "/api/v1/config/groups/bound", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("referenced delete = %d, want 409", rec.Code)
	}
	var ce ConflictError
	_ = json.Unmarshal(rec.Body.Bytes(), &ce)
	if len(ce.UsedBy) == 0 {
		t.Errorf("conflict should name referrers: %+v", ce)
	}

	if rec := do(t, h, http.MethodDelete, "/api/v1/config/groups/ghost", ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing delete = %d, want 404", rec.Code)
	}
}

// TestGroupsWrite_PersistError drives the commitClone disk-write failure path
// (500): a configDir that is non-empty (so the write surface is enabled) but
// whose directory does not exist, so WriteConfigV2's file write fails after
// validation passes.
func TestGroupsRead_Unavailable(t *testing.T) {
	// A nil store yields a nil snapshot -> 503 from the read handlers.
	if rec := do(t, GroupsListHandler(nil), http.MethodGet, "/api/v1/config/groups", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("list unavailable = %d, want 503", rec.Code)
	}
	if rec := do(t, GroupDetailHandler(nil), http.MethodGet, "/api/v1/config/groups/x", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("detail unavailable = %d, want 503", rec.Code)
	}
}

func TestGroupsWrite_PersistError(t *testing.T) {
	store, _ := newGroupsFixture(t)
	badDir := filepath.Join(t.TempDir(), "does-not-exist")

	if rec := do(t, GroupsCreateHandler(store, badDir), http.MethodPost, "/api/v1/config/groups",
		`{"name":"x","mode":"failover","targets":[{"backend":"openai"}]}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("create persist error = %d, want 500", rec.Code)
	}
	if rec := do(t, GroupsReplaceHandler(store, badDir), http.MethodPut, "/api/v1/config/groups/spare",
		`{"mode":"failover","targets":[{"backend":"openai"}]}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("replace persist error = %d, want 500", rec.Code)
	}
	if rec := do(t, GroupsDeleteHandler(store, badDir), http.MethodDelete, "/api/v1/config/groups/spare", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("delete persist error = %d, want 500", rec.Code)
	}
}

func TestGroupsCreate_TooLarge(t *testing.T) {
	store, dir := newGroupsFixture(t)
	big := `{"name":"big","mode":"failover","targets":[{"backend":"openai"}],"pad":"` +
		strings.Repeat("a", 300*1024) + `"}`
	rec := do(t, GroupsCreateHandler(store, dir), http.MethodPost, "/api/v1/config/groups", big)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("oversized body = %d, want 400", rec.Code)
	}
}

func TestGroupsReplace_DryRun(t *testing.T) {
	store, dir := newGroupsFixture(t)
	rec := do(t, GroupsReplaceHandler(store, dir), http.MethodPut, "/api/v1/config/groups/spare?dry_run=true",
		`{"mode":"load_balance","targets":[{"backend":"openai"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d", rec.Code)
	}
	if store.Snapshot().Groups["spare"].Mode != "failover" {
		t.Errorf("dry-run replace must not mutate the store")
	}
}

func TestGroupsWrite_Disabled(t *testing.T) {
	store, _ := newGroupsFixture(t)
	if rec := do(t, GroupsCreateHandler(store, ""), http.MethodPost, "/api/v1/config/groups", `{"name":"x","mode":"failover","targets":[{"backend":"openai"}]}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("create disabled = %d, want 503", rec.Code)
	}
	if rec := do(t, GroupsReplaceHandler(store, ""), http.MethodPut, "/api/v1/config/groups/spare", `{"mode":"failover","targets":[{"backend":"openai"}]}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("replace disabled = %d, want 503", rec.Code)
	}
	if rec := do(t, GroupsDeleteHandler(store, ""), http.MethodDelete, "/api/v1/config/groups/spare", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("delete disabled = %d, want 503", rec.Code)
	}
}
