package config

import (
	"strings"
	"testing"
)

func TestRedacted_ZeroesAllSecrets(t *testing.T) {
	cfg := Config{
		Postgres: Postgres{DSN: "postgres://svc:supersecret@db:5432/telemetry?sslmode=require"}, //nolint:gosec // test fixture: the whole point is to prove this password is redacted
		Console:  Console{Username: "admin", PasswordHash: "$2a$10$bcrypthashvalue"},
		Gateways: []Gateway{
			{ID: "gw1", HMACSecret: "hmac-one"},
			{ID: "gw2", HMACSecret: "hmac-two"},
		},
		Scanner: Scanner{
			Enabled:     true,
			EvidenceKey: "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=",
			Detectors:   []Detector{{CheckType: "injection", Endpoint: "http://d/detect"}},
		},
	}
	r := cfg.Redacted()

	if got, _ := dsnPassword(r.Postgres.DSN); got != RedactedPlaceholder {
		t.Errorf("dsn password = %q, want redacted", got)
	}
	if !strings.Contains(r.Postgres.DSN, "svc:") || !strings.Contains(r.Postgres.DSN, "@db:5432/telemetry") {
		t.Errorf("dsn lost its non-secret parts: %q", r.Postgres.DSN)
	}
	if r.Console.PasswordHash != RedactedPlaceholder {
		t.Errorf("password hash = %q", r.Console.PasswordHash)
	}
	if r.Scanner.EvidenceKey != RedactedPlaceholder {
		t.Errorf("evidence key = %q", r.Scanner.EvidenceKey)
	}
	for i, g := range r.Gateways {
		if g.HMACSecret != RedactedPlaceholder {
			t.Errorf("gateway[%d] hmac = %q", i, g.HMACSecret)
		}
		if g.ID == "" {
			t.Errorf("gateway[%d] id was dropped", i)
		}
	}

	// The original config must be untouched (Redacted copies, never mutates).
	if cfg.Console.PasswordHash == RedactedPlaceholder || cfg.Gateways[0].HMACSecret == RedactedPlaceholder {
		t.Error("Redacted mutated the source config")
	}
	if strings.Contains(cfg.Postgres.DSN, RedactedPlaceholder) {
		t.Error("Redacted mutated the source DSN")
	}
}

func TestRedacted_EmptyAndNoSecrets(t *testing.T) {
	// Empty config: nothing to redact, no panics.
	r := Config{}.Redacted()
	if r.Console.PasswordHash != "" || r.Postgres.DSN != "" {
		t.Errorf("empty config gained values: %+v", r)
	}
	// A DSN with no password is left intact.
	r2 := Config{Postgres: Postgres{DSN: "postgres://svc@db/telemetry"}}.Redacted()
	if r2.Postgres.DSN != "postgres://svc@db/telemetry" {
		t.Errorf("password-less dsn was altered: %q", r2.Postgres.DSN)
	}
}

func TestRedactDSNPassword_Unparseable(t *testing.T) {
	// A keyword/value DSN is not a URL — redact wholesale rather than risk a leak.
	got := redactDSNPassword("host=db user=svc password=secret dbname=telemetry")
	if strings.Contains(got, "secret") {
		t.Errorf("keyword/value dsn leaked: %q", got)
	}
}

// dsnPassword extracts the password from a URL-style DSN for assertions.
func dsnPassword(dsn string) (string, bool) {
	at := strings.Index(dsn, "@")
	scheme := strings.Index(dsn, "://")
	if at < 0 || scheme < 0 {
		return "", false
	}
	userinfo := dsn[scheme+3 : at]
	colon := strings.Index(userinfo, ":")
	if colon < 0 {
		return "", false
	}
	return userinfo[colon+1:], true
}
