//go:build e2e

package reporting_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// oversizeBody is ~1.5 MiB of content — comfortably over the 1 MiB cap the
// explicit-cap test sets, so the captured request body clears it with margin.
const oversizeBody = 3 * 512 * 1024

// TestReporting_WebhookExplicitBodyCapStripsOversize proves the per-binding
// max_body_bytes cap still strips oversize bodies end-to-end: the harness binds
// a webhook connector with an explicit 1 MiB cap, and a request whose captured
// body exceeds it must reach the destination with its bodies stripped
// (metadata_only) and BodyOmitted set — never the multi-MiB payload handed to a
// synchronous receiver. Guards the cap-when-set path now that the connector-type
// default is no cap (#309).
func TestReporting_WebhookExplicitBodyCapStripsOversize(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{WebhookMaxBodyBytes: 1 << 20})

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-oversize","object":"chat.completion"}`,
	})

	huge := strings.Repeat("a", oversizeBody)
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": huge}}},
		http.Header{"X-Sluice-Correlation-Id": []string{"oversize-capped"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	if env.Record == nil {
		t.Fatal("request envelope carried no raw Record")
	}
	if !env.Record.Request.BodyOmitted {
		t.Errorf("request body not stripped under the explicit 1 MiB cap: body_bytes=%d body_omitted=%v",
			env.Record.Request.BodyBytes, env.Record.Request.BodyOmitted)
	}
	if len(env.Record.Request.Body) != 0 {
		t.Errorf("stripped record still carries %d body bytes", len(env.Record.Request.Body))
	}
}

// TestReporting_WebhookNoDefaultCapShipsOversize locks in the #309 behavior: a
// webhook binding with max_body_bytes unset inherits NO cap (the protective
// per-type default was dropped — the bodycapture middleware already bounds
// inbound bodies). The same oversize body must therefore reach the destination
// intact, not stripped. Guards against an accidental re-introduction of the
// default cap.
func TestReporting_WebhookNoDefaultCapShipsOversize(t *testing.T) {
	t.Parallel()
	h := harness.New(t) // no WebhookMaxBodyBytes → binding cap unset

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-uncapped","object":"chat.completion"}`,
	})

	huge := strings.Repeat("a", oversizeBody)
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": huge}}},
		http.Header{"X-Sluice-Correlation-Id": []string{"oversize-uncapped"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	if env.Record == nil {
		t.Fatal("request envelope carried no raw Record")
	}
	if env.Record.Request.BodyOmitted {
		t.Error("request body stripped with no cap set; the per-type default cap should be gone (#309)")
	}
	if len(env.Record.Request.Body) < oversizeBody {
		t.Errorf("uncapped record carries %d body bytes, want the full ~%d", len(env.Record.Request.Body), oversizeBody)
	}
}
