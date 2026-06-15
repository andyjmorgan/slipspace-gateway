//go:build e2e

package telemetry_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
)

const (
	testGatewayID = "gw-e2e"
	testSecret    = "e2e-secret"
	testUser      = "admin"
	testPass      = "hunter2"
	// bcrypt hash of testPass (MinCost), generated once at init to avoid the
	// per-spawn cost.
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// buildBinary compiles cmd/telemetry once for the package.
func buildBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "telemetry-bin")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "telemetry")
		cmd := exec.Command("go", "build", "-o", binPath, "github.com/andyjmorgan/sluice-gateway/cmd/telemetry")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("build telemetry: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatalf("build: %v", buildErr)
	}
	return binPath
}

// freePort returns an OS-assigned free TCP port on loopback.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// service is a spawned telemetry binary under test.
type service struct {
	httpBase string // http://127.0.0.1:<port>
	otlpAddr string // 127.0.0.1:<port>
}

// startService spawns a fresh telemetry binary wired to the shared Postgres.
// extraConfig lines (top-level YAML keys, e.g. "content_max_bytes: 0") are
// appended to the generated config.
func startService(t *testing.T, extraConfig ...string) *service {
	t.Helper()
	if startErr != nil {
		t.Skipf("postgres container unavailable: %v", startErr)
	}
	bin := buildBinary(t)

	httpPort, otlpPort := freePort(t), freePort(t)
	hash, err := bcrypt.GenerateFromPassword([]byte(testPass), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	cfg := fmt.Sprintf(`
http_bind: 127.0.0.1:%d
otlp_bind: 127.0.0.1:%d
postgres:
  dsn: %s
console:
  username: %s
  password_hash: %q
gateways:
  - id: %s
    hmac_secret: %s
`, httpPort, otlpPort, sharedDSN, testUser, string(hash), testGatewayID, testSecret)
	for _, line := range extraConfig {
		cfg += line + "\n"
	}

	cfgPath := filepath.Join(t.TempDir(), "telemetry.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "-config", cfgPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})

	svc := &service{
		httpBase: fmt.Sprintf("http://127.0.0.1:%d", httpPort),
		otlpAddr: fmt.Sprintf("127.0.0.1:%d", otlpPort),
	}
	waitReady(t, svc.httpBase)
	return svc
}

// waitReady polls /readyz until 200 or a deadline.
func waitReady(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/readyz") //nolint:noctx // test
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("service did not become ready")
}

// --- record ingest (the real-time webhook pusher's wire shape) ---

// postRecord HMAC-signs and POSTs one cc.Record to the record-ingest endpoint,
// exactly as the gateway's webhook connector pusher does.
func (s *service) postRecord(t *testing.T, gwID, secret string, rec cc.Record) *http.Response {
	t.Helper()
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	sig := hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodPost, s.httpBase+"/api/v1/ingest/record", strings.NewReader(string(raw)))
	req.Header.Set("X-Sluice-Gateway-Id", gwID)
	req.Header.Set("X-Sluice-Signature", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post record: %v", err)
	}
	return resp
}

// --- OTLP ---

func strKV(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}
func intKV(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}

// strSliceKV builds a string-array attribute (the shape sluice.tags /
// sluice.rules_fired ride on the span).
func strSliceKV(k string, vs ...string) *commonpb.KeyValue {
	vals := make([]*commonpb.AnyValue, len(vs))
	for i, v := range vs {
		vals[i] = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}
	}
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{
		Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: vals}},
	}}
}

// sendSpan exports one gen_ai span carrying GenAI semconv attributes plus the
// sluice.correlation_id join key (the only sluice.* the extractor reads), then
// closes the connection.
func (s *service) sendSpan(t *testing.T, attrs ...*commonpb.KeyValue) {
	t.Helper()
	conn, err := grpc.NewClient(s.otlpAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("otlp dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	client := collectortrace.NewTraceServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// observed_at is the span START time (single-writer design), so stamp the
	// span "now" — a fixed 1970 start would fall outside the dashboard window.
	start := time.Now()
	_, err = client.Export(ctx, &collectortrace.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{{
					Name:              "gen_ai.request",
					StartTimeUnixNano: uint64(start.UnixNano()),                             //nolint:gosec // test timestamp
					EndTimeUnixNano:   uint64(start.Add(200 * time.Millisecond).UnixNano()), //nolint:gosec // test timestamp
					Attributes:        attrs,
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("otlp export: %v", err)
	}
}

// sendCounter exports one delta-temporality sum metric (the shape the gateway's
// sluice.* counters export under) so the dashboard meter rollups can SUM it.
func (s *service) sendCounter(t *testing.T, name string, dps ...*metricspb.NumberDataPoint) {
	t.Helper()
	conn, err := grpc.NewClient(s.otlpAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("otlp dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	client := collectormetrics.NewMetricsServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = client.Export(ctx, &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{
					Name: name,
					Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
						AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
						IsMonotonic:            true,
						DataPoints:             dps,
					}},
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("otlp metrics export: %v", err)
	}
}

// counterDP builds one delta counter sample stamped now (inside the dashboard
// window) with the given attributes.
func counterDP(value int64, attrs ...*commonpb.KeyValue) *metricspb.NumberDataPoint {
	return &metricspb.NumberDataPoint{
		TimeUnixNano: uint64(time.Now().UnixNano()), //nolint:gosec // test timestamp
		Value:        &metricspb.NumberDataPoint_AsInt{AsInt: value},
		Attributes:   attrs,
	}
}

// --- authed API ---

// getRaw GETs path with Basic auth and an explicit Accept-Encoding (which
// disables the Go transport's transparent gzip), returning the status,
// headers, and the raw on-the-wire body bytes — the measurement helper the
// payload-size assertions use.
func (s *service) getRaw(t *testing.T, path, acceptEncoding string) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, s.httpBase+path, nil)
	req.SetBasicAuth(testUser, testPass)
	req.Header.Set("Accept-Encoding", acceptEncoding)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp.StatusCode, resp.Header, body
}

func (s *service) getJSON(t *testing.T, path string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, s.httpBase+path, nil)
	req.SetBasicAuth(testUser, testPass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return resp.StatusCode
}

// --- tests ---

func recordWith(correlationID string, req, resp string) cc.Record {
	return cc.Record{
		V:              1,
		ID:             "id-" + correlationID,
		TsNs:           1_000_000_000,
		Seq:            1,
		InstanceID:     "i1",
		CorrelationID:  correlationID,
		Configuration:  "dev",
		Provider:       "anthropic",
		Protocol:       "messages",
		Model:          "claude-x",
		Request:        cc.RequestPart{Method: "POST", Body: json.RawMessage(req)},
		Response:       cc.ResponsePart{Status: 200, Body: json.RawMessage(resp), LastByteNs: 1_200_000_000},
		UpstreamStatus: 200,
		SchemaVersion:  cc.SchemaVersion,
	}
}

func TestE2E_RecordTrust(t *testing.T) {
	svc := startService(t)

	// Accepted: correct gateway + signature.
	if resp := svc.postRecord(t, testGatewayID, testSecret, recordWith("wh-ok", `{"hello":"world"}`, `{"ok":true}`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("trusted record status = %d, want 200", resp.StatusCode)
	}
	// Rejected: forged signature.
	if resp := svc.postRecord(t, testGatewayID, "wrong-secret", recordWith("wh-bad", `{}`, `{}`)); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged record status = %d, want 401", resp.StatusCode)
	}
	// Rejected: unregistered gateway.
	if resp := svc.postRecord(t, "gw-unknown", testSecret, recordWith("wh-bad2", `{}`, `{}`)); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unregistered record status = %d, want 401", resp.StatusCode)
	}

	// The accepted body is retrievable via the inspector; the rejected ones are not.
	var body adminc.MessageBodyDetail
	if code := svc.getJSON(t, "/api/v1/messages/wh-ok/body", &body); code != http.StatusOK {
		t.Fatalf("body fetch status = %d", code)
	}
	if body.Request != `{"hello":"world"}` {
		t.Errorf("request body = %q", body.Request)
	}
	if code := svc.getJSON(t, "/api/v1/messages/wh-bad/body", nil); code != http.StatusNotFound {
		t.Errorf("rejected body should be 404, got %d", code)
	}
}

