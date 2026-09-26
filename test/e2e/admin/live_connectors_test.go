//go:build e2e

package admin_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/contracts/events"
	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// TestAdmin_LiveConnector_CreateEditDelete proves a connector created
// through the admin write API takes effect without a restart (#567): the
// gateway boots with no connector at all, a webhook connector pointing at
// the harness capture server is created live, a configuration bound to
// it is created live, and the very next request lands a record on the
// receiver. An edit (same name, new settings) keeps delivering through the
// rebuilt pusher, and a delete stops delivery.
//
// Pre-fix the connector and configuration were persisted and readable
// through the API, but no pusher existed until restart and every bound
// record was dropped with no signal.
func TestAdmin_LiveConnector_CreateEditDelete(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{
		AdminEnabled:     true,
		ReportingEnabled: harness.BoolPtr(false), // no boot-time connector or binding
	})
	const (
		connName = "e2e-live-hook"
		cfgName  = "e2e-live-cfg"
	)

	// 1. Connector, live. secret_ref resolves to the env var the harness
	//    already exports to the gateway for its own capture server.
	connBody := func(timeoutMS int) []byte {
		return []byte(fmt.Sprintf(`{"name":%q,"type":"webhook","url":%q,"gateway_id":"harness-gw","secret_ref":"env:HARNESS_WEBHOOK_SECRET","timeout_ms":%d}`,
			connName, h.CaptureURL(), timeoutMS))
	}
	resp := authedJSON(t, h, "POST", "/api/v1/config/connectors", connBody(5000))
	wantStatus(t, resp, http.StatusCreated, "POST connector")
	_ = resp.Body.Close()

	// 2. Configuration bound to it, live, plus a key to reach it.
	resp = authedJSON(t, h, "POST", "/api/v1/config/configurations",
		[]byte(`{"name":"`+cfgName+`","credentials":{"openai":"sk-live-mock"},"bindings":[{"protocol":"chat","models":["gpt-*"],"provider":"openai"}],"connector_bindings":[{"connector":"`+connName+`"}]}`))
	wantStatus(t, resp, http.StatusCreated, "POST configuration")
	_ = resp.Body.Close()

	auth := http.Header{"Authorization": []string{"Bearer " + mintKey(t, h, "e2e-live-key", cfgName)}}

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-live","object":"chat.completion"}`,
	})
	send := func(correlationID string) {
		t.Helper()
		hdr := auth.Clone()
		hdr.Set("X-Slipspace-Correlation-Id", correlationID)
		r := h.PostJSON("/v1/chat/completions",
			map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "."}}}, hdr)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("gateway status=%d body=%s", r.StatusCode, r.Body)
		}
	}
	expectRecord := func(correlationID string) {
		t.Helper()
		env := h.ExpectEvent("gateway.request", 5*time.Second)
		var ev events.Request
		if err := json.Unmarshal(env.InlinePayload, &ev); err != nil {
			t.Fatalf("decode payload: %v raw=%s", err, env.InlinePayload)
		}
		if ev.CorrelationID != correlationID {
			t.Fatalf("CorrelationID=%q want %q", ev.CorrelationID, correlationID)
		}
	}

	// 3. Create → the first request after the write is delivered.
	send("live-create")
	expectRecord("live-create")

	// 4. Edit (same name, new timeout) → the rebuilt pusher delivers.
	resp = authedJSON(t, h, "PUT", "/api/v1/config/connectors/"+connName, connBody(9000))
	wantStatus(t, resp, http.StatusOK, "PUT connector")
	_ = resp.Body.Close()
	send("live-edit")
	expectRecord("live-edit")

	// 5. Delete: refused while bound (409), then unbind, then delete → silence.
	resp = authedJSON(t, h, "DELETE", "/api/v1/config/connectors/"+connName, nil)
	wantStatus(t, resp, http.StatusConflict, "DELETE bound connector")
	_ = resp.Body.Close()

	resp = authedJSON(t, h, "PUT", "/api/v1/config/configurations/"+cfgName,
		[]byte(`{"credentials":{"openai":null},"bindings":[{"protocol":"chat","models":["gpt-*"],"provider":"openai"}]}`))
	wantStatus(t, resp, http.StatusOK, "PUT configuration (unbind)")
	_ = resp.Body.Close()
	resp = authedJSON(t, h, "DELETE", "/api/v1/config/connectors/"+connName, nil)
	wantStatus(t, resp, http.StatusNoContent, "DELETE connector")
	_ = resp.Body.Close()

	send("live-delete")
	h.ExpectNoEvent("gateway.request", 2*time.Second)
}

