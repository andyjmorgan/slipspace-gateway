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
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path"

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
	// Scanner is the optional Arbiter async security scanner. Disabled by
	// default — the telemetry service is fully functional without it.
	Scanner Scanner `yaml:"scanner"`
}

// Scanner configures the optional Arbiter async security scanner: the detector
// endpoints it calls, the evidence-store key, and retention. When Enabled is
// false (the default) the ingest path is untouched and no scanner goroutines
// run.
type Scanner struct {
	// Enabled turns the scanner on. Default false.
	Enabled bool `yaml:"enabled"`
	// Detectors is the set of detector endpoints, one per check type. The
	// explode step only emits check_tasks for the check types listed here, and
	// the dispatcher only calls these endpoints.
	Detectors []Detector `yaml:"detectors"`
	// Workers is the dispatcher's concurrent worker count. nil applies
	// DefaultScannerWorkers.
	Workers *int `yaml:"workers,omitempty"`
	// EvidenceKey is the base64-encoded 32-byte AES-256 key the evidence store
	// encrypts offending fields with (ADR-018 — the store is a PII
	// concentrator). Required when Enabled.
	EvidenceKey string `yaml:"evidence_key"`
	// RetentionDays bounds how long evidence rows are kept. nil applies
	// DefaultScannerRetentionDays.
	RetentionDays *int `yaml:"retention_days,omitempty"`
	// EnrichedExport optionally re-emits the verdict as an enriched OTLP span
	// (ADR-002). Disabled when Endpoint is empty (the default).
	EnrichedExport EnrichedExport `yaml:"enriched_export"`
	// ScanTags scopes which spans are scanned by their request tags. Empty
	// Include scans all spans; Exclude wins over Include. Off by default
	// (no constraint) — the gate lives in Explode.
	ScanTags ScanTags `yaml:"scan_tags,omitempty"`
	// FindingExclude drops findings whose category matches any of these globs
	// before they are persisted — exclude-only result filtering (e.g. keep PII
	// detection but suppress "pii.url"). Applied in storeFindings.
	FindingExclude []string `yaml:"finding_exclude,omitempty"`
	// Severity classifies surviving findings into info/warning/error, stamped
	// on the finding and rolled up (max) onto the span verdict.
	Severity Severity `yaml:"severity,omitempty"`
}

// ScanTags scopes scanning by request tag. Patterns are full globs
// (path.Match); tags carry no '/', so '*' spans the whole value.
type ScanTags struct {
	// Include, when non-empty, restricts scanning to spans carrying a tag that
	// matches one of these globs. Empty includes all spans. A span with no tags
	// matches no include glob, so a non-empty Include excludes untagged spans.
	Include []string `yaml:"include,omitempty"`
	// Exclude skips spans carrying a tag that matches one of these globs. It
	// wins over Include — a span matching both is not scanned.
	Exclude []string `yaml:"exclude,omitempty"`
}

// Severity maps finding categories to a level. Rules are evaluated in order and
// first match wins, so an operator orders specific globs before general ones
// ("pii.url"→info before "pii.*"→warning).
type Severity struct {
	// Unmapped is the level applied to a surviving finding whose category
	// matches no rule. Defaults to SeverityWarning (applyDefaults).
	Unmapped string `yaml:"unmapped,omitempty"`
	// Rules is the ordered category→level table (first match wins).
	Rules []SeverityRule `yaml:"rules,omitempty"`
}

// SeverityRule assigns Level to findings whose category matches Match (full
// glob, path.Match).
type SeverityRule struct {
	// Match is the category glob.
	Match string `yaml:"match"`
	// Level is the assigned severity: SeverityInfo, SeverityWarning, or
	// SeverityError.
	Level string `yaml:"level"`
}

// Severity levels, ranked info < warning < error. The canonical set lives here
// because config validates it; the arbiter ranks them for verdict roll-up.
const (
	// SeverityInfo is the lowest severity.
	SeverityInfo = "info"
	// SeverityWarning is the middle severity and the default for unmapped
	// categories.
	SeverityWarning = "warning"
	// SeverityError is the highest severity.
	SeverityError = "error"
)

// EnrichedExport configures the optional re-emission of verdicts as enriched
// OTLP spans (ADR-002 — findings travel back over OTel, never a separate
// stream). When Endpoint is empty the emitter is a no-op.
type EnrichedExport struct {
	// Endpoint is the OTLP collector endpoint to export enriched spans to.
	// Empty disables enriched emit.
	Endpoint string `yaml:"endpoint"`
	// Protocol selects the OTLP transport: "grpc" (default) or "http".
	Protocol string `yaml:"protocol"`
}

// Detector is one detector endpoint Arbiter calls for a given check type.
type Detector struct {
	// CheckType is the logical check this detector serves: "injection",
	// "toxicity", "pii". Unique within Detectors.
	CheckType string `yaml:"check_type"`
	// Family is the detector family name recorded as provenance.
	Family string `yaml:"family"`
	// Endpoint is the detector's HTTP /detect URL (protojson).
	Endpoint string `yaml:"endpoint"`
	// Threshold is the emit floor passed to the detector; below it, findings
	// are not returned. 0 lets the detector use its own default.
	Threshold float32 `yaml:"threshold"`
}