// TestE2E_RecordDetailEnriched proves the lazy-record split: the span carries
// the post-rule fired-rule NAMES (sluice.rules_fired) onto the entity for the
// list view, while the full rule lifecycle (actions / terminated / attempts) and
// the raw headers ride the verbatim record blob, surfaced by the body endpoint.
func TestE2E_RecordDetailEnriched(t *testing.T) {
	svc := startService(t)

	// The span is the single writer of the entity; it carries the fired-rule
	// names so the list view can show which rules matched.
	svc.sendSpan(t,
		strKV("sluice.correlation_id", "enriched-1"),
		strKV("gen_ai.provider.name", "anthropic"),
		strSliceKV("sluice.rules_fired", "redirect", "stop"),
	)

	rec := recordWith("enriched-1", `{"q":"hi"}`, `{"a":"yo"}`)
	rec.Request.Headers = map[string]string{"content-type": "application/json"}
	rec.RulesFired = []cc.RuleFired{
		{Name: "redirect", ActionsApplied: []string{"changeProvider"}, Terminated: false},
		{Name: "stop", Terminated: true},
	}
	rec.PolicyRef = "failover-pool"
	rec.Attempts = []cc.Attempt{
		{Target: "primary", StartedAtNs: 1_000_000_000, DurationMs: 80, StatusCode: 503, Outcome: "failure_status"},
		{Target: "backup", StartedAtNs: 1_080_000_000, DurationMs: 100, StatusCode: 200, Outcome: "success"},
	}
	if resp := svc.postRecord(t, testGatewayID, testSecret, rec); resp.StatusCode != http.StatusOK {
		t.Fatalf("post record status = %d", resp.StatusCode)
	}

	// The recent-messages entry (entity = span) surfaces the fired-rule names.
	var msgs adminc.MessagesRecentResponse
	if code := svc.getJSON(t, "/api/v1/messages/recent?limit=50", &msgs); code != http.StatusOK {
		t.Fatalf("messages status = %d", code)
	}
	var e *adminc.MessageEntry
	for i := range msgs.Entries {
		if msgs.Entries[i].EventID == "enriched-1" {
			e = &msgs.Entries[i]
		}
	}
	if e == nil {
		t.Fatalf("enriched-1 not found in %+v", msgs.Entries)
	}
	if len(e.RulesMatched) != 2 || e.RulesMatched[0].RuleName != "redirect" || e.RulesMatched[1].RuleName != "stop" {
		t.Errorf("fired-rule names not surfaced on the entity: %+v", e.RulesMatched)
	}

	// The full rule lifecycle + attempts + raw headers ride the lazy record blob,
	// returned verbatim by the telemetry-native events body endpoint as the
	// cc.Record. (The parity /messages/{id}/body endpoint returns the projected
	// MessageBodyDetail shape; the raw record lives on /events/{id}/body.)
	var rrec cc.Record
	if code := svc.getJSON(t, "/api/v1/events/enriched-1/body", &rrec); code != http.StatusOK {
		t.Fatalf("record body status = %d", code)
	}
	if len(rrec.RulesFired) != 2 || rrec.RulesFired[0].Name != "redirect" ||
		len(rrec.RulesFired[0].ActionsApplied) != 1 || rrec.RulesFired[0].ActionsApplied[0] != "changeProvider" ||
		!rrec.RulesFired[1].Terminated {
		t.Errorf("rule lifecycle not on the record: %+v", rrec.RulesFired)
	}
	if len(rrec.Attempts) != 2 || rrec.Attempts[0].Target != "primary" || rrec.Attempts[1].Outcome != "success" {
		t.Errorf("attempts not on the record: %+v", rrec.Attempts)
	}
	if rrec.Request.Headers["content-type"] != "application/json" {
		t.Errorf("request headers not on the record: %+v", rrec.Request.Headers)
	}
}

