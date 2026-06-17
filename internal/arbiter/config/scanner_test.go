package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func validKey() string { return base64.StdEncoding.EncodeToString(make([]byte, 32)) }

// base returns a Config valid except for the scanner block, so scanner-branch
// Validate tests are isolated.
func base() Config {
	return Config{
		Postgres: Postgres{DSN: "postgres://x"},
		Console:  Console{Username: "u", PasswordHash: "h"},
	}
}

func TestScannerHelpers(t *testing.T) {
	if (Config{}).ScannerEnabled() {
		t.Error("disabled by default")
	}
	c := Config{Scanner: Scanner{Enabled: true}}
	if !c.ScannerEnabled() {
		t.Error("enabled")
	}
	// workers: default / explicit / non-positive
	if (Config{}).ScannerWorkers() != DefaultScannerWorkers {
		t.Error("default workers")
	}
	w := 8
	if (Config{Scanner: Scanner{Workers: &w}}).ScannerWorkers() != 8 {
		t.Error("explicit workers")
	}
	zero := 0
	if (Config{Scanner: Scanner{Workers: &zero}}).ScannerWorkers() != DefaultScannerWorkers {
		t.Error("non-positive workers -> default")
	}
	// retention: default / explicit / non-positive
	if (Config{}).ScannerRetentionDays() != DefaultScannerRetentionDays {
		t.Error("default retention")
	}
	r := 5
	if (Config{Scanner: Scanner{RetentionDays: &r}}).ScannerRetentionDays() != 5 {
		t.Error("explicit retention")
	}
	if (Config{Scanner: Scanner{RetentionDays: &zero}}).ScannerRetentionDays() != DefaultScannerRetentionDays {
		t.Error("non-positive retention -> default")
	}
}

func TestApplyDefaults_UnmappedSeverity(t *testing.T) {
	// Omitted unmapped level defaults to warning.
	c := base()
	c.applyDefaults()
	if c.Scanner.Severity.Unmapped != SeverityWarning {
		t.Errorf("default unmapped = %q, want %q", c.Scanner.Severity.Unmapped, SeverityWarning)
	}
	// An explicit level is left untouched.
	c2 := base()
	c2.Scanner.Severity.Unmapped = SeverityError
	c2.applyDefaults()
	if c2.Scanner.Severity.Unmapped != SeverityError {
		t.Errorf("explicit unmapped overwritten: got %q", c2.Scanner.Severity.Unmapped)
	}
}

func TestEvidenceKeyBytes(t *testing.T) {
	if _, err := (Config{Scanner: Scanner{EvidenceKey: validKey()}}).EvidenceKeyBytes(); err != nil {
		t.Errorf("valid key: %v", err)
	}
	if _, err := (Config{Scanner: Scanner{EvidenceKey: "!!not-base64"}}).EvidenceKeyBytes(); err == nil {
		t.Error("bad base64")
	}
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := (Config{Scanner: Scanner{EvidenceKey: short}}).EvidenceKeyBytes(); err == nil {
		t.Error("wrong length")
	}
}

func TestValidate_Scanner(t *testing.T) {
	det := Detector{CheckType: "injection", Endpoint: "http://d", Family: "f"}
	cases := []struct {
		name    string
		scanner Scanner
		wantErr string
	}{
		{"disabled ok", Scanner{Enabled: false}, ""},
		{"no detectors", Scanner{Enabled: true, EvidenceKey: validKey()}, "detectors is required"},
		{"bad key", Scanner{Enabled: true, EvidenceKey: "!!", Detectors: []Detector{det}}, "evidence_key"},
		{"missing check_type", Scanner{Enabled: true, EvidenceKey: validKey(), Detectors: []Detector{{Endpoint: "http://d"}}}, "check_type is required"},
		{"missing endpoint", Scanner{Enabled: true, EvidenceKey: validKey(), Detectors: []Detector{{CheckType: "injection"}}}, "endpoint is required"},
		{"duplicate check_type", Scanner{Enabled: true, EvidenceKey: validKey(), Detectors: []Detector{det, det}}, "duplicate check_type"},
		{"valid", Scanner{Enabled: true, EvidenceKey: validKey(), Detectors: []Detector{det}}, ""},
		// scan-filter validation (only reached when enabled + detectors valid).
		{"bad include glob", Scanner{Enabled: true, EvidenceKey: validKey(), Detectors: []Detector{det}, ScanTags: ScanTags{Include: []string{"["}}}, "scan_tags.include"},
		{"bad exclude glob", Scanner{Enabled: true, EvidenceKey: validKey(), Detectors: []Detector{det}, ScanTags: ScanTags{Exclude: []string{"a[b"}}}, "scan_tags.exclude"},
		{"empty include glob", Scanner{Enabled: true, EvidenceKey: validKey(), Detectors: []Detector{det}, ScanTags: ScanTags{Include: []string{""}}}, "empty pattern"},
		{"bad finding_exclude glob", Scanner{Enabled: true, EvidenceKey: validKey(), Detectors: []Detector{det}, FindingExclude: []string{"[bad"}}, "finding_exclude"},
		{"bad unmapped level", Scanner{Enabled: true, EvidenceKey: validKey(), Detectors: []Detector{det}, Severity: Severity{Unmapped: "critical"}}, "severity.unmapped"},
		{"severity rule missing match", Scanner{Enabled: true, EvidenceKey: validKey(), Detectors: []Detector{det}, Severity: Severity{Rules: []SeverityRule{{Level: "info"}}}}, "match is required"},
		{"severity rule bad glob", Scanner{Enabled: true, EvidenceKey: validKey(), Detectors: []Detector{det}, Severity: Severity{Rules: []SeverityRule{{Match: "a[b", Level: "info"}}}}, "severity.rules[0]"},
		{"severity rule bad level", Scanner{Enabled: true, EvidenceKey: validKey(), Detectors: []Detector{det}, Severity: Severity{Rules: []SeverityRule{{Match: "pii.*", Level: "nope"}}}}, "invalid level"},
		{"valid scan filters", Scanner{Enabled: true, EvidenceKey: validKey(), Detectors: []Detector{det}, ScanTags: ScanTags{Include: []string{"tier:*"}, Exclude: []string{"env:internal"}}, FindingExclude: []string{"pii.url"}, Severity: Severity{Unmapped: "warning", Rules: []SeverityRule{{Match: "pii.url", Level: "info"}, {Match: "pii.*", Level: "warning"}}}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := base()
			cfg.Scanner = c.scanner
			err := cfg.Validate()
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("want ok, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want contains %q", err, c.wantErr)
			}
		})
	}
}
