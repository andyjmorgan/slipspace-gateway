//go:build e2e

package admin_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// configExportFile mirrors admin.ConfigExportFile on the wire — kept local so
// the e2e package depends only on the JSON shape, not the internal handler type.
type configExportFile struct {
	Name      string `json:"name"`
	Content   string `json:"content"`
	SizeBytes int    `json:"size_bytes"`
}

type configExportFilesResponse struct {
	Files []configExportFile `json:"files"`
}

// TestAdmin_ConfigExport_RedactsV2Credentials drives the redacted-config-export
// endpoint against the default config-dev/ policy, whose configurations carry
// the v2 `credentials:` map (sk-dev-mock, sk-mock-openai, ...). It asserts the
// exported bundle leaks NO plaintext provider credential and shows the "***"
// placeholder instead — the wire-level proof of the redact.go fix.
func TestAdmin_ConfigExport_RedactsV2Credentials(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	req, _ := http.NewRequest(http.MethodGet, h.AdminURL+"/api/v1/config/export/files", nil)
	req.SetBasicAuth(adminc.Username, h.AdminPassword)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/config/export/files: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body configExportFilesResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode export files: %v", err)
	}
	if len(body.Files) == 0 {
		t.Fatal("export returned zero files")
	}

	var policy *configExportFile
	for i := range body.Files {
		if body.Files[i].Name == "policy.yaml" {
			policy = &body.Files[i]
			break
		}
	}
	if policy == nil {
		t.Fatalf("policy.yaml absent from export; files = %v", names(body.Files))
	}

	// Every v2 credential value from config-dev/policy.yaml. If the redactor
	// only handled the v1 `upstream_credentials` key, these leak in cleartext.
	plaintextSecrets := []string{
		"sk-dev-mock",
		"sk-ant-dev-mock",
		"sk-mock-openai",
		"sk-ant-mock-anthropic",
		"gemini-mock",
	}
	for _, secret := range plaintextSecrets {
		if strings.Contains(policy.Content, secret) {
			t.Errorf("export leaked plaintext credential %q in policy.yaml:\n%s", secret, policy.Content)
		}
	}
	if !strings.Contains(policy.Content, "***") {
		t.Errorf("export missing redaction placeholder in policy.yaml:\n%s", policy.Content)
	}
	// Provider names under credentials are operator data, not secrets — they
	// must survive so the export stays legible.
	for _, providerKey := range []string{"openai:", "anthropic:", "gemini:"} {
		if !strings.Contains(policy.Content, providerKey) {
			t.Errorf("provider key %q lost from redacted policy.yaml:\n%s", providerKey, policy.Content)
		}
	}
}

func names(files []configExportFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	return out
}