func TestE2E_OTLPStitchAndDashboard(t *testing.T) {
	svc := startService(t)

	// Single-writer entity: the gen_ai span owns request_events. The relaxed
	// channel boundary means the gateway facts (configuration, protocol, tags)
	// ride the span too, alongside the GenAI semconv. The whole span lands in
	// span_event; the columns + tags project out of it.
	svc.sendSpan(t,
		strKV("sluice.correlation_id", "otlp-1"),
		strKV("gen_ai.provider.name", "anthropic"),
		strKV("gen_ai.request.model", "claude-x"),
		strKV("sluice.configuration", "dev"),
		strKV("sluice.protocol", "messages"),
		strSliceKV("sluice.tags", "alpha", "beta"),
		intKV("http.response.status_code", 200),
		intKV("gen_ai.usage.input_tokens", 10),
		intKV("gen_ai.usage.output_tokens", 20),
	)
	// Record feed: the lazy verbatim blob carrying the raw bodies for the same
	// correlation id, joined only when the inspector body tab opens.
	rec := recordWith("otlp-1", `{"q":"hi"}`, `{"a":"yo"}`)
	rec.Tags = []string{"alpha", "beta"}
	svc.postRecord(t, testGatewayID, testSecret, rec)

	// Messages list carries the stitched event.
	var msgs adminc.MessagesRecentResponse
	if code := svc.getJSON(t, "/api/v1/messages/recent?limit=50", &msgs); code != http.StatusOK {
		t.Fatalf("messages status = %d", code)
	}
	var found *adminc.MessageEntry
	for i := range msgs.Entries {
		if msgs.Entries[i].EventID == "otlp-1" {
			found = &msgs.Entries[i]
		}
	}
	if found == nil {
		t.Fatalf("otlp-1 not in messages: %+v", msgs.Entries)
	}
	if found.Model != "claude-x" || found.Provider != "anthropic" || found.TokensIn != 10 {
		t.Errorf("stitched entry = %+v", found)
	}
	if len(found.Tags) != 2 {
		t.Errorf("tags = %v", found.Tags)
	}

	// Body inspector stitches request + response by correlation id.
	var body adminc.MessageBodyDetail
	svc.getJSON(t, "/api/v1/messages/otlp-1/body", &body)
	if body.Request == "" || body.Response == "" {
		t.Errorf("body not stitched: %+v", body)
	}

	// The dashboard rollups now read the sluice meter feed (the continuous
	// aggregates over metric_points), NOT the request_events span — see
	// invariant #4 + the @100 rearchitecture. So drive the dashboard with the
	// matching request/token counters the gateway emits alongside the span; the
	// real-time CAGG view surfaces them without an explicit refresh.
	svc.sendCounter(t, "sluice.requests.total",
		counterDP(1,
			strKV("gen_ai.provider.name", "anthropic"),
			strKV("gen_ai.request.model", "claude-x"),
			strKV("sluice.configuration", "dev"),
			strKV("sluice.protocol", "messages"),
			strKV("http.response.status_code", "200"),
		),
	)
	svc.sendCounter(t, "sluice.tokens.input.total",
		counterDP(10,
			strKV("gen_ai.provider.name", "anthropic"),
			strKV("gen_ai.request.model", "claude-x"),
			strKV("sluice.configuration", "dev"),
			strKV("sluice.protocol", "messages"),
		),
	)

	// Dashboard summary counts the request (from cagg_requests_1m).
	var sum adminc.DashboardSummary
	if code := svc.getJSON(t, "/api/v1/dashboard/summary?window=24h", &sum); code != http.StatusOK {
		t.Fatalf("summary status = %d", code)
	}
	if sum.Totals.Requests < 1 {
		t.Errorf("summary requests = %d, want >= 1", sum.Totals.Requests)
	}
	if sum.Totals.TokensIn < 10 {
		t.Errorf("summary tokens_in = %d, want >= 10", sum.Totals.TokensIn)
	}

	// Timeseries returns the named series.
	var ts adminc.DashboardTimeseries
	if code := svc.getJSON(t, "/api/v1/dashboard/timeseries?series=requests&window=24h", &ts); code != http.StatusOK {
		t.Fatalf("timeseries status = %d", code)
	}
	if len(ts.Series) != 1 || ts.Series[0].Name != "requests" {
		t.Errorf("series = %+v", ts.Series)
	}
}

func TestE2E_SessionRollup(t *testing.T) {
	svc := startService(t)
	for _, id := range []string{"sess-a", "sess-b"} {
		svc.sendSpan(t,
			strKV("sluice.correlation_id", id),
			strKV("gen_ai.conversation.id", "session-1"),
			strKV("gen_ai.provider.name", "openai"),
			intKV("http.response.status_code", 200),
			intKV("gen_ai.usage.input_tokens", 5),
		)
	}
	var sess adminc.SessionView
	if code := svc.getJSON(t, "/api/v1/sessions/session-1", &sess); code != http.StatusOK {
		t.Fatalf("session status = %d", code)
	}
	if sess.SessionID != "session-1" {
		t.Errorf("session_id = %q", sess.SessionID)
	}
	if sess.Totals.Requests != 2 {
		t.Errorf("session rollup requests = %d, want 2", sess.Totals.Requests)
	}
	// The endpoint must serve the tagged MessageEntry shape (snake_case,
	// projected via mapEntry) — not the raw store struct. Assert per-request
	// fields decoded onto the typed contract through the real binary.
	if len(sess.Requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(sess.Requests))
	}
	for _, r := range sess.Requests {
		if r.Provider != "openai" || r.StatusCode != 200 || r.TokensIn != 5 {
			t.Errorf("request not projected onto tagged shape: %+v", r)
		}
	}
}

// TestE2E_DashboardFiredFromMeters proves the rules-fired / tags-fired panels
// read the sluice meter rollups (metric_points), not a request_events.detail
// scan (invariant #4). Delta counter samples SUM to exact counts.
func TestE2E_DashboardFiredFromMeters(t *testing.T) {
	svc := startService(t)

	svc.sendCounter(t, "sluice.rule.fired",
		counterDP(2, strKV("rule_name", "redirect"), strKV("sluice.configuration", "dev")),
		counterDP(1, strKV("rule_name", "tagit"), strKV("sluice.configuration", "dev")),
	)
	svc.sendCounter(t, "gateway.tags.applied.total",
		counterDP(3, strKV("tag", "alpha"), strKV("sluice.configuration", "dev")),
	)

	var sum adminc.DashboardSummary
	if code := svc.getJSON(t, "/api/v1/dashboard/summary?window=24h", &sum); code != http.StatusOK {
		t.Fatalf("summary status = %d", code)
	}

	rules := map[string]int64{}
	for _, r := range sum.RulesFired {
		rules[r.RuleName] = r.FireCount
	}
	if rules["redirect"] != 2 || rules["tagit"] != 1 {
		t.Errorf("rules-fired rollup = %+v (want redirect=2, tagit=1)", sum.RulesFired)
	}
	tags := map[string]int64{}
	for _, r := range sum.TagsFired {
		tags[r.Tag] = r.ApplyCount
	}
	if tags["alpha"] != 3 {
		t.Errorf("tags-fired rollup = %+v (want alpha=3)", sum.TagsFired)
	}
}

