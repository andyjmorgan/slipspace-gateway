//go:build e2e

package resilience_test

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// TestMetrics_ResilienceCountersAfterFailover scrapes /metrics after
// a 2-attempt failover scenario and asserts every new
// gateway.resilience.* + gateway.cb.* series the orchestrator emits.
// Acceptance check for the PR-10b telemetry surface.
func TestMetrics_ResilienceCountersAfterFailover(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: failoverPolicy})
	sess := h.NewSession(t)

	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusServiceUnavailable,
		Body:         `{"error":"down"}`,
		MaxResponses: 1,
	})
	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"recovered"}`,
	})

	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("client status = %d; want 200", resp.StatusCode)
	}

	// Promtheus exporter normalises dots to underscores:
	//   gateway.resilience.attempts.total      → gateway_resilience_attempts_total
	//   gateway.resilience.attempt.duration    → gateway_resilience_attempt_duration_*
	//   gateway.resilience.attempts_per_request → gateway_resilience_attempts_per_request_*
	//   gateway.resilience.outcome.total       → gateway_resilience_outcome_total
	//   gateway.cb.state                       → gateway_cb_state
	// Prometheus alphabetises label order, so each probe matches the
	// metric name + any label-set that contains all the labels we care
	// about. The histogram-duration metric inherits a `_seconds` suffix
	// from its unit ("s"); the unit-1 histogram (attempts_per_request)
	// gets none.
	wantSeries := []seriesProbe{
		{
			name:     "gateway_resilience_attempts_total (primary/failure_status)",
			matcher:  mustMatchAll(`gateway_resilience_attempts_total\{`, `outcome="failure_status"`, `policy="cross-provider-failover"`, `target="openai"`),
			describe: "per-attempt counter, primary outcome=failure_status",
		},
		{
			name:     "gateway_resilience_attempts_total (backup/success)",
			matcher:  mustMatchAll(`gateway_resilience_attempts_total\{`, `outcome="success"`, `policy="cross-provider-failover"`, `target="anthropic"`),
			describe: "per-attempt counter, backup outcome=success",
		},
		{
			name:     "gateway_resilience_outcome_total",
			matcher:  mustMatchAll(`gateway_resilience_outcome_total\{`, `outcome="success"`, `policy="cross-provider-failover"`),
			describe: "orchestrator outcome counter, success",
		},
		{
			name:     "gateway_resilience_attempts_per_request_count",
			matcher:  mustMatchAll(`gateway_resilience_attempts_per_request_count\{`, `policy="cross-provider-failover"`),
			describe: "attempts-per-request histogram count series",
		},
		{
			name:     "gateway_resilience_attempt_duration_seconds_count",
			matcher:  mustMatchAll(`gateway_resilience_attempt_duration_seconds_count\{`, `policy="cross-provider-failover"`, `target="openai"`),
			describe: "per-attempt duration histogram count for primary",
		},
	}

	deadline := time.Now().Add(5 * time.Second)
	var lastBody string
	for time.Now().Before(deadline) {
		body, err := scrapeText(h.PromURL() + "/metrics")
		if err != nil {
			t.Fatalf("scrape: %v", err)
		}
		lastBody = body
		if allMatch(body, wantSeries) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	for _, p := range wantSeries {
		if !p.matcher.MatchString(lastBody) {
			t.Errorf("missing series: %s — %s", p.name, p.describe)
		}
	}
	t.Logf("scrape (length %d):\n%s", len(lastBody), lastBody)
}

// TestMetrics_RequestsTotalIncrementsOncePerRequest proves the PR-10b
// per-attempt → per-request meter refactor for slipspace_requests_total:
// a 2-attempt failover bumps the counter by exactly 1, not 2.
func TestMetrics_RequestsTotalIncrementsOncePerRequest(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{PolicyYAML: failoverPolicy})
	sess := h.NewSession(t)

	// Baseline scrape: counter starts at 0 for this gateway process.
	baseBody, err := scrapeText(h.PromURL() + "/metrics")
	if err != nil {
		t.Fatalf("baseline scrape: %v", err)
	}
	baseline := parseCounter(baseBody, `slipspace_requests_total\{[^}]*gen_ai_request_model="gpt-4o-mini"[^}]*http_response_status_code="200"`)

	sess.Stage(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Status:       http.StatusServiceUnavailable,
		Body:         `{"error":"down"}`,
		MaxResponses: 1,
	})
	sess.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Status: http.StatusOK,
		Body:   `{"id":"recovered"}`,
	})
	resp := sess.Post("/v1/chat/completions",
		map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}

	// Poll until the counter has incremented at least once.
	deadline := time.Now().Add(5 * time.Second)
	var got int
	for time.Now().Before(deadline) {
		body, _ := scrapeText(h.PromURL() + "/metrics")
		got = parseCounter(body, `slipspace_requests_total\{[^}]*gen_ai_request_model="gpt-4o-mini"[^}]*http_response_status_code="200"`)
		if got > baseline {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if got-baseline != 1 {
		t.Errorf("slipspace_requests_total{http_response_status_code=200,gen_ai_request_model=gpt-4o-mini} delta = %d; want 1 (one increment per request, not per attempt)", got-baseline)
	}
}

// seriesProbe is one regex-matched series the metrics scrape test
// expects to see in the response body.
type seriesProbe struct {
	name     string
	matcher  *regexpMatcher
	describe string
}

// regexpMatcher matches a metric line if it contains every supplied
// substring. Avoids hand-encoded alphabetic label ordering — the
// Prometheus exporter sorts labels, so a single regex per probe is
// brittle when new labels are added. Multi-substring matching is
// order-independent and survives schema growth.
type regexpMatcher struct {
	parts []*regexp.Regexp
}

func mustMatchAll(literals ...string) *regexpMatcher {
	parts := make([]*regexp.Regexp, len(literals))
	for i, s := range literals {
		parts[i] = regexp.MustCompile(s)
	}
	return &regexpMatcher{parts: parts}
}

// MatchString returns true if some line in body matches every part.
func (m *regexpMatcher) MatchString(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if matchAll(line, m.parts) {
			return true
		}
	}
	return false
}

func matchAll(line string, parts []*regexp.Regexp) bool {
	for _, p := range parts {
		if !p.MatchString(line) {
			return false
		}
	}
	return true
}

func allMatch(body string, probes []seriesProbe) bool {
	for _, p := range probes {
		if !p.matcher.MatchString(body) {
			return false
		}
	}
	return true
}

func scrapeText(url string) (string, error) {
	resp, err := http.Get(url) //nolint:gosec,noctx // test scrape against the harness's bound prom port
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

// parseCounter scans body for the first metric line matching pattern
// and returns its integer counter value. Returns 0 when no match —
// useful for baseline-then-delta assertions.
func parseCounter(body, pattern string) int {
	re := regexp.MustCompile(pattern + `[^}]*\}\s+(\d+)`)
	m := re.FindStringSubmatch(body)
	if len(m) < 2 {
		return 0
	}
	n := 0
	for _, c := range m[1] {
		n = n*10 + int(c-'0')
	}
	return n
}

func grep(body, pattern string) string {
	re := regexp.MustCompile(pattern)
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if re.MatchString(line) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