// Scanner defaults.
const (
	// DefaultScannerWorkers is the dispatcher's worker-pool size when unset.
	DefaultScannerWorkers = 4
	// DefaultScannerRetentionDays bounds evidence retention when unset.
	DefaultScannerRetentionDays = 30
)

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
	// Unmapped finding categories default to warning — a safe middle ground the
	// operator can override down to info or up to error.
	if c.Scanner.Severity.Unmapped == "" {
		c.Scanner.Severity.Unmapped = SeverityWarning
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

// ScannerEnabled reports whether the Arbiter security scanner is on.
func (c Config) ScannerEnabled() bool { return c.Scanner.Enabled }

// ScannerWorkers resolves the dispatcher worker count.
func (c Config) ScannerWorkers() int {
	if c.Scanner.Workers == nil || *c.Scanner.Workers <= 0 {
		return DefaultScannerWorkers
	}
	return *c.Scanner.Workers
}

// ScannerRetentionDays resolves the evidence retention window in days.
func (c Config) ScannerRetentionDays() int {
	if c.Scanner.RetentionDays == nil || *c.Scanner.RetentionDays <= 0 {
		return DefaultScannerRetentionDays
	}
	return *c.Scanner.RetentionDays
}

// EvidenceKeyBytes decodes the base64 AES-256 evidence key. Returns an error
// unless the decoded key is exactly 32 bytes.
func (c Config) EvidenceKeyBytes() ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(c.Scanner.EvidenceKey)
	if err != nil {
		return nil, fmt.Errorf("scanner.evidence_key: not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("scanner.evidence_key: must decode to 32 bytes, got %d", len(key))
	}
	return key, nil
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

	if c.Scanner.Enabled {
		if len(c.Scanner.Detectors) == 0 {
			return errors.New("scanner.detectors is required when scanner.enabled")
		}
		if _, err := c.EvidenceKeyBytes(); err != nil {
			return err
		}
		seenCheck := make(map[string]struct{}, len(c.Scanner.Detectors))
		for i, d := range c.Scanner.Detectors {
			if d.CheckType == "" {
				return fmt.Errorf("scanner.detectors[%d]: check_type is required", i)
			}
			if d.Endpoint == "" {
				return fmt.Errorf("scanner.detectors[%d] (%s): endpoint is required", i, d.CheckType)
			}
			if _, dup := seenCheck[d.CheckType]; dup {
				return fmt.Errorf("scanner.detectors[%d]: duplicate check_type %q", i, d.CheckType)
			}
			seenCheck[d.CheckType] = struct{}{}
		}
		if err := validateScanFilters(c.Scanner); err != nil {
			return err
		}
	}
	return nil
}

// validateScanFilters checks the tag-selection, finding-exclude, and severity
// globs/levels. A bad glob silently never matches, so it is rejected at startup
// rather than degrading scan scoping unnoticed.
func validateScanFilters(s Scanner) error {
	for _, p := range s.ScanTags.Include {
		if err := validGlob(p); err != nil {
			return fmt.Errorf("scanner.scan_tags.include: %w", err)
		}
	}
	for _, p := range s.ScanTags.Exclude {
		if err := validGlob(p); err != nil {
			return fmt.Errorf("scanner.scan_tags.exclude: %w", err)
		}
	}
	for _, p := range s.FindingExclude {
		if err := validGlob(p); err != nil {
			return fmt.Errorf("scanner.finding_exclude: %w", err)
		}
	}
	if u := s.Severity.Unmapped; u != "" && !validSeverityLevel(u) {
		return fmt.Errorf("scanner.severity.unmapped: invalid level %q (want info|warning|error)", u)
	}
	for i, r := range s.Severity.Rules {
		if r.Match == "" {
			return fmt.Errorf("scanner.severity.rules[%d]: match is required", i)
		}
		if err := validGlob(r.Match); err != nil {
			return fmt.Errorf("scanner.severity.rules[%d]: %w", i, err)
		}
		if !validSeverityLevel(r.Level) {
			return fmt.Errorf("scanner.severity.rules[%d] (%s): invalid level %q (want info|warning|error)", i, r.Match, r.Level)
		}
	}
	return nil
}

// validGlob reports whether p is a non-empty, well-formed path.Match pattern.
func validGlob(p string) error {
	if p == "" {
		return errors.New("empty pattern")
	}
	// path.Match only returns ErrBadPattern for a malformed pattern; the match
	// result against a probe string is irrelevant here.
	if _, err := path.Match(p, ""); err != nil {
		return fmt.Errorf("invalid glob %q: %w", p, err)
	}
	return nil
}

// validSeverityLevel reports whether l is one of the canonical severity levels.
func validSeverityLevel(l string) bool {
	switch l {
	case SeverityInfo, SeverityWarning, SeverityError:
		return true
	default:
		return false
	}
}
