//go:build e2e

package admin_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// These exercise the v2 read-write config surface through the spawned gateway
// binary: each mutation goes over HTTP to the :8081 admin listener (with Basic
// auth), and the assertions read back through the same API so the mux wiring,
// auth, commitClone persistence, and atomic snapshot swap are all in the loop.
// authedJSON / mustJSONDecode live in rules_write_test.go (same package).

func bodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return string(b)
}

func wantStatus(t *testing.T, resp *http.Response, want int, ctx string) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s: status=%d, want %d; body=%s", ctx, resp.StatusCode, want, bodyString(t, resp))
	}
}

func TestAdmin_Providers_FullLifecycle(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})
	const name = "e2e-be"

	resp := authedJSON(t, h, "POST", "/api/v1/config/providers",
		[]byte(`{"name":"`+name+`","base_url":"http://mockllm:5555","protocols":{"chat":{"path":"/v1/chat/completions"}}}`))
	wantStatus(t, resp, http.StatusCreated, "POST provider")
	_ = resp.Body.Close()

	resp = authedJSON(t, h, "GET", "/api/v1/config/providers/"+name, nil)
	wantStatus(t, resp, http.StatusOK, "GET provider")
	_ = resp.Body.Close()

	resp = authedJSON(t, h, "PUT", "/api/v1/config/providers/"+name,
		[]byte(`{"base_url":"http://moved:5555","protocols":{"chat":{"path":"/v1/chat/completions"}}}`))
	wantStatus(t, resp, http.StatusOK, "PUT provider")
	_ = resp.Body.Close()

	resp = authedJSON(t, h, "GET", "/api/v1/config/providers/"+name, nil)
	if !strings.Contains(bodyString(t, resp), "moved:5555") {
		t.Errorf("PUT did not persist new base_url")
	}

	resp = authedJSON(t, h, "DELETE", "/api/v1/config/providers/"+name, nil)
	wantStatus(t, resp, http.StatusNoContent, "DELETE provider")
	_ = resp.Body.Close()

	resp = authedJSON(t, h, "GET", "/api/v1/config/providers/"+name, nil)
	wantStatus(t, resp, http.StatusNotFound, "GET after delete")
	_ = resp.Body.Close()
}

func TestAdmin_Providers_DeleteReferenced_409(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	// openai is referenced by bindings + credentials in config-dev.
	resp := authedJSON(t, h, "DELETE", "/api/v1/config/providers/openai", nil)
	wantStatus(t, resp, http.StatusConflict, "DELETE referenced provider")
	var c struct {
		UsedBy []string `json:"used_by"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	_ = resp.Body.Close()
	if len(c.UsedBy) == 0 {
		t.Errorf("409 should name referrers")
	}
}

func TestAdmin_Groups_FullLifecycle(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})
	const name = "e2e-grp"

	resp := authedJSON(t, h, "POST", "/api/v1/config/groups",
		[]byte(`{"name":"`+name+`","mode":"failover","targets":[{"provider":"openai"}]}`))
	wantStatus(t, resp, http.StatusCreated, "POST group")
	_ = resp.Body.Close()

	resp = authedJSON(t, h, "PUT", "/api/v1/config/groups/"+name,
		[]byte(`{"mode":"load_balance","targets":[{"provider":"openai","weight":3}]}`))
	wantStatus(t, resp, http.StatusOK, "PUT group")
	_ = resp.Body.Close()

	resp = authedJSON(t, h, "GET", "/api/v1/config/groups/"+name, nil)
	if !strings.Contains(bodyString(t, resp), "load_balance") {
		t.Errorf("PUT did not persist mode")
	}

	resp = authedJSON(t, h, "DELETE", "/api/v1/config/groups/"+name, nil)
	wantStatus(t, resp, http.StatusNoContent, "DELETE group")
	_ = resp.Body.Close()
}

func TestAdmin_Configurations_DeleteReferenced_409(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	// dev is referenced by an api_key in config-dev.
	resp := authedJSON(t, h, "DELETE", "/api/v1/config/configurations/dev", nil)
	wantStatus(t, resp, http.StatusConflict, "DELETE referenced configuration")
	var c struct {
		UsedBy []string `json:"used_by"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	_ = resp.Body.Close()
	if len(c.UsedBy) == 0 {
		t.Errorf("409 should name referring api keys")
	}
}

type credView struct {
	Length int    `json:"length"`
	Last4  string `json:"last4"`
}

// TestAdmin_Configurations_CredentialWriteBack proves the masked round-trip
// through the binary: a configuration created with a credential, then PUT back
// with that credential null (the "unchanged" signal), keeps the stored secret —
// the GET never leaks plaintext and the credential is not wiped.
func TestAdmin_Configurations_CredentialWriteBack(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})
	const name = "e2e-cfg"

	resp := authedJSON(t, h, "POST", "/api/v1/config/configurations",
		[]byte(`{"name":"`+name+`","credentials":{"openai":"secret123"},"bindings":[{"protocol":"chat","models":["gpt-*"],"provider":"openai"}]}`))
	wantStatus(t, resp, http.StatusCreated, "POST configuration")
	if strings.Contains(bodyString(t, resp), "secret123") {
		t.Errorf("create response leaked the plaintext credential")
	}

	// GET: credential redacted, length 9.
	resp = authedJSON(t, h, "GET", "/api/v1/config/configurations/"+name, nil)
	wantStatus(t, resp, http.StatusOK, "GET configuration")
	var before struct {
		Credentials map[string]credView `json:"credentials"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&before); err != nil {
		t.Fatalf("decode: %v", err)
	}
	_ = resp.Body.Close()
	if before.Credentials["openai"].Length != 9 {
		t.Fatalf("credential length=%d, want 9", before.Credentials["openai"].Length)
	}

	// PUT with the credential null (unchanged) — must NOT wipe it.
	resp = authedJSON(t, h, "PUT", "/api/v1/config/configurations/"+name,
		[]byte(`{"credentials":{"openai":null},"bindings":[{"protocol":"chat","models":["gpt-*"],"provider":"openai"}]}`))
	wantStatus(t, resp, http.StatusOK, "PUT configuration (null credential)")
	_ = resp.Body.Close()

	resp = authedJSON(t, h, "GET", "/api/v1/config/configurations/"+name, nil)
	var after struct {
		Credentials map[string]credView `json:"credentials"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&after); err != nil {
		t.Fatalf("decode: %v", err)
	}
	_ = resp.Body.Close()
	if after.Credentials["openai"].Length != 9 {
		t.Errorf("masked round-trip wiped the credential: length=%d, want 9 (still secret123)", after.Credentials["openai"].Length)
	}

	resp = authedJSON(t, h, "DELETE", "/api/v1/config/configurations/"+name, nil)
	wantStatus(t, resp, http.StatusNoContent, "DELETE configuration")
	_ = resp.Body.Close()
}

