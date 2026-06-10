// Package config models the central telemetry service's own configuration:
// the Postgres DSN it stores into, the OTLP + HTTP listeners it binds, the
// HTTP Basic credentials guarding its console, and the registry of gateways
// allowed to push large payloads (each keyed by an HMAC secret).
//
// Unlike the gateway's config dir, the telemetry service takes a single YAML
// file. File contents are trusted (mounted from a Secret or a
// filesystem-permissioned path) — there is no ${VAR} / env: interpolation,
// matching the gateway's "only file paths are env-overridable" rule.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Default listener binds. HTTP serves the console + webhook ingest; OTLP
// takes the gen_ai + sluice telemetry feeds.
const (
	DefaultHTTPBind = "0.0.0.0:8686"
	DefaultOTLPBind = "0.0.0.0:8687"
)

// DefaultContentMaxBytes is the per-request gen_ai content cap applied when the
// operator does not set content_max_bytes. Content is a best-effort console
// aid (the audit copy is the Record feed), so it is bounded by default; an
// operator who wants the full content in the console raises this or sets it to
// 0 (unlimited).
const DefaultContentMaxBytes = 16 * 1024

// DefaultSpanFieldMaxBytes is the per-field cap the console's session-spans
// projection applies to served content (text / tool args / concatenated
// input_text / output_text) when the operator does not set
// span_field_max_bytes. 64 KiB per field keeps any realistic spans page to
// tens of MB even when the ingest content cap is disabled; the renderer's
// *_chars fields still carry the true uncapped sizes.
const DefaultSpanFieldMaxBytes = 64 * 1024

// Config is the root of the telemetry service's YAML file.
type Config struct {
	// HTTPBind is the listen address for the console + HMAC webhook ingest.
	HTTPBind string `yaml:"http_bind"`
	// OTLPBind is the listen address for the gen_ai + sluice OTLP feeds.
	OTLPBind string `yaml:"otlp_bind"`
	// ContentMaxBytes caps the per-request gen_ai content the OTLP ingest keeps
	// for the console, in bytes. nil applies DefaultContentMaxBytes; a value of
	// 0 (or negative) disables the cap so the full content is stored. A pointer
	// distinguishes "unset" (take the default) from an explicit 0 (unlimited).
	ContentMaxBytes *int `yaml:"content_max_bytes,omitempty"`
	// SpanFieldMaxBytes caps each served content field (text, tool args,
	// input_text, output_text) of the console's session-spans projection, in
	// bytes. nil applies DefaultSpanFieldMaxBytes; 0 (or negative) disables the
	// cap so fields are served whole. A pointer distinguishes "unset" (take the
	// default) from an explicit 0 (unlimited).
	SpanFieldMaxBytes *int `yaml:"span_field_max_bytes,omitempty"`
	// Postgres is the central store connection.
	Postgres Postgres `yaml:"postgres"`
	// Console carries the HTTP Basic credentials for the operator console.
	// Independent of any gateway's auth.
	Console Console `yaml:"console"`
	// Gateways is the registry of appliances permitted to push payloads.
	// A webhook is trusted iff its signature verifies against one of these.
	Gateways []Gateway `yaml:"gateways"`
}

// Postgres is the central store connection.
type Postgres struct {
	// DSN is the libpq/pgx connection string (e.g. postgres://user:pw@host/db).
	DSN string `yaml:"dsn"`
}

// Console carries the HTTP Basic credentials guarding the operator console.
type Console struct {
	// Username is the console login.
	Username string `yaml:"username"`
	// PasswordHash is the bcrypt hash of the console password. Storing a hash
	// (not the cleartext) keeps the secret-at-rest story consistent with the
	// gateway's api-key handling.
	PasswordHash string `yaml:"password_hash"`
}

// Gateway is one registered appliance allowed to push large payloads.
type Gateway struct {
	// ID is the stable gateway identifier echoed on its webhook requests and
	// carried on request events for stitching.
	ID string `yaml:"id"`
	// HMACSecret is the shared secret the gateway signs payloads with. Incoming
	// webhooks are trusted iff X-Sluice-Signature verifies against this.
	HMACSecret string `yaml:"hmac_secret"`
}

// ErrNoConfig is returned by Load when the path is empty.
var ErrNoConfig = errors.New("telemetry config: no config path provided")

// Load reads, parses, and validates the telemetry config file at path. It
// applies listener defaults before validating so an operator may omit them.
func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, ErrNoConfig
	}

	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied config path, same trust model as the gateway config dir
	if err != nil {
		return Config{}, fmt.Errorf("telemetry config: read %q: %w", path, err)
	}

	var cfg Config
	// KnownFields surfaces typos as errors rather than silently dropping a
	// misspelled key — the operator's config is small and hand-edited.
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("telemetry config: parse %q: %w", path, err)
	}

	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("telemetry config: %w", err)
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.HTTPBind == "" {
		c.HTTPBind = DefaultHTTPBind
	}
	if c.OTLPBind == "" {
		c.OTLPBind = DefaultOTLPBind
	}
}

// ContentCap resolves the per-request content cap in bytes: the operator's
// explicit value when set (including 0, which disables the cap), else
// DefaultContentMaxBytes. The ingest layer treats a result <= 0 as unlimited.
func (c Config) ContentCap() int {
	if c.ContentMaxBytes == nil {
		return DefaultContentMaxBytes
	}
	return *c.ContentMaxBytes
}

// SpanFieldCap resolves the session-spans per-field content cap in bytes: the
// operator's explicit value when set (including 0, which disables the cap),
// else DefaultSpanFieldMaxBytes. The console projection treats a result <= 0
// as unlimited.
func (c Config) SpanFieldCap() int {
	if c.SpanFieldMaxBytes == nil {
		return DefaultSpanFieldMaxBytes
	}
	return *c.SpanFieldMaxBytes
}

// Validate enforces the invariants the service depends on at startup: a store
// to write to, console credentials to guard it, and a well-formed,
// duplicate-free gateway registry.
func (c Config) Validate() error {
	if c.Postgres.DSN == "" {
		return errors.New("postgres.dsn is required")
	}
	if c.Console.Username == "" {
		return errors.New("console.username is required")
	}
	if c.Console.PasswordHash == "" {
		return errors.New("console.password_hash is required")
	}

	seen := make(map[string]struct{}, len(c.Gateways))
	for i, g := range c.Gateways {
		if g.ID == "" {
			return fmt.Errorf("gateways[%d]: id is required", i)
		}
		if g.HMACSecret == "" {
			return fmt.Errorf("gateways[%d] (%s): hmac_secret is required", i, g.ID)
		}
		if _, dup := seen[g.ID]; dup {
			return fmt.Errorf("gateways[%d]: duplicate id %q", i, g.ID)
		}
		seen[g.ID] = struct{}{}
	}
	return nil
}