// TestE2E_DashboardSecurityDisabled proves the security dashboard route is
// wired through the real binary and reports the scanner as off when it is not
// configured — the default deployment. enabled=false short-circuits the query,
// so the breakdown slices come back as [] (the SPA maps over them).
func TestE2E_DashboardSecurityDisabled(t *testing.T) {
	svc := startService(t)

	var sec adminc.DashboardSecurity
	if code := svc.getJSON(t, "/api/v1/dashboard/security?window=24h", &sec); code != http.StatusOK {
		t.Fatalf("security status = %d", code)
	}
	if sec.Enabled {
		t.Fatal("scanner should report disabled when not configured")
	}
	if sec.Window != "24h" {
		t.Errorf("window = %q", sec.Window)
	}
	if sec.Scanned != 0 || sec.ByCheckType == nil || sec.TopCategories == nil {
		t.Errorf("disabled response should be empty with non-nil slices: %+v", sec)
	}
}

func TestE2E_StreamingResponseCaptured(t *testing.T) {
	svc := startService(t)
	// The gateway escapes raw SSE bytes to a JSON string before they ride
	// the Record's Body; model that with a JSON-string response here.
	rec := recordWith("sse-1", `{"q":"hi"}`, `"data: {\"delta\":\"hello\"}\n\n"`)
	rec.Response.StreamChunks = 3 // streaming
	// The gateway's accumulator rollup rides the Record alongside the raw SSE
	// bytes — this is the alignment fix: telemetry must show the assembled
	// response (Response tab), not only the raw stream (Raw stream tab).
	rec.Response.Assembled = json.RawMessage(`{"content":[{"type":"text","text":"hello world"}]}`)
	rec.Response.AssemblyPartial = true
	svc.postRecord(t, testGatewayID, testSecret, rec)
	var body adminc.MessageBodyDetail
	if code := svc.getJSON(t, "/api/v1/messages/sse-1/body", &body); code != http.StatusOK {
		t.Fatalf("body status = %d", code)
	}
	// Raw SSE bytes survive on the response body (the Raw stream tab), decoded
	// back to their raw form — the gateway's JSON-string wrapping is reversed,
	// so the inspector shows real `data:` lines and a real newline, not a
	// quoted, escaped blob. Regression guard for the Raw stream tab rendering
	// SSE as "…\n…" with literal backslash-n.
	if want := "data: {\"delta\":\"hello\"}\n\n"; body.Response != want {
		t.Errorf("raw SSE response body = %q, want decoded %q", body.Response, want)
	}
	if strings.HasPrefix(body.Response, `"`) || strings.Contains(body.Response, `\n`) {
		t.Errorf("response still JSON-string-wrapped (quotes / literal \\n): %q", body.Response)
	}
	// The assembled rollup reaches the inspector's Response (assembled) tab.
	if !strings.Contains(body.ResponseAssembled, `"text":"hello world"`) {
		t.Errorf("assembled rollup not captured: %q", body.ResponseAssembled)
	}
	// The partial flag is carried through the event detail.
	if !body.AssemblyPartial {
		t.Errorf("AssemblyPartial = false, want true")
	}
}

// messagesPage is the decode target for the filtered/paged browser endpoint.
type messagesPage struct {
	Entries    []adminc.MessageEntry `json:"entries"`
	NextCursor string                `json:"next_cursor"`
}

// ids pulls the event ids out of a page for set comparisons.
func ids(p messagesPage) []string {
	out := make([]string, len(p.Entries))
	for i, e := range p.Entries {
		out[i] = e.EventID
	}
	return out
}

