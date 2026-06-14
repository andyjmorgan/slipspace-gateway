package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/config"
)

// newSettingsServer builds a console server with a redacted applied-config
// snapshot attached, so the settings route is registered.
func newSettingsServer(t *testing.T, cfg config.Config) http.Handler {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("hunter2"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	console := config.Console{Username: "admin", PasswordHash: string(hash)}
	return New(console, stubPinger{}, &fakeQueries{}, nil, config.DefaultSpanFieldMaxBytes, discardLogger()).
		WithAppliedConfig(cfg.Redacted()).
		Handler()
}

func sampleConfig() config.Config {
	return config.Config{
		HTTPBind: "0.0.0.0:8686",
		OTLPBind: "0.0.0.0:8687",
		Postgres: config.Postgres{DSN: "postgres://svc:supersecret@db:5432/telemetry?sslmode=require"}, //nolint:gosec // test fixture: the whole point is to prove this password is redacted
		Console:  config.Console{Username: "admin", PasswordHash: "$2a$10$abcdefghijklmnopqrstuv"},
		Gateways: []config.Gateway{{ID: "gw1", HMACSecret: "topsecret"}},
		Scanner: config.Scanner{
			Enabled:     true,
			EvidenceKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			Detectors:   []config.Detector{{CheckType: "injection", Endpoint: "http://d/detect"}},
		},
	}
}

func TestHandleSettings_Redacted(t *testing.T) {
	h := newSettingsServer(t, sampleConfig())
	resp := get(t, h, "/api/v1/settings", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var body admin.SettingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	// The whole document must carry NO secret value anywhere.
	full := mustJSON(t, body.Config)
	for _, secret := range []string{"supersecret", "topsecret", "$2a$10$abcdefghijklmnopqrstuv", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="} {
		if strings.Contains(full, secret) {
			t.Errorf("settings leaked secret %q in %s", secret, full)
		}
	}
	// Non-secret facts survive (keys are snake_case from the yaml tags).
	for _, want := range []string{"http_bind", "0.0.0.0:8686", "scanner", "injection"} {
		if !strings.Contains(full, want) {
			t.Errorf("settings missing %q in %s", want, full)
		}
	}
	// The placeholder is present where secrets were.
	if !strings.Contains(full, config.RedactedPlaceholder) {
		t.Errorf("expected redaction placeholder in %s", full)
	}
}

func TestHandleSettings_RequiresAuth(t *testing.T) {
	h := newSettingsServer(t, sampleConfig())
	if resp := get(t, h, "/api/v1/settings", false); resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d, want 401", resp.Code)
	}
}

func TestHandleSettings_OmittedWhenUnset(t *testing.T) {
	// No WithAppliedConfig -> route not registered -> falls through to the SPA
	// shell (200 from the catch-all), never the JSON settings body.
	hash, _ := bcrypt.GenerateFromPassword([]byte("hunter2"), bcrypt.MinCost)
	h := New(config.Console{Username: "admin", PasswordHash: string(hash)}, stubPinger{}, &fakeQueries{}, nil, config.DefaultSpanFieldMaxBytes, discardLogger()).Handler()
	resp := get(t, h, "/api/v1/settings", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	if strings.Contains(resp.Body.String(), "\"config\"") {
		t.Errorf("settings route should be unregistered; got JSON body %s", resp.Body.String())
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
