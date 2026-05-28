package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// writableFixture loads the config-dev tree into a fresh tmp dir so
// each test gets its own writable policy.yaml + the providers.yaml +
// admin.yaml siblings the loader needs. Returns the dir and the
// loaded Store, ready for the mutation pipeline to run end-to-end.
func writableFixture(t *testing.T) (string, *config.Store) {
	t.Helper()

	src, err := filepath.Abs(filepath.Join("..", "..", "config-dev"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dir := t.TempDir()
	for _, name := range []string{"policy.yaml", "providers.yaml", "admin.yaml"} {
		body, err := os.ReadFile(filepath.Join(src, name)) //nolint:gosec // path is the test fixture under config-dev
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil { //nolint:gosec // path is under t.TempDir()
			t.Fatalf("write %s: %v", name, err)
		}
	}
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return dir, config.NewStore(resolved)
}

// newRuleJSON is the canonical happy-path body for a freshly minted
// rule: provider condition, single setHeader action, continue behavior.
func newRuleJSON(name string) []byte {
	return []byte(`{
		"name": "` + name + `",
		"condition": {"type": "provider", "operator": "Equals", "expectedProvider": "openai"},
		"actions": [
			{"type": "setHeader", "headerName": "X-Test-` + name + `", "headerAction": "Set", "headerValue": "ok"}
		],
		"behavior": "continue"
	}`)
}

func do(t *testing.T, h http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRulesCreate_HappyPath(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesCreateHandler(store, dir)

	rec := do(t, h, "POST", "/api/v1/config/rules", newRuleJSON("test-create"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	if _, ok := store.Snapshot().RuleIndex["test-create"]; !ok {
		t.Errorf("rule not present in store snapshot after create")
	}
	// Disk persistence: re-load the dir and confirm the rule survives
	// a fresh decode.
	reloaded, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if _, ok := reloaded.RuleIndex["test-create"]; !ok {
		t.Errorf("rule not persisted to disk")
	}
}

func TestRulesCreate_DuplicateName_Returns409(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesCreateHandler(store, dir)

	// config-dev/policy.yaml ships with rule "tag-openai-chat".
	rec := do(t, h, "POST", "/api/v1/config/rules", newRuleJSON("tag-openai-chat"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var body admin.RuleConflictError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	if body.Name != "tag-openai-chat" {
		t.Errorf("conflict body Name=%q, want tag-openai-chat", body.Name)
	}
}

func TestRulesCreate_BadJSON_Returns400(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesCreateHandler(store, dir)
	rec := do(t, h, "POST", "/api/v1/config/rules", []byte("{not json"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestRulesCreate_MissingName_Returns400(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesCreateHandler(store, dir)
	rec := do(t, h, "POST", "/api/v1/config/rules", []byte(`{"condition":{"type":"provider","operator":"Equals","expectedProvider":"openai"},"actions":[]}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRulesCreate_NilStore_Returns503(t *testing.T) {
	t.Parallel()
	h := admin.RulesCreateHandler(nil, "/tmp")
	rec := do(t, h, "POST", "/api/v1/config/rules", newRuleJSON("x"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", rec.Code)
	}
}

func TestRulesCreate_EmptyConfigDir_Returns503(t *testing.T) {
	t.Parallel()
	_, store := writableFixture(t)
	h := admin.RulesCreateHandler(store, "")
	rec := do(t, h, "POST", "/api/v1/config/rules", newRuleJSON("x"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", rec.Code)
	}
}

func TestRulesReplace_HappyPath(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesReplaceHandler(store, dir)

	updated := []byte(`{
		"name": "tag-openai-chat",
		"condition": {"type": "endpoint", "operator": "Equals", "expectedEndpoint": "chat_completions"},
		"actions": [
			{"type": "setHeader", "headerName": "X-Replaced", "headerAction": "Set", "headerValue": "yes"}
		],
		"behavior": "continue"
	}`)
	rec := do(t, h, "PUT", "/api/v1/config/rules/tag-openai-chat", updated)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	rule, ok := store.Snapshot().RuleIndex["tag-openai-chat"]
	if !ok {
		t.Fatal("rule missing after replace")
	}
	if rule.Condition.ConditionType() != "endpoint" {
		t.Errorf("condition type=%s, want endpoint", rule.Condition.ConditionType())
	}
}

func TestRulesReplace_MissingRule_Returns404(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesReplaceHandler(store, dir)

	rec := do(t, h, "PUT", "/api/v1/config/rules/no-such-rule", newRuleJSON("no-such-rule"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

func TestRulesReplace_RenameAttempt_Returns409(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesReplaceHandler(store, dir)

	// URL name says tag-openai-chat, body name says different.
	body := newRuleJSON("renamed-rule")
	rec := do(t, h, "PUT", "/api/v1/config/rules/tag-openai-chat", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRulesReplace_EmptyBodyName_UsesURLName(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesReplaceHandler(store, dir)

	body := []byte(`{
		"condition": {"type": "provider", "operator": "Equals", "expectedProvider": "openai"},
		"actions": [{"type": "setHeader", "headerName": "X-Bodyless-Name", "headerAction": "Set", "headerValue": "ok"}],
		"behavior": "continue"
	}`)
	rec := do(t, h, "PUT", "/api/v1/config/rules/tag-openai-chat", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRulesDelete_Unreferenced_Succeeds(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)

	// Create an unreferenced rule first via the create handler.
	createH := admin.RulesCreateHandler(store, dir)
	rec := do(t, createH, "POST", "/api/v1/config/rules", newRuleJSON("solo-rule"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup create: status=%d, body=%s", rec.Code, rec.Body.String())
	}

	delH := admin.RulesDeleteHandler(store, dir)
	rec = do(t, delH, "DELETE", "/api/v1/config/rules/solo-rule", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := store.Snapshot().RuleIndex["solo-rule"]; ok {
		t.Errorf("rule still present after delete")
	}
}

func TestRulesDelete_Referenced_Returns409WithUsedBy(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesDeleteHandler(store, dir)

	// config-dev's dev configuration references tag-openai-chat.
	rec := do(t, h, "DELETE", "/api/v1/config/rules/tag-openai-chat", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var body admin.RuleConflictError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.UsedBy) == 0 {
		t.Errorf("UsedBy empty, want at least one configuration")
	}
	foundDev := false
	for _, c := range body.UsedBy {
		if c == "dev" {
			foundDev = true
		}
	}
	if !foundDev {
		t.Errorf("UsedBy=%v, want to include dev", body.UsedBy)
	}
	// Rule must still be present on disk after the rejected delete.
	reloaded, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if _, ok := reloaded.RuleIndex["tag-openai-chat"]; !ok {
		t.Errorf("rule was unexpectedly removed from disk despite 409")
	}
}

func TestRulesDelete_Missing_Returns404(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesDeleteHandler(store, dir)
	rec := do(t, h, "DELETE", "/api/v1/config/rules/never-existed", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

// TestRulesCreate_ValidationFailure_Returns422 covers the post-mutation
// Validate path: a useResiliencePolicy action referencing an unknown
// policy fails validateLibraries' cross-block check.
func TestRulesCreate_ValidationFailure_Returns422(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesCreateHandler(store, dir)

	body := []byte(`{
		"name": "validation-broken",
		"condition": {"type": "provider", "operator": "Equals", "expectedProvider": "openai"},
		"actions": [
			{"type": "useResiliencePolicy", "policyName": "no-such-policy"}
		],
		"behavior": "continue"
	}`)
	rec := do(t, h, "POST", "/api/v1/config/rules", body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	var v admin.RuleValidationError
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode validation body: %v", err)
	}
	if v.Detail == "" {
		t.Errorf("RuleValidationError.Detail empty")
	}
	// Snapshot must be unmodified after a 422.
	if _, present := store.Snapshot().RuleIndex["validation-broken"]; present {
		t.Errorf("rule was added to snapshot despite 422")
	}
}

// TestRulesReplace_NilStore_Returns503 covers the writableGuard branch
// inside the PUT handler (the create handler test covers the POST side).
func TestRulesReplace_NilStore_Returns503(t *testing.T) {
	t.Parallel()
	h := admin.RulesReplaceHandler(nil, "/tmp")
	rec := do(t, h, "PUT", "/api/v1/config/rules/anything", newRuleJSON("anything"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", rec.Code)
	}
}

// TestRulesDelete_NilStore_Returns503 covers writableGuard on DELETE.
func TestRulesDelete_NilStore_Returns503(t *testing.T) {
	t.Parallel()
	h := admin.RulesDeleteHandler(nil, "/tmp")
	rec := do(t, h, "DELETE", "/api/v1/config/rules/anything", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", rec.Code)
	}
}

// TestRulesReplace_EmptyPath_Returns404 covers the URL-name-empty
// branch on PUT (the existing TestRulesReplace_MissingRule_Returns404
// hits the index-miss branch on a non-empty name).
func TestRulesReplace_EmptyPath_Returns404(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesReplaceHandler(store, dir)
	rec := do(t, h, "PUT", "/api/v1/config/rules/", newRuleJSON("ignored"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

// TestRulesDelete_EmptyPath_Returns404 mirrors the above for DELETE.
func TestRulesDelete_EmptyPath_Returns404(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesDeleteHandler(store, dir)
	rec := do(t, h, "DELETE", "/api/v1/config/rules/", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

// TestRulesCreate_DiskWriteFailure_Returns500 makes the config dir
// read-only and confirms a write failure surfaces as 500 with the
// snapshot left untouched.
func TestRulesCreate_DiskWriteFailure_Returns500(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)

	// Strip write permission from the dir. The temp file create will
	// fail before we reach the rename.
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // intentionally read-only for the test
		t.Fatalf("chmod ro: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // restore on cleanup; t.TempDir owns the path

	h := admin.RulesCreateHandler(store, dir)
	rec := do(t, h, "POST", "/api/v1/config/rules", newRuleJSON("write-fails"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	if _, present := store.Snapshot().RuleIndex["write-fails"]; present {
		t.Errorf("rule landed in snapshot despite disk write failure")
	}
}

func TestRulesCreate_OversizedBody_Returns400(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesCreateHandler(store, dir)
	// 512 KiB of zeroes — well over the 256 KiB cap.
	rec := do(t, h, "POST", "/api/v1/config/rules", []byte(strings.Repeat("a", 512*1024)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 for oversized body", rec.Code)
	}
}

// TestRulesReplace_EmptyBody_Returns400 covers the empty-body branch
// of decodeRuleBody on the PUT path.
func TestRulesReplace_EmptyBody_Returns400(t *testing.T) {
	t.Parallel()
	dir, store := writableFixture(t)
	h := admin.RulesReplaceHandler(store, dir)
	rec := do(t, h, "PUT", "/api/v1/config/rules/tag-openai-chat", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 for empty body", rec.Code)
	}
}
