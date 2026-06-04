package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeConfig writes body to a temp file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

//nolint:gosec // test fixture, not a real credential
const validBody = `
postgres:
  dsn: postgres://u:p@localhost:5432/telemetry
console:
  username: admin
  password_hash: $2a$10$abcdefghijklmnopqrstuv
gateways:
  - id: gw-a
    hmac_secret: secret-a
  - id: gw-b
    hmac_secret: secret-b
`

func TestLoad_Valid(t *testing.T) {
	cfg, err := Load(writeConfig(t, validBody))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPBind != DefaultHTTPBind {
		t.Errorf("HTTPBind = %q, want default %q", cfg.HTTPBind, DefaultHTTPBind)
	}
	if cfg.OTLPBind != DefaultOTLPBind {
		t.Errorf("OTLPBind = %q, want default %q", cfg.OTLPBind, DefaultOTLPBind)
	}
	if len(cfg.Gateways) != 2 {
		t.Fatalf("Gateways len = %d, want 2", len(cfg.Gateways))
	}
	if cfg.Gateways[0].ID != "gw-a" || cfg.Gateways[0].HMACSecret != "secret-a" {
		t.Errorf("gateway[0] = %+v", cfg.Gateways[0])
	}
}

func TestLoad_OverridesDefaults(t *testing.T) {
	body := validBody + "http_bind: 127.0.0.1:9000\notlp_bind: 127.0.0.1:9001\n"
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPBind != "127.0.0.1:9000" {
		t.Errorf("HTTPBind = %q", cfg.HTTPBind)
	}
	if cfg.OTLPBind != "127.0.0.1:9001" {
		t.Errorf("OTLPBind = %q", cfg.OTLPBind)
	}
}

func TestLoad_EmptyPath(t *testing.T) {
	if _, err := Load(""); !errors.Is(err, ErrNoConfig) {
		t.Fatalf("Load(\"\") err = %v, want ErrNoConfig", err)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("Load of missing file: want error")
	}
}

func TestLoad_BadYAML(t *testing.T) {
	if _, err := Load(writeConfig(t, "postgres: [::::")); err == nil {
		t.Fatal("Load of bad yaml: want error")
	}
}

func TestLoad_UnknownField(t *testing.T) {
	if _, err := Load(writeConfig(t, validBody+"bogus: 1\n")); err == nil {
		t.Fatal("Load with unknown field: want error")
	}
}

func TestLoad_ValidationErrors(t *testing.T) {
	cases := map[string]string{ //nolint:gosec // test fixtures, not real credentials
		"no dsn": `
console: {username: a, password_hash: h}
`,
		"no username": `
postgres: {dsn: x}
console: {password_hash: h}
`,
		"no password hash": `
postgres: {dsn: x}
console: {username: a}
`,
		"gateway no id": `
postgres: {dsn: x}
console: {username: a, password_hash: h}
gateways:
  - hmac_secret: s
`,
		"gateway no secret": `
postgres: {dsn: x}
console: {username: a, password_hash: h}
gateways:
  - id: g
`,
		"duplicate gateway id": `
postgres: {dsn: x}
console: {username: a, password_hash: h}
gateways:
  - {id: g, hmac_secret: s1}
  - {id: g, hmac_secret: s2}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, body)); err == nil {
				t.Fatalf("Load(%s): want validation error", name)
			}
		})
	}
}
