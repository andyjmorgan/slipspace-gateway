package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/admin"
)

func newServer(t *testing.T, password string) *httptest.Server {
	t.Helper()
	meters, _ := newMeters(t)
	h := admin.NewMux(admin.MuxOptions{
		Password: password,
		Meters:   meters,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestMux_SPAIsUnauthenticated(t *testing.T) {
	srv := newServer(t, "secret")
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestMux_SPADeepLinkServedWithoutAuth(t *testing.T) {
	srv := newServer(t, "secret")
	resp, err := http.Get(srv.URL + "/dashboard")
	if err != nil {
		t.Fatalf("GET /dashboard: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestMux_APIRequiresBasicAuth(t *testing.T) {
	srv := newServer(t, "secret")
	resp, err := http.Get(srv.URL + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /api/v1/auth/me: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestMux_APIWithGoodCredsReturnsJSON(t *testing.T) {
	srv := newServer(t, "secret")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/auth/me", nil)
	req.SetBasicAuth("admin", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/auth/me: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestMux_DashboardSummaryAuthed(t *testing.T) {
	// With no snapshotter configured, the handler returns a well-shaped
	// empty summary. End-to-end "real numbers in the response" is
	// exercised in the e2e suite where the gateway feeds a live
	// snapshotter; here we only verify the wire contract decodes.
	srv := newServer(t, "secret")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/dashboard/summary", nil)
	req.SetBasicAuth("admin", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/dashboard/summary: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got adminc.DashboardSummary
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Window == "" {
		t.Error("Window = empty")
	}
}

func TestMux_DashboardSummaryRequiresAuth(t *testing.T) {
	srv := newServer(t, "secret")
	resp, err := http.Get(srv.URL + "/api/v1/dashboard/summary")
	if err != nil {
		t.Fatalf("GET /api/v1/dashboard/summary: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
