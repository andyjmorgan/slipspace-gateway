//go:build e2e

package admin_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	adminc "github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// TestAdmin_EarlyFailures proves the real binary publishes local rejections
// to the same recent/SSE feed as upstream completions, with inspectable bodies.
func TestAdmin_EarlyFailures(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})
	before := h.MockRequestCount()
	for _, tc := range []struct {
		name, method, path, body, reason string
		status                           int
		authenticated                    bool
		configuration                    string
	}{
		{"models", "GET", "/v1/models?private=not-in-path", "", "no_route", 404, true, "production"},
		{"binding", "POST", "/v1/messages", `{"model":"unmapped-internal","messages":[]}`, "no_binding", 404, true, ""},
		{"authentication", "GET", "/v1/models", "", "unauthorized", 401, false, ""},
		{"parse", "POST", "/v1/messages", "{", "", 400, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cid := "early-failure-" + tc.name
			req, err := http.NewRequest(tc.method, h.GatewayURL+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Slipspace-Correlation-Id", cid)
			req.Header.Set("X-Slipspace-Session-Id", "failure-session")
			if tc.authenticated {
				req.Header.Set("Authorization", "Bearer "+h.APIKey)
			}
			if tc.configuration != "" {
				req.Header.Set("X-Slipspace-Configuration", tc.configuration)
			}
			resp, err := h.HTTP.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			wireBody, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, body %s", resp.StatusCode, wireBody)
			}

			var entries []adminc.MessageEntry
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				recent, err := http.NewRequest(http.MethodGet, h.AdminURL+"/api/v1/messages/recent", nil)
				if err != nil {
					t.Fatal(err)
				}
				recent.SetBasicAuth(adminc.Username, h.AdminPassword)
				res, err := h.HTTP.Do(recent)
				if err != nil {
					t.Fatal(err)
				}
				var data adminc.MessagesRecentResponse
				err = json.NewDecoder(res.Body).Decode(&data)
				_ = res.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				entries = nil
				for _, e := range data.Entries {
					if e.CorrelationID == cid {
						entries = append(entries, e)
					}
				}
				if len(entries) > 0 {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if len(entries) != 1 {
				t.Fatalf("expected one entry; got %+v", entries)
			}
			e := entries[0]
			if e.StatusCode != tc.status || e.GatewayError == "" || !strings.Contains(e.GatewayError, tc.reason) || e.Method != tc.method || e.Path != strings.Split(tc.path, "?")[0] || e.SessionID != "failure-session" {
				t.Fatalf("unexpected entry: %+v", e)
			}
			if e.UpstreamError != "" || e.Provider != "" {
				t.Fatalf("local rejection described as upstream: %+v", e)
			}
			req, err = http.NewRequest(http.MethodGet, h.AdminURL+"/api/v1/messages/"+e.EventID+"/body", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.SetBasicAuth(adminc.Username, h.AdminPassword)
			res, err := h.HTTP.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != 200 || !strings.Contains(string(b), "response") {
				t.Fatalf("body detail: %d %s", res.StatusCode, b)
			}
		})
	}
	if after := h.MockRequestCount(); after != before {
		t.Fatalf("rejected requests reached upstream: before=%d after=%d", before, after)
	}
}
