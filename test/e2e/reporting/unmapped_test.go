//go:build e2e

package reporting_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// TestMetrics_UnmappedFields_RequestFieldEmitted sends an Anthropic
// /v1/messages request carrying a field this build does not model and
// asserts the gateway publishes gateway_unmapped_fields_total labelled with
// that field path and direction=request — the provider-drift early-warning
// signal, exercised through the running binary rather than just the unit ring.
func TestMetrics_UnmappedFields_RequestFieldEmitted(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Body:   `{"id":"msg_1","type":"message","role":"assistant","model":"claude-3","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`,
	})

	const field = "future_probe_field"
	resp := h.PostJSON("/v1/messages",
		map[string]any{
			"model":      "claude-opus-4-7",
			"max_tokens": 10,
			"messages":   []map[string]string{{"role": "user", "content": "."}},
			field:        true,
		}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d body=%s", resp.StatusCode, resp.Body)
	}

	const metric = "gateway_unmapped_fields_total"
	wantField := `sluice_unmapped_field="` + field + `"`
	const wantDir = `sluice_unmapped_direction="request"`

	deadline := time.Now().Add(5 * time.Second)
	var lastBody string
	for time.Now().Before(deadline) {
		body, err := scrapeMetrics(h.PromURL())
		if err != nil {
			t.Fatalf("scrape: %v", err)
		}
		lastBody = body
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, metric+"{") && strings.Contains(line, wantField) && strings.Contains(line, wantDir) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("metric %s with field=%s direction=request not observed; last scrape:\n%s",
		metric, field, snippet(lastBody, metric))
}