// mintKey creates an api-key bound to configuration through the admin API
// and returns its one-time plaintext secret.
func mintKey(t *testing.T, h *harness.Harness, name, configuration string) string {
	t.Helper()
	resp := authedJSON(t, h, "POST", "/api/v1/config/api-keys",
		[]byte(`{"name":"`+name+`","configuration":"`+configuration+`"}`))
	wantStatus(t, resp, http.StatusCreated, "POST api-key")
	var reveal struct {
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reveal); err != nil {
		t.Fatalf("decode reveal: %v", err)
	}
	_ = resp.Body.Close()
	return reveal.Secret
}

// spoolSeries matches one gateway.spool.* series for a connector in the
// Prometheus exposition. The OTel→Prom bridge turns dots into underscores
// and may append _total to counters; labels are matched loosely because the
// gauges also carry pod / state_name.
func spoolSeries(metric, connector string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(metric) + `(?:_total)?\{[^}]*connector="` + regexp.QuoteMeta(connector) + `"[^}]*\}\s+([0-9.e+]+)`)
}

func scrape(t *testing.T, promURL string) string {
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
	return string(b)
}

// TestAdmin_LiveConnector_SpoolTrackAndMetrics is the spool-backed half of
// #567 plus the /metrics surface of #560: an s3 connector created live gets
// a spool track in the running spool (the gateway booted with none), a
// bound request is enqueued onto it, and the per-track counters appear on
// /metrics as gateway_spool_* series labelled by connector.
func TestAdmin_LiveConnector_SpoolTrackAndMetrics(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{
		AdminEnabled:     true,
		ReportingEnabled: harness.BoolPtr(false),
	})
	const (
		connName = "e2e-live-s3"
		cfgName  = "e2e-live-s3-cfg"
	)

	// Static credentials reuse the one secret the harness exports; the
	// endpoint is the capture server so no upload ever leaves the host.
	// Delivery is not asserted here — the track and its counters are.
	resp := authedJSON(t, h, "POST", "/api/v1/config/connectors",
		[]byte(fmt.Sprintf(`{"name":%q,"type":"s3","bucket":"e2e","region":"us-east-1","endpoint_url":%q,"use_path_style":true,"auth":{"mode":"static","access_key_id_ref":"env:HARNESS_WEBHOOK_SECRET","secret_access_key_ref":"env:HARNESS_WEBHOOK_SECRET"}}`,
			connName, h.CaptureURL())))
	wantStatus(t, resp, http.StatusCreated, "POST s3 connector")
	_ = resp.Body.Close()

	resp = authedJSON(t, h, "POST", "/api/v1/config/configurations",
		[]byte(`{"name":"`+cfgName+`","credentials":{"openai":"sk-live-mock"},"bindings":[{"protocol":"chat","models":["gpt-*"],"provider":"openai"}],"connector_bindings":[{"connector":"`+connName+`"}]}`))
	wantStatus(t, resp, http.StatusCreated, "POST configuration")
	_ = resp.Body.Close()

	secret := mintKey(t, h, "e2e-live-s3-key", cfgName)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"chatcmpl-live-s3","object":"chat.completion"}`,
	})
	r := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{"Authorization": []string{"Bearer " + secret}})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d body=%s", r.StatusCode, r.Body)
	}

	enqueued := spoolSeries("gateway_spool_enqueued", connName)
	deadline := time.Now().Add(5 * time.Second)
	var body string
	for time.Now().Before(deadline) {
		body = scrape(t, h.PromURL())
		if m := enqueued.FindStringSubmatch(body); m != nil && m[1] != "0" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	m := enqueued.FindStringSubmatch(body)
	if m == nil || m[1] == "0" {
		t.Fatalf("gateway_spool_enqueued_total{connector=%q} never reached 1 after a bound request — the live-created s3 connector has no spool track or the spool meters are not exported\n%s",
			connName, spoolLines(body))
	}
	for _, name := range []string{
		"gateway_spool_dropped", "gateway_spool_written", "gateway_spool_write_errors",
		"gateway_spool_segments_sealed", "gateway_spool_uploads",
		"gateway_spool_breaker_state", "gateway_spool_pending_segments",
	} {
		if !spoolSeries(name, connName).MatchString(body) {
			t.Errorf("missing %s series for connector %q on /metrics\n%s", name, connName, spoolLines(body))
		}
	}
}

// spoolLines trims a scrape body to the gateway_spool_ lines so a failure
// message is readable rather than a full exposition dump.
func spoolLines(body string) string {
	var keep []string
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "gateway_spool_") {
			keep = append(keep, line)
		}
	}
	if len(keep) == 0 {
		return "(no gateway_spool_ series in scrape)"
	}
	return strings.Join(keep, "\n")
}
