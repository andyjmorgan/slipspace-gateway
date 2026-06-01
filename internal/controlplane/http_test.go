package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFleetHTTPHandler_StatusDerivation(t *testing.T) {
	reg := NewMemoryRegistry()
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	// Three gateways last seen at different ages relative to "now".
	cases := map[string]time.Time{
		"gw-online":  base.Add(-10 * time.Second), // < 45s  -> online
		"gw-stale":   base.Add(-60 * time.Second), // 45-120 -> stale
		"gw-offline": base.Add(-5 * time.Minute),  // > 120s -> offline
	}
	for id, seen := range cases {
		reg.now = fixedClock(seen)
		if _, err := reg.Register(context.Background(), RegisterInput{ID: id, Version: "v1"}); err != nil {
			t.Fatal(err)
		}
	}

	h := NewFleetHTTPHandler(reg, 45*time.Second, 120*time.Second)
	h.now = fixedClock(base)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/fleet", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}

	var views []GatewayView
	if err := json.NewDecoder(rec.Body).Decode(&views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]string{}
	for _, v := range views {
		got[v.ID] = v.Status
	}
	want := map[string]string{
		"gw-online":  StatusOnline,
		"gw-stale":   StatusStale,
		"gw-offline": StatusOffline,
	}
	for id, status := range want {
		if got[id] != status {
			t.Fatalf("%s status = %q, want %q", id, got[id], status)
		}
	}
}

func TestFleetHTTPHandler_MethodNotAllowed(t *testing.T) {
	h := NewFleetHTTPHandler(NewMemoryRegistry(), time.Second, time.Minute)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/fleet", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestFleetHTTPHandler_RegistryError(t *testing.T) {
	h := NewFleetHTTPHandler(errRegistry{err: errors.New("boom")}, time.Second, time.Minute)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/fleet", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
