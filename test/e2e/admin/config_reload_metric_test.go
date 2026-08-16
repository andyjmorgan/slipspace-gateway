//go:build e2e

package admin_test

import (
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// configReloadSeries matches the counter's Prometheus exposition line. The
// OTel→Prom bridge renames gateway.config_reload.total to
// gateway_config_reload_total and may append _total again; tolerate both and
// ignore any labels.
var configReloadSeries = regexp.MustCompile(`(?m)^gateway_config_reload_total(?:_total)?(?:\{[^}]*\})?\s+([0-9.e+]+)`)

func scrapeConfigReload(t *testing.T, promURL string) (float64, string) {
	t.Helper()
	resp, err := http.Get(promURL + "/metrics") //nolint:noctx // test scrape, harness client lifetime
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scrape: %v", err)
	}
	body := string(b)
	m := configReloadSeries.FindStringSubmatch(body)
	if m == nil {
		return 0, body
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("parse counter %q: %v", m[1], err)
	}
	return v, body
}

// TestAdmin_ConfigReloadCounter_IncrementsOnLiveWrite proves
// gateway.config_reload.total actually moves when the admin write API
// applies a config change live.
//
// The counter was registered and exported but never incremented, so it sat
// at a permanent zero — worse than absent, because a zero timeseries reads
// as "config has never reloaded" and tells an operator debugging a live
// rules edit that their write did not take effect.
//
// The assertion is on the delta, not an absolute value: the gateway may
// legitimately publish snapshots for reasons other than this request.
func TestAdmin_ConfigReloadCounter_IncrementsOnLiveWrite(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})
	const name = "e2e-reload-counter"

	// Baseline. Registering a subscriber must NOT have counted a reload,
	// so a gateway that has served no writes reports zero (or has no
	// series yet, which scrapes as the same thing).
	before, body := scrapeConfigReload(t, h.PromURL())
	if before != 0 {
		t.Errorf("baseline gateway_config_reload_total = %v, want 0 — "+
			"Subscribe's registration call must not count as a reload\n%s", before, body)
	}

	resp := authedJSON(t, h, "POST", "/api/v1/config/providers",
		[]byte(`{"name":"`+name+`","base_url":"http://mockllm:5555","protocols":{"chat":{"path":"/v1/chat/completions"}}}`))
	wantStatus(t, resp, http.StatusCreated, "POST provider")
	_ = resp.Body.Close()

	// The counter is recorded synchronously inside Replace, but the
	// Prometheus bridge collects on scrape, so poll briefly.
	deadline := time.Now().Add(5 * time.Second)
	var after float64
	var lastBody string
	for time.Now().Before(deadline) {
		after, lastBody = scrapeConfigReload(t, h.PromURL())
		if after >= before+1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if after < before+1 {
		t.Fatalf("gateway_config_reload_total = %v, want >= %v after a live config write\n%s",
			after, before+1, tail(lastBody))
	}

	// A second write moves it again — it counts swaps, not "has ever swapped".
	resp = authedJSON(t, h, "PUT", "/api/v1/config/providers/"+name,
		[]byte(`{"base_url":"http://moved:5555","protocols":{"chat":{"path":"/v1/chat/completions"}}}`))
	wantStatus(t, resp, http.StatusOK, "PUT provider")
	_ = resp.Body.Close()

	deadline = time.Now().Add(5 * time.Second)
	var second float64
	for time.Now().Before(deadline) {
		second, lastBody = scrapeConfigReload(t, h.PromURL())
		if second >= after+1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if second < after+1 {
		t.Fatalf("second write: gateway_config_reload_total = %v, want >= %v\n%s",
			second, after+1, tail(lastBody))
	}
}

// tail trims a scrape body to the config_reload lines plus a little context,
// so a failure message is readable rather than a full exposition dump.
func tail(body string) string {
	var keep []string
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "config_reload") {
			keep = append(keep, line)
		}
	}
	if len(keep) == 0 {
		return "(no config_reload series in scrape)"
	}
	return strings.Join(keep, "\n")
}