func TestE2E_MessageBrowserFilters(t *testing.T) {
	svc := startService(t)

	// The single-writer entity is the span, so the browser is fed by spans (not
	// records). The package shares one Postgres database across tests, so sibling
	// tests' events are present too. Namespace every filter dimension with an
	// "mbf-" prefix so exact-match filters return only this test's events, and tag
	// every seeded event with a shared "mbf-all" so the paging assertions scope to
	// exactly these four.
	mk := func(id, provider, model, cfg, endpoint, session, agent, user string, tags []string) {
		all := append([]string{"mbf-all"}, tags...)
		attrs := []*commonpb.KeyValue{
			strKV("sluice.correlation_id", id),
			strKV("gen_ai.provider.name", provider),
			strKV("gen_ai.request.model", model),
			strKV("sluice.configuration", cfg),
			strKV("sluice.protocol", endpoint),
			strKV("gen_ai.conversation.id", session),
			strKV("gen_ai.agent.id", agent),
			strKV("enduser.id", user),
			strSliceKV("sluice.tags", all...),
			intKV("http.response.status_code", 200),
		}
		svc.sendSpan(t, attrs...)
	}
	mk("mb-1", "mbf-anthropic", "mbf-claude", "mbf-dev", "mbf-messages", "mbf-sess-A", "mbf-agent-A", "mbf-user-A", []string{"mbf-eu", "mbf-pii"})
	mk("mb-2", "mbf-openai", "mbf-gpt", "mbf-prod", "mbf-chat", "mbf-sess-A", "mbf-agent-B", "mbf-user-B", []string{"mbf-eu"})
	mk("mb-3", "mbf-openai", "mbf-gpt", "mbf-prod", "mbf-chat", "mbf-sess-B", "mbf-agent-A", "mbf-user-A", []string{"mbf-pii"})
	mk("mb-4", "mbf-gemini", "mbf-gemini2", "mbf-dev", "mbf-generate", "mbf-sess-B", "mbf-agent-B", "mbf-user-B", []string{"mbf-eu", "mbf-pii"})

	// Helper: fetch a page and require 200.
	page := func(query string) messagesPage {
		var p messagesPage
		if code := svc.getJSON(t, "/api/v1/messages?"+query, &p); code != http.StatusOK {
			t.Fatalf("messages?%s: status %d", query, code)
		}
		return p
	}
	hasExactly := func(p messagesPage, want ...string) {
		t.Helper()
		got := ids(p)
		set := map[string]bool{}
		for _, g := range got {
			set[g] = true
		}
		if len(got) != len(want) {
			t.Fatalf("ids = %v, want %v", got, want)
		}
		for _, w := range want {
			if !set[w] {
				t.Fatalf("ids = %v, missing %s", got, w)
			}
		}
	}

	// Single-dimension filters (namespaced values can't collide with siblings).
	hasExactly(page("provider=mbf-openai"), "mb-2", "mb-3")
	hasExactly(page("model=mbf-gemini2"), "mb-4")
	hasExactly(page("configuration=mbf-prod"), "mb-2", "mb-3")
	hasExactly(page("protocol=mbf-messages"), "mb-1")
	hasExactly(page("session_id=mbf-sess-A"), "mb-1", "mb-2")
	hasExactly(page("agent_id=mbf-agent-A"), "mb-1", "mb-3")
	hasExactly(page("user_id=mbf-user-A"), "mb-1", "mb-3")
	hasExactly(page("correlation_id=mb-3"), "mb-3")

	// Tags AND: only records carrying BOTH mbf-eu and mbf-pii.
	hasExactly(page("tags=mbf-eu&tags=mbf-pii"), "mb-1", "mb-4")
	// A single tag is the looser any-of-that-one set.
	hasExactly(page("tags=mbf-pii"), "mb-1", "mb-3", "mb-4")

	// Combined filters intersect.
	hasExactly(page("provider=mbf-openai&session_id=mbf-sess-B"), "mb-3")

	// Keyset paging, scoped to this test's four via the shared tag: limit=2 walks
	// two pages with no overlap, and the cursor clears at the end.
	p1 := page("tags=mbf-all&limit=2")
	if len(p1.Entries) != 2 || p1.NextCursor == "" {
		t.Fatalf("page 1 = %d entries, next=%q", len(p1.Entries), p1.NextCursor)
	}
	p2 := page("tags=mbf-all&limit=2&cursor=" + p1.NextCursor)
	if len(p2.Entries) != 2 {
		t.Fatalf("page 2 = %d entries", len(p2.Entries))
	}
	if p2.NextCursor != "" {
		// A third page must be empty if a cursor was returned.
		if p3 := page("tags=mbf-all&limit=2&cursor=" + p2.NextCursor); len(p3.Entries) != 0 {
			t.Fatalf("page 3 = %d entries, want 0", len(p3.Entries))
		}
	}
	seen := map[string]bool{}
	for _, id := range append(ids(p1), ids(p2)...) {
		if seen[id] {
			t.Fatalf("id %s appeared on two pages", id)
		}
		seen[id] = true
	}
	if len(seen) != 4 {
		t.Fatalf("paged ids = %v, want all 4", seen)
	}

	// Facets enumerate the distinct seeded values (subset assertions — sibling
	// tests contribute their own dimensions to the same shared store).
	var facets struct {
		Providers      []string `json:"providers"`
		Models         []string `json:"models"`
		Configurations []string `json:"configurations"`
		Protocols      []string `json:"protocols"`
		Tags           []string `json:"tags"`
	}
	if code := svc.getJSON(t, "/api/v1/facets", &facets); code != http.StatusOK {
		t.Fatalf("facets status = %d", code)
	}
	contains := func(s []string, v string) bool {
		for _, x := range s {
			if x == v {
				return true
			}
		}
		return false
	}
	for _, v := range []string{"mbf-anthropic", "mbf-openai", "mbf-gemini"} {
		if !contains(facets.Providers, v) {
			t.Errorf("providers %v missing %s", facets.Providers, v)
		}
	}
	if !contains(facets.Protocols, "mbf-chat") || !contains(facets.Protocols, "mbf-generate") {
		t.Errorf("endpoints = %v", facets.Protocols)
	}
	if !contains(facets.Tags, "mbf-eu") || !contains(facets.Tags, "mbf-pii") {
		t.Errorf("tags = %v", facets.Tags)
	}
	// Tags are de-duped: the shared mbf-all appears once despite four records.
	mbfAll := 0
	for _, tg := range facets.Tags {
		if tg == "mbf-all" {
			mbfAll++
		}
	}
	if mbfAll != 1 {
		t.Errorf("mbf-all not de-duped in tags (count=%d): %v", mbfAll, facets.Tags)
	}
}

func TestE2E_ConsoleServedPublic(t *testing.T) {
	svc := startService(t)
	resp, err := http.Get(svc.httpBase + "/") //nolint:noctx // test
	if err != nil {
		t.Fatalf("get /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("console / status = %d, want 200 (public SPA)", resp.StatusCode)
	}
}

// doubleKV builds a double-valued attribute (the shape
// gen_ai.response.time_to_first_chunk rides on the span).
func doubleKV(k string, v float64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: v}}}
}

