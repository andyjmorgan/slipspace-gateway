//go:build e2e

package reporting_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// TestReporting_WebhookDefaultBodyCapStripsOversize proves the webhook
// connector's protective default cap end-to-end: the harness binds a
// webhook connector with no max_body_bytes, so the 1 MiB connector-type
// default applies. A request whose captured body exceeds 1 MiB must
// reach the destination with its bodies stripped (metadata_only) and
// BodyOmitted set — never the multi-MiB payload handed to a synchronous
// receiver. Guards the unwired-default regression: before the default
// was honoured, an unset cap meant unlimited and the receiver got the
// whole body.
func TestReporting_WebhookDefaultBodyCapStripsOversize(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-oversize","object":"chat.completion"}`,
	})

	// ~1.5 MiB of content → the captured request body clears the 1 MiB
	// webhook default with margin.
	huge := strings.Repeat("a", 3*512*1024)
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": huge}}},
		http.Header{"X-Sluice-Correlation-Id": []string{"oversize-webhook"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	env := h.ExpectEvent("gateway.request", 5*time.Second)
	if env.Record == nil {
		t.Fatal("request envelope carried no raw Record")
	}
	if !env.Record.Request.BodyOmitted {
		t.Errorf("request body not stripped under the webhook default cap: body_bytes=%d body_omitted=%v",
			env.Record.Request.BodyBytes, env.Record.Request.BodyOmitted)
	}
	if len(env.Record.Request.Body) != 0 {
		t.Errorf("stripped record still carries %d body bytes", len(env.Record.Request.Body))
	}
}
