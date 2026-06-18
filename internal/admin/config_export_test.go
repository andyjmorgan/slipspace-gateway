package admin_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/admin"
)

const samplePolicy = `configurations:
  production:
    upstream_credentials:
      openai: sk-openai-prodsecret
      anthropic: sk-ant-prodsecret
api_keys:
  - name: prod-svc
    secret: sk_live_supersecretvalue
    configuration: production
`

const sampleProviders = `providers:
  openai:
    base_url: https://api.openai.com
    endpoints:
      chat_completions:
        accepted_paths: [/v1/chat/completions]
`

const sampleAdmin = `admin:
  enabled: true
  bind_addr: 0.0.0.0:8081
  password: hunter2-prod
`

func writeConfigDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestConfigExportFilesHandler_ReturnsRedactedFiles(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"policy.yaml":    samplePolicy,
		"providers.yaml": sampleProviders,
		"admin.yaml":     sampleAdmin,
	})

	h := admin.ConfigExportFilesHandler(dir)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/export/files", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	var body admin.ConfigExportFilesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, want := len(body.Files), 3; got != want {
		t.Fatalf("len(files) = %d, want %d", got, want)
	}
	wantOrder := []string{"admin.yaml", "policy.yaml", "providers.yaml"}
	for i, f := range body.Files {
		if f.Name != wantOrder[i] {
			t.Errorf("files[%d].Name = %q, want %q", i, f.Name, wantOrder[i])
		}
		if f.SizeBytes != len(f.Content) {
			t.Errorf("files[%d].SizeBytes = %d, want %d", i, f.SizeBytes, len(f.Content))
		}
	}

	combined := body.Files[0].Content + body.Files[1].Content + body.Files[2].Content
	for _, plaintext := range []string{"sk-openai-prodsecret", "sk-ant-prodsecret", "sk_live_supersecretvalue", "hunter2-prod"} {
		if strings.Contains(combined, plaintext) {
			t.Fatalf("plaintext %q survived export:\n%s", plaintext, combined)
		}
	}
}

func TestConfigExportFilesHandler_EmptyConfigDirReturns503(t *testing.T) {
	h := admin.ConfigExportFilesHandler("")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/config/export/files", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestConfigExportFilesHandler_LoadFailureReturns500(t *testing.T) {
	// A non-existent directory surfaces from config.ListConfigFiles as an
	// error; the handler converts it to 500. Picks an obviously absent
	// path under a TempDir to avoid colliding with any real filesystem
	// entry on the host.
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	h := admin.ConfigExportFilesHandler(dir)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/config/export/files", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestConfigExportDownloadHandler_ReturnsZipWithManifestAndRedactedFiles(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"policy.yaml":    samplePolicy,
		"providers.yaml": sampleProviders,
	})

	h := admin.ConfigExportDownloadHandler(dir, "test-host", nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/export/download", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="sluice-config-`) || !strings.HasSuffix(cd, `.zip"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if cl := rec.Header().Get("Content-Length"); cl == "" {
		t.Errorf("Content-Length unset")
	}

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	got := make(map[string]string, len(zr.File))
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("open %s: %v", zf.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", zf.Name, err)
		}
		got[zf.Name] = string(body)
	}

	if _, ok := got["MANIFEST.txt"]; !ok {
		t.Fatal("MANIFEST.txt missing from archive")
	}
	if !strings.Contains(got["MANIFEST.txt"], "test-host") {
		t.Errorf("manifest missing hostname:\n%s", got["MANIFEST.txt"])
	}

	combined := got["policy.yaml"] + got["providers.yaml"]
	for _, plaintext := range []string{"sk-openai-prodsecret", "sk-ant-prodsecret", "sk_live_supersecretvalue"} {
		if strings.Contains(combined, plaintext) {
			t.Fatalf("plaintext %q survived zip export", plaintext)
		}
	}
}

func TestConfigExportDownloadHandler_EmptyConfigDirReturns503(t *testing.T) {
	h := admin.ConfigExportDownloadHandler("", "host", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/config/export/download", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestConfigExportDownloadHandler_LoadFailureReturns500(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing-dir")
	h := admin.ConfigExportDownloadHandler(dir, "host", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/config/export/download", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
