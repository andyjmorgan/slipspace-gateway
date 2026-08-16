//go:build e2e

package admin_test

import (
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	adminc "github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// TestAdmin_EnabledWithoutPassword_ListenerRefused proves the gateway will
// not open the management console when admin.enabled is true but no password
// is configured.
//
// admin.Config.Validate() has always encoded this rule — it returns
// ErrPasswordRequired — but nothing called it, so the guard was dead code.
// The listener came up and internal/admin.BasicAuth compared the supplied
// password against "", which succeeds for an empty one: anyone who could
// reach the port authenticated as admin and got config read/write plus
// api-key reveal.
//
// The data plane must stay up: refusing the console is not a reason to stop
// serving proxy traffic.
func TestAdmin_EnabledWithoutPassword_ListenerRefused(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{
		AdminEnabled:      true,
		AdminOmitPassword: true,
	})

	// The admin port must not be serving. Poll briefly rather than probing
	// once, so a slow start cannot pass this by accident.
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(h.AdminURL + "/api/v1/auth/me") //nolint:noctx // short-lived probe
		if err == nil {
			body := resp.StatusCode
			_ = resp.Body.Close()
			t.Fatalf("admin listener answered %d with no password configured — "+
				"the console must not open (BasicAuth accepts an empty password)", body)
		}
		var netErr net.Error
		if !errors.As(err, &netErr) && !isConnRefused(err) {
			t.Fatalf("unexpected probe error: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// And the data plane is unaffected — refusing the console must not take
	// the gateway down with it.
	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl","object":"chat.completion"}`,
	})
	resp := h.PostJSON("/v1/chat/completions", map[string]any{
		"model":    "gpt-4o-mini",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("data plane status = %d, want 200 — refusing the admin console "+
			"must not stop the gateway serving proxy traffic (body=%s)",
			resp.StatusCode, string(resp.Body))
	}

	// Sanity: the credential the console would have demanded is the one the
	// contract names, so this test tracks the real guard rather than a typo.
	if adminc.Username == "" {
		t.Error("adminc.Username is empty; the auth contract moved")
	}
}

// isConnRefused reports whether err is a dial failure, which is what a
// refused-to-open listener produces.
func isConnRefused(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr)
}
