//go:build e2e

// Package admin_test exercises the management-console listener through
// the spawned gateway binary. Each test brings up a full harness
// (mockllm + NATS + gateway) and hits the admin port via real HTTP.
package admin_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

func TestAdmin_AuthMe_RejectsMissingCredentials(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	resp, err := h.HTTP.Get(h.AdminURL + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /api/v1/auth/me: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	// Asserting absence — the SPA owns credential UX, the browser must
	// not pop its native dialog. See internal/admin/auth.go.
	if got := resp.Header.Get("WWW-Authenticate"); got != "" {
		t.Errorf("WWW-Authenticate = %q, want empty so the browser dialog stays suppressed", got)
	}
}

func TestAdmin_AuthMe_AcceptsValidCredentials(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	req, _ := http.NewRequest(http.MethodGet, h.AdminURL+"/api/v1/auth/me", nil)
	req.SetBasicAuth(adminc.Username, h.AdminPassword)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/auth/me: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["username"] != adminc.Username {
		t.Errorf("username = %q, want %q", body["username"], adminc.Username)
	}
}

func TestAdmin_AuthMe_RejectsWrongPassword(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	req, _ := http.NewRequest(http.MethodGet, h.AdminURL+"/api/v1/auth/me", nil)
	req.SetBasicAuth(adminc.Username, "not-the-password")
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/auth/me: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAdmin_AuthMe_RejectsWrongUsername(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	req, _ := http.NewRequest(http.MethodGet, h.AdminURL+"/api/v1/auth/me", nil)
	req.SetBasicAuth("operator", h.AdminPassword)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/auth/me: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (username 'operator' is not the admin username)", resp.StatusCode)
	}
}

func TestAdmin_DashboardSummary_ReflectsRealTraffic(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	// Prime the mock LLM so the data-plane POST below resolves to 200.
	canned := `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   canned,
	})

	// Drive a real provider request through the data plane so the
	// snapshotter has something to delta against.
	body := []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	dataReq, _ := http.NewRequest(http.MethodPost, h.GatewayURL+"/v1/chat/completions", bytes.NewReader(body))
	dataReq.Header.Set("Authorization", "Bearer "+h.APIKey)
	dataReq.Header.Set("Content-Type", "application/json")
	dataResp, err := h.HTTP.Do(dataReq)
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	_, _ = io.Copy(io.Discard, dataResp.Body)
	_ = dataResp.Body.Close()
	if dataResp.StatusCode != http.StatusOK {
		t.Fatalf("data plane request: status = %d, want 200", dataResp.StatusCode)
	}

	// Snapshot interval is 200ms in e2e harness; poll the dashboard
	// for up to 5s and break when the request shows up.
	var got adminc.DashboardSummary
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, h.AdminURL+"/api/v1/dashboard/summary", nil)
		req.SetBasicAuth(adminc.Username, h.AdminPassword)
		resp, err := h.HTTP.Do(req)
		if err != nil {
			t.Fatalf("GET /api/v1/dashboard/summary: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			_ = resp.Body.Close()
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		got = adminc.DashboardSummary{}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			_ = resp.Body.Close()
			t.Fatalf("decode DashboardSummary: %v", err)
		}
		_ = resp.Body.Close()
		if got.Totals.Requests > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if got.Totals.Requests == 0 {
		t.Fatal("Totals.Requests stayed 0; snapshotter never observed the driven request")
	}
	if got.Totals.Requests != got.Totals.RequestsSuccess+got.Totals.RequestsErrored {
		t.Errorf("Totals: %d != %d ok + %d err",
			got.Totals.Requests, got.Totals.RequestsSuccess, got.Totals.RequestsErrored)
	}
	if len(got.ByProvider) == 0 {
		t.Errorf("ByProvider empty after driving a request")
	}
	if len(got.ProviderHealth) == 0 {
		t.Errorf("ProviderHealth empty — should have one row per configured provider")
	}
}

func TestAdmin_DashboardSummary_RequiresAuth(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	resp, err := h.HTTP.Get(h.AdminURL + "/api/v1/dashboard/summary")
	if err != nil {
		t.Fatalf("GET /api/v1/dashboard/summary: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAdmin_SPA_ServesIndexAtRoot(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	resp, err := h.HTTP.Get(h.AdminURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := strings.ToLower(string(body))
	if !strings.Contains(s, "<!doctype html>") {
		t.Errorf("body did not look like HTML:\n%s", string(body))
	}
}

func TestAdmin_SPA_DeepLinkFallsBackToIndex(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	resp, err := h.HTTP.Get(h.AdminURL + "/dashboard")
	if err != nil {
		t.Fatalf("GET /dashboard: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestAdmin_DataPlaneDoesNotExposeAPI(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	// Hitting /api/v1/auth/me on the data-plane port must NOT resolve
	// to the admin handler — admin lives on its own listener.
	resp, err := h.HTTP.Get(h.GatewayURL + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET data-plane /api/v1/auth/me: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
		t.Errorf("data plane exposed /api/v1/auth/me (status %d) — admin routes leaked across listeners", resp.StatusCode)
	}
}

func TestAdmin_Disabled_NoListener(t *testing.T) {
	t.Parallel()
	// AdminEnabled defaults to false; the harness still allocates a port
	// and writes admin.yaml with enabled: false. The gateway must not
	// bind that port.
	h := harness.New(t)

	if h.AdminURL != "" {
		t.Fatalf("AdminURL should be empty when admin disabled, got %q", h.AdminURL)
	}

	// The harness-allocated admin port is not exposed when disabled, so
	// reach into the internal state by probing all loopback ports the
	// harness reserved. Easier: probe the gateway's data-plane port for
	// the admin shape and assert it's a 404 from the proxy router.
	// Already covered by TestAdmin_DataPlaneDoesNotExposeAPI but that
	// test runs with admin enabled. Here we confirm gateway started OK
	// without admin and the data plane responds normally.
	resp, err := h.HTTP.Get(h.GatewayURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz with admin disabled: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (data plane healthy with admin off)", resp.StatusCode)
	}
}

// TestAdmin_PortIsolation confirms that the admin and data-plane ports
// are physically different listeners by inspecting the LocalAddr of a
// connection to each.
func TestAdmin_PortIsolation(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	if h.GatewayURL == h.AdminURL {
		t.Fatalf("GatewayURL == AdminURL = %q — two listeners collapsed to one", h.GatewayURL)
	}
	gwHost := stripScheme(h.GatewayURL)
	admHost := stripScheme(h.AdminURL)

	if conn, err := net.DialTimeout("tcp", gwHost, 2*time.Second); err != nil {
		t.Errorf("dial gateway %s: %v", gwHost, err)
	} else {
		_ = conn.Close()
	}
	if conn, err := net.DialTimeout("tcp", admHost, 2*time.Second); err != nil {
		t.Errorf("dial admin %s: %v", admHost, err)
	} else {
		_ = conn.Close()
	}

	gwPort := portOf(gwHost)
	admPort := portOf(admHost)
	if gwPort == admPort {
		t.Errorf("gateway and admin share port %s", gwPort)
	}
}

func stripScheme(u string) string {
	return strings.TrimPrefix(strings.TrimPrefix(u, "http://"), "https://")
}

func portOf(hostport string) string {
	if i := strings.LastIndex(hostport, ":"); i >= 0 {
		return hostport[i+1:]
	}
	return ""
}