// TestE2E_SessionSpansDTO proves GET /api/v1/sessions/{id}/spans serves the
// SessionSpansDTO v1 projection (span-dto.schema.json) from ingested gen_ai
// spans alone: per-span scalars + usage (incl. the server_tool_use counter
// map) from span attributes, and the input/output part envelopes from the
// gen_ai content attributes — served whole below the default field cap
// (chars == served length), ordered by at, with the tool_call ->
// tool_call_response join id intact across spans, in the paged
// {spans, next_cursor} envelope.
func TestE2E_SessionSpansDTO(t *testing.T) {
	svc := startService(t)

	// Span 1: assistant answers with text + a client tool call, and reports a
	// server-side web_search alongside.
	svc.sendSpan(t,
		strKV("sluice.correlation_id", "lcs-a"),
		strKV("sluice.session_id", "lifecycle-1"),
		strKV("gen_ai.conversation.id", "lifecycle-1"),
		strKV("gen_ai.provider.name", "anthropic"),
		strKV("gen_ai.request.model", "claude-x"),
		intKV("http.response.status_code", 200),
		intKV("sluice.upstream_status", 200),
		intKV("gen_ai.usage.input_tokens", 100),
		intKV("gen_ai.usage.output_tokens", 25),
		intKV("gen_ai.usage.server_tool_use.web_search_requests", 2),
		strSliceKV("gen_ai.response.finish_reasons", "tool_use"),
		doubleKV("gen_ai.response.time_to_first_chunk", 0.25),
		strKV("gen_ai.input.messages", `[{"role":"user","parts":[{"type":"text","content":"run the tests"}]}]`),
		strKV("gen_ai.output.messages", `[{"role":"assistant","parts":[{"type":"text","content":"on it"},{"type":"reasoning","content":"plan"},{"type":"tool_call","id":"toolu_e2e","name":"Bash","arguments":{"command":"make e2e"}}]}]`),
	)
	// Span 2: the tool continuation — the result joins span 1's call by id.
	svc.sendSpan(t,
		strKV("sluice.correlation_id", "lcs-b"),
		strKV("sluice.session_id", "lifecycle-1"),
		strKV("gen_ai.conversation.id", "lifecycle-1"),
		strKV("gen_ai.provider.name", "anthropic"),
		strKV("gen_ai.request.model", "claude-x"),
		intKV("http.response.status_code", 200),
		strSliceKV("gen_ai.response.finish_reasons", "end_turn"),
		strKV("gen_ai.input.messages", `[{"role":"user","parts":[{"type":"tool_call_response","id":"toolu_e2e","result":"ok: 12 passed"}]}]`),
		strKV("gen_ai.output.messages", `[{"role":"assistant","parts":[{"type":"text","content":"all green"}]}]`),
	)

	var page adminc.SessionSpansPage
	if code := svc.getJSON(t, "/api/v1/sessions/lifecycle-1/spans", &page); code != http.StatusOK {
		t.Fatalf("spans status = %d", code)
	}
	spans := page.Spans
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(spans))
	}
	if page.NextCursor != "" {
		t.Fatalf("next_cursor = %q, want empty (single page)", page.NextCursor)
	}
	// Ordered by at (oldest first); both spans were stamped time.Now() in send
	// order, so span 1 leads.
	a, b := spans[0], spans[1]
	if a.CID != "lcs-a" || b.CID != "lcs-b" {
		t.Fatalf("order = %q, %q; want lcs-a, lcs-b", a.CID, b.CID)
	}
	if !b.At.After(a.At) && !b.At.Equal(a.At) {
		t.Errorf("not ordered by at: %v then %v", a.At, b.At)
	}

	// Span 1 scalars + usage.
	if a.SessionID != "lifecycle-1" || a.ConversationID != "lifecycle-1" {
		t.Errorf("ids = %q / %q", a.SessionID, a.ConversationID)
	}
	if a.Status == nil || *a.Status != 200 {
		t.Errorf("status = %v", a.Status)
	}
	if a.Model == nil || *a.Model != "claude-x" {
		t.Errorf("model = %v", a.Model)
	}
	if a.FinishReason == nil || *a.FinishReason != "tool_use" {
		t.Errorf("finish_reason = %v", a.FinishReason)
	}
	if a.TTFCMs == nil || *a.TTFCMs != 250 {
		t.Errorf("ttfc_ms = %v, want 250", a.TTFCMs)
	}
	if a.LatencyMs == nil || *a.LatencyMs != 200 {
		t.Errorf("latency_ms = %v, want 200 (the sendSpan span width)", a.LatencyMs)
	}
	if a.Usage.Input == nil || *a.Usage.Input != 100 || a.Usage.Output == nil || *a.Usage.Output != 25 {
		t.Errorf("usage = %+v", a.Usage)
	}
	if a.Usage.CacheRead != nil {
		t.Errorf("cache_read = %v, want null (attribute absent)", a.Usage.CacheRead)
	}
	if a.Usage.ServerToolUse["web_search_requests"] != 2 {
		t.Errorf("server_tool_use = %v", a.Usage.ServerToolUse)
	}

	// Span 1 envelopes: text + reasoning + tool_call, full fidelity.
	if len(a.OutputParts) != 3 {
		t.Fatalf("output_parts = %+v", a.OutputParts)
	}
	if a.OutputParts[0].Type != "text" || a.OutputParts[1].Type != "reasoning" {
		t.Errorf("part types = %q, %q", a.OutputParts[0].Type, a.OutputParts[1].Type)
	}
	call := a.OutputParts[2]
	if call.Type != "tool_call" || call.ID != "toolu_e2e" || call.Name != "Bash" {
		t.Fatalf("tool_call part = %+v", call)
	}
	if call.Args == "" || !strings.Contains(call.Args, `"make e2e"`) {
		t.Errorf("args not served whole: %q", call.Args)
	}
	if call.ArgsChars == nil || *call.ArgsChars != len(call.Args) {
		t.Errorf("args_chars = %v, want %d (full fidelity)", call.ArgsChars, len(call.Args))
	}
	if a.OutputText == nil || *a.OutputText != "on it" {
		t.Errorf("output_text = %v", a.OutputText)
	}
	if a.OutputTextChars == nil || *a.OutputTextChars != 5 {
		t.Errorf("output_text_chars = %v", a.OutputTextChars)
	}
	if a.InputText == nil || *a.InputText != "run the tests" {
		t.Errorf("input_text = %v", a.InputText)
	}

	// Span 2: the join material — input tool_call_response carrying span 1's
	// call id, with the full result text.
	if len(b.InputParts) != 1 {
		t.Fatalf("input_parts = %+v", b.InputParts)
	}
	resp := b.InputParts[0]
	if resp.Type != "tool_call_response" || resp.ID != "toolu_e2e" {
		t.Errorf("tool_call_response part = %+v", resp)
	}
	if resp.Text != "ok: 12 passed" || resp.Chars == nil || *resp.Chars != 13 {
		t.Errorf("result not served whole: %+v", resp)
	}
	if b.FinishReason == nil || *b.FinishReason != "end_turn" {
		t.Errorf("finish_reason = %v", b.FinishReason)
	}
	// input_text stays null on a tool-continuation turn (no human text part).
	if b.InputText != nil {
		t.Errorf("input_text = %v, want null", *b.InputText)
	}

	// ?include=structure: the envelope-only projection the dashboard pages
	// fetch — same ids/names/sizes/usage/timing, no content bodies.
	var sp adminc.SessionSpansPage
	if code := svc.getJSON(t, "/api/v1/sessions/lifecycle-1/spans?include=structure", &sp); code != http.StatusOK {
		t.Fatalf("structure status = %d", code)
	}
	if len(sp.Spans) != 2 {
		t.Fatalf("structure spans = %d, want 2", len(sp.Spans))
	}
	sa, sb := sp.Spans[0], sp.Spans[1]
	if len(sa.OutputParts) != 3 || sa.OutputParts[2].ID != "toolu_e2e" || sa.OutputParts[2].Name != "Bash" {
		t.Fatalf("structure envelope lost the part identity: %+v", sa.OutputParts)
	}
	if sa.OutputParts[2].Args != "" {
		t.Errorf("structure page served tool args: %q", sa.OutputParts[2].Args)
	}
	if sa.OutputParts[2].ArgsChars == nil || *sa.OutputParts[2].ArgsChars != *call.ArgsChars {
		t.Errorf("structure args_chars = %v, want %v", sa.OutputParts[2].ArgsChars, *call.ArgsChars)
	}
	if sa.OutputText != nil || sa.InputText != nil {
		t.Errorf("structure page served text bodies: out=%v in=%v", sa.OutputText, sa.InputText)
	}
	if sa.OutputTextChars == nil || *sa.OutputTextChars != 5 || sa.InputTextChars == nil || *sa.InputTextChars != 13 {
		t.Errorf("structure *_chars = %v / %v, want 5 / 13", sa.OutputTextChars, sa.InputTextChars)
	}
	if sa.Usage.Input == nil || *sa.Usage.Input != 100 || sa.TTFCMs == nil || *sa.TTFCMs != 250 {
		t.Errorf("structure page dropped usage/timing: usage=%+v ttfc=%v", sa.Usage, sa.TTFCMs)
	}
	if sb.InputParts[0].Text != "" || sb.InputParts[0].ID != "toolu_e2e" || sb.InputParts[0].Chars == nil || *sb.InputParts[0].Chars != 13 {
		t.Errorf("structure input part = %+v, want join id + size, no body", sb.InputParts[0])
	}

	// GET /sessions/{id}/spans/{cid}: the inspector's lazy fetch — one full
	// element, bodies included.
	var one adminc.SessionSpan
	if code := svc.getJSON(t, "/api/v1/sessions/lifecycle-1/spans/lcs-a", &one); code != http.StatusOK {
		t.Fatalf("single span status = %d", code)
	}
	if one.CID != "lcs-a" || one.OutputText == nil || *one.OutputText != "on it" {
		t.Fatalf("single span = %q / %v, want full bodies", one.CID, one.OutputText)
	}
	if !strings.Contains(one.OutputParts[2].Args, `"make e2e"`) {
		t.Errorf("single span args = %q, want the full payload", one.OutputParts[2].Args)
	}
	// 404 on an unknown cid AND on a cid that belongs to a different session.
	if code := svc.getJSON(t, "/api/v1/sessions/lifecycle-1/spans/no-such-cid", nil); code != http.StatusNotFound {
		t.Errorf("unknown cid status = %d, want 404", code)
	}
	if code := svc.getJSON(t, "/api/v1/sessions/some-other-session/spans/lcs-a", nil); code != http.StatusNotFound {
		t.Errorf("cross-session cid status = %d, want 404", code)
	}
}

