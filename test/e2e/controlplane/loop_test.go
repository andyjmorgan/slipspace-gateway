//go:build e2e

package controlplane_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// TestControlPlane_FullLoop proves the config-distribution loop end-to-end
// through the real binaries:
//
//   - The harness spawns cmd/api (the control plane) serving config-dev with
//     the live mockllm upstream.
//   - It spawns a CP-managed gateway whose OWN local config points the upstream
//     at a dead address (127.0.0.1:1).
//
// A successful upstream round-trip can therefore only happen if the gateway
// fetched and applied the control plane's config (live upstream) over its local
// copy (dead upstream). The test also asserts the gateway registered in the CP
// fleet — exercising Register/Heartbeat, FetchConfig, and the fleet read API
// through the spawned binaries.
func TestControlPlane_FullLoop(t *testing.T) {
	h := harness.NewWithOptions(t, harness.Options{ControlPlane: true})

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"cp-loop","object":"chat.completion"}`,
	})

	resp := h.PostJSON("/v1/chat/completions", map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "."}},
	}, http.Header{"Authorization": []string{"Bearer " + h.APIKey}})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("request through CP-managed gateway = %d, want 200 — the gateway must serve control-plane config (its local upstream is a dead address). body=%s",
			resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), "cp-loop") {
		t.Fatalf("response did not carry the canned upstream payload (CP config not served?): %s", resp.Body)
	}

	requireFleetMember(t, h)
}

// TestControlPlane_BootsWithNoLocalConfig proves the CP-managed paradigm's boot
// path: a gateway with NO local config directory at all (SLUICE_CONFIG_DIR
// points at a path that does not exist) boots empty and serves config sourced
// entirely from the control plane. Without the startup tolerance this gateway
// would die on the missing config directory before ever reaching the CP fetch.
func TestControlPlane_BootsWithNoLocalConfig(t *testing.T) {
	h := harness.NewWithOptions(t, harness.Options{ControlPlane: true, ControlPlaneNoLocalConfig: true})

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"cp-noconfig","object":"chat.completion"}`,
	})

	resp := h.PostJSON("/v1/chat/completions", map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "."}},
	}, http.Header{"Authorization": []string{"Bearer " + h.APIKey}})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("request through no-local-config CP gateway = %d, want 200 — the gateway must boot empty and serve control-plane config. body=%s",
			resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), "cp-noconfig") {
		t.Fatalf("response did not carry the canned upstream payload (CP config not served?): %s", resp.Body)
	}

	requireFleetMember(t, h)
}

// requireFleetMember polls the control plane's fleet read API until the gateway
// has registered.
func requireFleetMember(t *testing.T, h *harness.Harness) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if len(fetchFleet(t, h)) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("gateway never registered in the control-plane fleet")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func fetchFleet(t *testing.T, h *harness.Harness) []map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.ControlPlaneFleetURL(), nil)
	if err != nil {
		t.Fatalf("build fleet request: %v", err)
	}
	req.SetBasicAuth(h.ControlPlaneAdminUser(), h.ControlPlaneAdminPassword())
	res, err := h.HTTP.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = res.Body.Close() }()
	var out []map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return out
}
