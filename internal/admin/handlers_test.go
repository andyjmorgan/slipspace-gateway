package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	adminc "github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	"github.com/andyjmorgan/slipspace-gateway/internal/admin"
)

func TestAuthMeHandler_ReturnsJSON(t *testing.T) {
	h := admin.AuthMeHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["username"] != adminc.Username {
		t.Errorf("username = %q, want %q", body["username"], adminc.Username)
	}
}

func TestVersionHandler_ReturnsBuildVersion(t *testing.T) {
	h := admin.VersionHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Source builds default to "dev"; tagged release builds inject
	// the tag name via ldflags. Either is a non-empty string.
	if body["version"] == "" {
		t.Errorf(`version = ""; want non-empty (default "dev" or ldflags-injected tag)`)
	}
}

func TestDashboardSummaryHandler_EmptySnapshotter(t *testing.T) {
	// A handler built with a snapshotter that has no samples yet must
	// still return a well-shaped, decodable DashboardSummary so the SPA
	// renders without errors on first paint.
	h := admin.DashboardSummaryHandler(nil, []string{"openai", "anthropic"}, nil, nil, 24*time.Hour, 5*time.Minute, time.Now())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/summary", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got adminc.DashboardSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal into DashboardSummary: %v", err)
	}
	if got.Window != "24h" {
		t.Errorf("Window = %q, want 24h", got.Window)
	}
	if got.Totals.Requests != 0 {
		t.Errorf("Totals.Requests = %d, want 0 (no samples)", got.Totals.Requests)
	}
	if len(got.ProviderHealth) != 2 {
		t.Errorf("len(ProviderHealth) = %d, want 2 (one per configured provider)", len(got.ProviderHealth))
	}
	for _, p := range got.ProviderHealth {
		if !p.Healthy {
			t.Errorf("provider %q Healthy = false; want true on first paint", p.Provider)
		}
	}

	// SPA accesses .length and .map() on every slice field without
	// optional chaining — JSON null would crash the dashboard on first
	// paint. Walk the struct by reflection rather than enumerating the
	// fields by hand: the hand-written list this replaces silently
	// omitted TagsFired when that field was added, which is exactly the
	// regression the gate is supposed to catch. Reflection makes a new
	// slice field fail here until emptySummary initialises it.
	rv := reflect.ValueOf(got)
	for i := range rv.NumField() {
		f := rv.Field(i)
		if f.Kind() != reflect.Slice {
			continue
		}
		if f.IsNil() {
			t.Errorf("%s serialised as nil; SPA expects an empty array — initialise it in emptySummary",
				rv.Type().Field(i).Name)
		}
	}
}