// sendSpanBatch exports a batch of pre-built spans over one OTLP connection
// (the bulk path the large-session test uses; per-span dials would dominate
// the runtime at 300 spans).
func sendSpanBatch(t *testing.T, client collectortrace.TraceServiceClient, spans []*tracepb.Span) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := client.Export(ctx, &collectortrace.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
		}},
	})
	if err != nil {
		t.Fatalf("otlp export batch: %v", err)
	}
}

// TestE2E_SessionSpansLargeSessionPaged is the OOM regression e2e (2026-06-10
// prod: a 688-message session of ~410 KB blobs OOM-killed the 512 Mi pod via
// the unbounded session reads). It mirrors the incident shape — ingest content
// cap DISABLED (content_max_bytes: 0) and a session of 300 spans carrying
// ~100 KB content each — and proves through the real binary that:
//
//   - GET /sessions/{id} answers 200 with the full rollup (the narrow
//     projection never materializes the content);
//   - GET /sessions/{id}/spans pages (default 200/page), the cursor walk is
//     stable/ordered/complete, and it equals a single max-size page;
//   - the per-field cap holds: served input_text is span_field_max_bytes
//     while input_text_chars reports the true uncapped size.
//
// Heap assertions are deliberately omitted: the spawned binary exposes no
// runtime-metrics surface, so this stays functional-only; the memory bounds
// are pinned structurally by the store/server unit tests (blob-discipline
// tripwire + paging/cap tests).
func TestE2E_SessionSpansLargeSessionPaged(t *testing.T) {
	svc := startService(t, "content_max_bytes: 0", "span_field_max_bytes: 65536")

	const (
		nSpans      = 300
		contentSize = 100_000
		fieldCap    = 65536
		batchSize   = 25 // ~2.5 MB per export, under the 4 MB gRPC default
	)
	filler := strings.Repeat("x", contentSize)
	inputMessages := `[{"role":"user","parts":[{"type":"text","content":"` + filler + `"}]}]`

	conn, err := grpc.NewClient(svc.otlpAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("otlp dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	client := collectortrace.NewTraceServiceClient(conn)

	base := time.Now().Add(-10 * time.Minute)
	var batch []*tracepb.Span
	for i := 0; i < nSpans; i++ {
		start := base.Add(time.Duration(i) * time.Second)
		batch = append(batch, &tracepb.Span{
			Name:              "gen_ai.request",
			StartTimeUnixNano: uint64(start.UnixNano()),                             //nolint:gosec // test timestamp
			EndTimeUnixNano:   uint64(start.Add(200 * time.Millisecond).UnixNano()), //nolint:gosec // test timestamp
			Attributes: []*commonpb.KeyValue{
				strKV("sluice.correlation_id", fmt.Sprintf("big-%03d", i)),
				strKV("sluice.session_id", "big-session"),
				strKV("gen_ai.conversation.id", "big-session"),
				strKV("gen_ai.provider.name", "anthropic"),
				strKV("gen_ai.request.model", "claude-x"),
				intKV("http.response.status_code", 200),
				intKV("gen_ai.usage.input_tokens", 10),
				intKV("gen_ai.usage.output_tokens", 5),
				strKV("gen_ai.input.messages", inputMessages),
			},
		})
		if len(batch) == batchSize {
			sendSpanBatch(t, client, batch)
			batch = nil
		}
	}
	if len(batch) > 0 {
		sendSpanBatch(t, client, batch)
	}

	// Rollup: 200 with every request and correct totals, content never needed.
	var sess adminc.SessionView
	if code := svc.getJSON(t, "/api/v1/sessions/big-session", &sess); code != http.StatusOK {
		t.Fatalf("session rollup status = %d", code)
	}
	if sess.Totals.Requests != nSpans || len(sess.Requests) != nSpans {
		t.Fatalf("rollup = %d totals / %d requests, want %d", sess.Totals.Requests, len(sess.Requests), nSpans)
	}

	// Cursor walk with the default page size: complete, ordered, no overlap.
	var walked []adminc.SessionSpan
	cursor := ""
	pages := 0
	for {
		path := "/api/v1/sessions/big-session/spans"
		if cursor != "" {
			path += "?cursor=" + cursor
		}
		var page adminc.SessionSpansPage
		if code := svc.getJSON(t, path, &page); code != http.StatusOK {
			t.Fatalf("spans page %d status = %d", pages, code)
		}
		walked = append(walked, page.Spans...)
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 10 {
			t.Fatal("cursor walk did not terminate")
		}
	}
	if pages != 2 {
		t.Errorf("pages = %d, want 2 (default 200 + 100)", pages)
	}
	if len(walked) != nSpans {
		t.Fatalf("walked %d spans, want %d", len(walked), nSpans)
	}
	for i, s := range walked {
		if want := fmt.Sprintf("big-%03d", i); s.CID != want {
			t.Fatalf("walked[%d] = %s, want %s (stable oldest-first order)", i, s.CID, want)
		}
		if i > 0 && walked[i].At.Before(walked[i-1].At) {
			t.Fatalf("walked[%d] at %v precedes walked[%d] at %v", i, walked[i].At, i-1, walked[i-1].At)
		}
	}

	// Per-field cap: served bytes bounded, *_chars carries the true size.
	first := walked[0]
	if first.InputText == nil || len(*first.InputText) != fieldCap {
		t.Fatalf("input_text served %d bytes, want capped %d", len(*first.InputText), fieldCap)
	}
	if first.InputTextChars == nil || *first.InputTextChars != contentSize {
		t.Fatalf("input_text_chars = %v, want true size %d", first.InputTextChars, contentSize)
	}

	// One max-size page equals the cursor walk (content identical to the old
	// single-shot shape, just reassembled).
	var maxPage adminc.SessionSpansPage
	if code := svc.getJSON(t, "/api/v1/sessions/big-session/spans?limit=500", &maxPage); code != http.StatusOK {
		t.Fatalf("max page status = %d", code)
	}
	if len(maxPage.Spans) != nSpans || maxPage.NextCursor != "" {
		t.Fatalf("max page = %d spans, next=%q; want %d, empty", len(maxPage.Spans), maxPage.NextCursor, nSpans)
	}
	for i := range maxPage.Spans {
		if maxPage.Spans[i].CID != walked[i].CID {
			t.Fatalf("max page[%d] = %s, walk = %s — page boundaries changed content", i, maxPage.Spans[i].CID, walked[i].CID)
		}
	}

	// Structure vs full page: payload bytes + wall time, measured on the wire
	// (identity encoding so the transport doesn't gunzip behind our back).
	// The hard assertions are the contract — bodies absent, sizes present,
	// >10x smaller; the timings are t.Logf'd for the perf record.
	const pagePath = "/api/v1/sessions/big-session/spans?limit=200"
	tFull := time.Now()
	code, _, fullBody := svc.getRaw(t, pagePath, "identity")
	fullDur := time.Since(tFull)
	if code != http.StatusOK {
		t.Fatalf("full page status = %d", code)
	}
	tStruct := time.Now()
	code, _, structBody := svc.getRaw(t, pagePath+"&include=structure", "identity")
	structDur := time.Since(tStruct)
	if code != http.StatusOK {
		t.Fatalf("structure page status = %d", code)
	}
	if bytes.Contains(structBody, []byte(strings.Repeat("x", 256))) {
		t.Error("structure page leaked content bodies")
	}
	if len(structBody)*10 > len(fullBody) {
		t.Errorf("structure page = %d bytes vs full %d — expected >10x smaller", len(structBody), len(fullBody))
	}
	var structPage adminc.SessionSpansPage
	if err := json.Unmarshal(structBody, &structPage); err != nil {
		t.Fatalf("decode structure page: %v", err)
	}
	if structPage.Spans[0].InputText != nil {
		t.Error("structure span carries input_text")
	}
	if structPage.Spans[0].InputTextChars == nil || *structPage.Spans[0].InputTextChars != contentSize {
		t.Errorf("structure input_text_chars = %v, want %d", structPage.Spans[0].InputTextChars, contentSize)
	}

	// gzip negotiation on the console API: a client that asks gets a gzip
	// stream that is much smaller than the identity bytes.
	code, hdr, gzBody := svc.getRaw(t, pagePath+"&include=structure", "gzip")
	if code != http.StatusOK {
		t.Fatalf("gzip page status = %d", code)
	}
	if got := hdr.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if len(gzBody) >= len(structBody) {
		t.Errorf("gzip page = %d bytes >= identity %d — not compressing", len(gzBody), len(structBody))
	}

	// The inspector's lazy fetch serves the single full span, field-capped.
	var one adminc.SessionSpan
	if code := svc.getJSON(t, "/api/v1/sessions/big-session/spans/big-000", &one); code != http.StatusOK {
		t.Fatalf("single span status = %d", code)
	}
	if one.InputText == nil || len(*one.InputText) != fieldCap {
		t.Fatalf("single span input_text = %d bytes, want capped %d", len(*one.InputText), fieldCap)
	}
	if one.InputTextChars == nil || *one.InputTextChars != contentSize {
		t.Fatalf("single span input_text_chars = %v, want %d", one.InputTextChars, contentSize)
	}

	t.Logf("page of 200 (~100 KB content/span): full = %d bytes in %v; structure = %d bytes in %v; structure+gzip = %d bytes",
		len(fullBody), fullDur, len(structBody), structDur, len(gzBody))
}