func TestAdmin_Connectors_FullLifecycle(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})
	const name = "e2e-conn"

	resp := authedJSON(t, h, "POST", "/api/v1/config/connectors",
		[]byte(`{"name":"`+name+`","type":"webhook","url":"https://example.com/hook","secret_ref":"env:E2E_HOOK","timeout_ms":5000}`))
	wantStatus(t, resp, http.StatusCreated, "POST connector")
	_ = resp.Body.Close()

	resp = authedJSON(t, h, "PUT", "/api/v1/config/connectors/"+name,
		[]byte(`{"type":"webhook","url":"https://example.com/hook2","secret_ref":"env:E2E_HOOK","timeout_ms":9000}`))
	wantStatus(t, resp, http.StatusOK, "PUT connector")
	_ = resp.Body.Close()

	resp = authedJSON(t, h, "GET", "/api/v1/config/connectors/"+name, nil)
	if !strings.Contains(bodyString(t, resp), "hook2") {
		t.Errorf("PUT did not persist connector url")
	}

	resp = authedJSON(t, h, "DELETE", "/api/v1/config/connectors/"+name, nil)
	wantStatus(t, resp, http.StatusNoContent, "DELETE connector")
	_ = resp.Body.Close()
}

// TestAdmin_APIKeys_MintLifecycle covers the dedicated api-key resource through
// the binary: mint (one-time plaintext reveal), redacted list, disable via
// PATCH addressed by the minted UUID, and delete.
func TestAdmin_APIKeys_MintLifecycle(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	resp := authedJSON(t, h, "POST", "/api/v1/config/api-keys",
		[]byte(`{"name":"e2e-key","configuration":"dev"}`))
	wantStatus(t, resp, http.StatusCreated, "POST api-key")
	var reveal struct {
		Name   string `json:"name"`
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reveal); err != nil {
		t.Fatalf("decode reveal: %v", err)
	}
	_ = resp.Body.Close()
	if !strings.HasPrefix(reveal.Secret, "sk_live_") {
		t.Fatalf("minted secret missing sk_live_ prefix")
	}
	secret := reveal.Secret

	// List: the new key present, its id captured, and the plaintext NOT leaked.
	resp = authedJSON(t, h, "GET", "/api/v1/config/api-keys", nil)
	listBody := bodyString(t, resp)
	if strings.Contains(listBody, secret) {
		t.Errorf("api-keys list leaked the plaintext secret")
	}
	var list []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(listBody), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var id string
	for _, k := range list {
		if k.Name == "e2e-key" {
			id = k.ID
		}
	}
	if id == "" {
		t.Fatalf("minted key has no id in the list")
	}

	// PATCH disable, addressed by the minted UUID.
	resp = authedJSON(t, h, "PATCH", "/api/v1/config/api-keys/"+id, []byte(`{"enabled":false}`))
	wantStatus(t, resp, http.StatusOK, "PATCH api-key disable")
	_ = resp.Body.Close()

	resp = authedJSON(t, h, "GET", "/api/v1/config/api-keys/"+id, nil)
	if !strings.Contains(bodyString(t, resp), `"enabled":false`) {
		t.Errorf("PATCH did not disable the key")
	}

	resp = authedJSON(t, h, "DELETE", "/api/v1/config/api-keys/"+id, nil)
	wantStatus(t, resp, http.StatusNoContent, "DELETE api-key")
	_ = resp.Body.Close()
}
