//go:build e2e

package reporting_test

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// TestMetrics_RequestsTotalCarriesModelLabel scrapes the gateway's
// Prometheus endpoint after a chat-completions request and asserts the
// emitted `slipspace_requests_total` series carries the outbound model in
// the `gen_ai_request_model` label. This is the end-to-end half of
// issue #4 — the instrument is wired correctly inside the running
// binary, not just in the cmd/gateway test ring.
func TestMetrics_RequestsTotalCarriesModelLabel(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-1","object":"chat.completion"}`,
	})

	const model = "gpt-4o-mini-metric-probe"
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d", resp.StatusCode)
	}

	// The OTel Prometheus exporter publishes counters under their normalized
	// name; slipspace.requests.total → slipspace_requests_total.
	const metric = "slipspace_requests_total"
	want := `gen_ai_request_model="` + model + `"`

	deadline := time.Now().Add(5 * time.Second)
	var lastBody string
	for time.Now().Before(deadline) {
		body, err := scrapeMetrics(h.PromURL())
		if err != nil {
			t.Fatalf("scrape: %v", err)
		}
		lastBody = body
		if matchesSeries(body, metric, model) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("metric %s with %s not observed within deadline; last scrape:\n%s",
		metric, want, snippet(lastBody, metric))
}

func scrapeMetrics(promURL string) (string, error) {
	resp, err := http.Get(promURL + "/metrics") //nolint:gosec,noctx // test scrape against the harness's bound prom port
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// matchesSeries returns true if body contains a sample line for the given
// metric name carrying gen_ai_request_model="<model>" in its label set.
// Matches across arbitrary label-set orderings so the test is not
// sensitive to label emission order in the exporter.
func matchesSeries(body, metricName, model string) bool {
	pattern := `(?m)^` + regexp.QuoteMeta(metricName) + `\{[^}]*gen_ai_request_model="` + regexp.QuoteMeta(model) + `"[^}]*\}`
	return regexp.MustCompile(pattern).MatchString(body)
}

func snippet(body, needle string) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, needle) {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return "(no matching lines)"
	}
	return strings.Join(out, "\n")
}
