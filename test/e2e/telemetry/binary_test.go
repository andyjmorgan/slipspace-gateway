//go:build e2e

package telemetry_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
func startService(t *testing.T) *service {
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
	_, err = client.Export(ctx, &collectortrace.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{{
					Name:              "gen_ai.request",
					StartTimeUnixNano: 1_000_000_000,
					EndTimeUnixNano:   1_200_000_000,
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
		Endpoint:       "messages",
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

func TestE2E_OTLPStitchAndDashboard(t *testing.T) {
	svc := startService(t)

	// gen_ai OTLP feed: GenAI semconv + correlation_id only (the join key).
	// model / provider / tokens land on the request_events row's gen_ai columns.
	svc.sendSpan(t,
		strKV("sluice.correlation_id", "otlp-1"),
		strKV("gen_ai.provider.name", "anthropic"),
		strKV("gen_ai.request.model", "claude-x"),
		intKV("http.response.status_code", 200),
		intKV("gen_ai.usage.input_tokens", 10),
		intKV("gen_ai.usage.output_tokens", 20),
	)
	// Record feed: the gateway columns (configuration, protocol, tags) + bodies
	// for the same correlation id. The two feeds merge by correlation_id.
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

	// Dashboard summary counts the request.
	var sum adminc.DashboardSummary
	if code := svc.getJSON(t, "/api/v1/dashboard/summary?window=24h", &sum); code != http.StatusOK {
		t.Fatalf("summary status = %d", code)
	}
	if sum.Totals.Requests < 1 {
		t.Errorf("summary requests = %d, want >= 1", sum.Totals.Requests)
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
	var sess struct {
		SessionID string `json:"session_id"`
		Requests  []any  `json:"requests"`
		Totals    struct {
			Requests int `json:"requests"`
		} `json:"totals"`
	}
	if code := svc.getJSON(t, "/api/v1/sessions/session-1", &sess); code != http.StatusOK {
		t.Fatalf("session status = %d", code)
	}
	if sess.Totals.Requests != 2 {
		t.Errorf("session rollup requests = %d, want 2", sess.Totals.Requests)
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

func TestE2E_StreamingResponseCaptured(t *testing.T) {
	svc := startService(t)
	rec := recordWith("sse-1", `{"q":"hi"}`, `{"assembled":"hello world"}`)
	rec.Response.StreamChunks = 3 // streaming
	svc.postRecord(t, testGatewayID, testSecret, rec)
	var body adminc.MessageBodyDetail
	if code := svc.getJSON(t, "/api/v1/messages/sse-1/body", &body); code != http.StatusOK {
		t.Fatalf("body status = %d", code)
	}
	if !strings.Contains(body.Response, "hello world") {
		t.Errorf("streaming response body = %q", body.Response)
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
