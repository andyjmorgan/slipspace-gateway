package configexport_test

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/internal/admin/configexport"
)

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

const samplePolicyYAML = `configurations:
  production:
    upstream_credentials:
      openai: sk-openai-aaaabbbb
      anthropic: sk-ant-ccccdddd
api_keys:
  - name: prod-svc
    secret: sk_live_topsecretvalue
    configuration: production
`

const sampleProvidersYAML = `providers:
  openai:
    base_url: https://api.openai.com
    endpoints:
      chat_completions:
        accepted_paths: [/v1/chat/completions]
`

const sampleAdminYAML = `admin:
  enabled: true
  bind_addr: 0.0.0.0:8081
  password: hunter2-prod-secret
`

func TestLoadRedacted_RedactsEveryDeclaredSecret(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"policy.yaml":    samplePolicyYAML,
		"providers.yaml": sampleProvidersYAML,
		"admin.yaml":     sampleAdminYAML,
	})

	files, err := configexport.LoadRedacted(dir)
	if err != nil {
		t.Fatalf("LoadRedacted: %v", err)
	}
	if got, want := len(files), 3; got != want {
		t.Fatalf("file count = %d, want %d", got, want)
	}
	// Files come back alphabetised so the UI tabs and the ZIP listing stay
	// in lockstep across calls.
	wantOrder := []string{"admin.yaml", "policy.yaml", "providers.yaml"}
	for i, f := range files {
		if f.Name != wantOrder[i] {
			t.Fatalf("file[%d] = %q, want %q", i, f.Name, wantOrder[i])
		}
		if f.SizeBytes != len(f.Content) {
			t.Fatalf("file %q SizeBytes mismatch: %d vs len(Content)=%d", f.Name, f.SizeBytes, len(f.Content))
		}
	}

	combined := strings.Join([]string{files[0].Content, files[1].Content, files[2].Content}, "\n")
	for _, plaintext := range []string{"sk-openai-aaaabbbb", "sk-ant-ccccdddd", "sk_live_topsecretvalue", "hunter2-prod-secret"} {
		if strings.Contains(combined, plaintext) {
			t.Fatalf("plaintext secret %q survived bundle redaction", plaintext)
		}
	}
}

func TestLoadRedacted_MissingDirectoryReturnsError(t *testing.T) {
	_, err := configexport.LoadRedacted(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected error for missing dir, got nil")
	}
}

func TestLoadRedacted_MalformedYAMLSurfacesAsError(t *testing.T) {
	// An accepted filename (policy.yaml) whose contents fail to parse
	// must surface as an error rather than landing in the bundle as
	// half-redacted text — the export loses its safety guarantee if
	// malformed YAML slips through.
	dir := writeConfigDir(t, map[string]string{
		"policy.yaml": "api_keys: [\n  - name: broken",
	})
	_, err := configexport.LoadRedacted(dir)
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
}

func TestWriteZip_ContainsManifestAndAllFiles(t *testing.T) {
	files := []configexport.File{
		{Name: "policy.yaml", Content: "policy: redacted\n"},
		{Name: "providers.yaml", Content: "providers: {}\n"},
	}
	for i := range files {
		files[i].SizeBytes = len(files[i].Content)
	}
	info := configexport.ManifestInfo{
		Version:   "v9.9.9-test",
		Hostname:  "test-host",
		ConfigDir: "/etc/slipspace/",
		Timestamp: time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	}
	var buf bytes.Buffer
	if err := configexport.WriteZip(&buf, files, info); err != nil {
		t.Fatalf("WriteZip: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
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
	for _, want := range []string{"policy.yaml", "providers.yaml"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("%s missing from archive", want)
		}
	}

	manifest := got["MANIFEST.txt"]
	for _, want := range []string{
		"SlipSpace Gateway Configuration Export",
		"v9.9.9-test",
		"test-host",
		"/etc/slipspace/",
		"Secrets:     redacted (***)",
		"policy.yaml",
		"providers.yaml",
		"2026-05-22T12:00:00Z",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing %q:\n%s", want, manifest)
		}
	}
}

func TestWriteZip_EmptyFileListStillWritesManifest(t *testing.T) {
	var buf bytes.Buffer
	if err := configexport.WriteZip(&buf, nil, configexport.ManifestInfo{Timestamp: time.Now()}); err != nil {
		t.Fatalf("WriteZip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	if len(zr.File) != 1 {
		names := make([]string, 0, len(zr.File))
		for _, zf := range zr.File {
			names = append(names, zf.Name)
		}
		sort.Strings(names)
		t.Fatalf("expected only MANIFEST.txt, got %v", names)
	}
	if zr.File[0].Name != "MANIFEST.txt" {
		t.Fatalf("expected MANIFEST.txt, got %q", zr.File[0].Name)
	}
}

func TestWriteZip_FailingWriterSurfacesError(t *testing.T) {
	files := []configexport.File{{Name: "a.yaml", Content: "a: b\n", SizeBytes: 4}}
	err := configexport.WriteZip(failingWriter{}, files, configexport.ManifestInfo{Timestamp: time.Now()})
	if err == nil {
		t.Fatal("expected error from failing writer, got nil")
	}
}

type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, io.ErrShortWrite }

func TestWriteZip_LargeIncompressibleBodyFailingWriterErrors(t *testing.T) {
	// Sanity check that a large (100 KB) incompressible body — the kind
	// where deflate can't shrink the payload enough to stay in zip's
	// internal buffers — still propagates an underlying-writer failure.
	body := make([]byte, 100<<10)
	r := newDeterministicRand()
	for i := range body {
		body[i] = byte(r.next() & 0xff)
	}
	files := []configexport.File{{Name: "big.bin", Content: string(body), SizeBytes: len(body)}}
	err := configexport.WriteZip(failingWriter{}, files, configexport.ManifestInfo{Timestamp: time.Now()})
	if err == nil {
		t.Fatal("expected error from failing writer with large body, got nil")
	}
}

// deterministicRand is a tiny LCG used to fill a buffer with incompressible
// bytes without pulling in crypto/rand. Determinism keeps the test
// reproducible across runs.
type deterministicRand struct{ state uint64 }

func newDeterministicRand() *deterministicRand { return &deterministicRand{state: 0xdeadbeefcafebabe} }

func (d *deterministicRand) next() uint64 {
	d.state = d.state*6364136223846793005 + 1442695040888963407
	return d.state
}
