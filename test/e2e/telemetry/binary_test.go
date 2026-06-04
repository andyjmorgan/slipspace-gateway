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

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
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

// --- webhook ---

func (s *service) postPayload(t *testing.T, gwID, secret, correlationID, kind string, body string) *http.Response {
	t.Helper()
	env := map[string]any{
		"correlation_id": correlationID,
		"kind":           kind,
		"instance_id":    "i1",
		"seq":            1,
		"ts_ns":          1,
		"body":           json.RawMessage(body),
	}
	raw, _ := json.Marshal(env)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	sig := hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodPost, s.httpBase+"/api/v1/ingest/payload", strings.NewReader(string(raw)))
	req.Header.Set("X-Sluice-Gateway-Id", gwID)
	req.Header.Set("X-Sluice-Signature", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post payload: %v", err)
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

// sendSpan exports one gen_ai span carrying the sluice.* + gen_ai.* attributes
// the extractor reads, then closes the connection.
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

func TestE2E_WebhookTrust(t *testing.T) {
	svc := startService(t)

	// Accepted: correct gateway + signature.
	if resp := svc.postPayload(t, testGatewayID, testSecret, "wh-ok", "request_body", `{"hello":"world"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("trusted webhook status = %d, want 200", resp.StatusCode)
	}
	// Rejected: forged signature.
	if resp := svc.postPayload(t, testGatewayID, "wrong-secret", "wh-bad", "request_body", `{}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged webhook status = %d, want 401", resp.StatusCode)
	}
	// Rejected: unregistered gateway.
	if resp := svc.postPayload(t, "gw-unknown", testSecret, "wh-bad2", "request_body", `{}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unregistered webhook status = %d, want 401", resp.StatusCode)
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

	svc.sendSpan(t,
		strKV("sluice.correlation_id", "otlp-1"),
		strKV("sluice.gateway_id", testGatewayID),
		strKV("sluice.backend", "anthropic"),
		strKV("gen_ai.request.model", "claude-x"),
		strKV("sluice.protocol", "messages"),
		intKV("http.response.status_code", 200),
		intKV("gen_ai.usage.input_tokens", 10),
		intKV("gen_ai.usage.output_tokens", 20),
		strKV("sluice.tags", "alpha,beta"),
	)
	// Also push the captured bodies for the same correlation id.
	svc.postPayload(t, testGatewayID, testSecret, "otlp-1", "request_body", `{"q":"hi"}`)
	svc.postPayload(t, testGatewayID, testSecret, "otlp-1", "response_body", `{"a":"yo"}`)

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
			strKV("sluice.backend", "openai"),
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

func TestE2E_SSERollup(t *testing.T) {
	svc := startService(t)
	svc.postPayload(t, testGatewayID, testSecret, "sse-1", "sse_rollup", `{"assembled":"hello world"}`)
	var body adminc.MessageBodyDetail
	if code := svc.getJSON(t, "/api/v1/messages/sse-1/body", &body); code != http.StatusOK {
		t.Fatalf("body status = %d", code)
	}
	if !strings.Contains(body.ResponseAssembled, "hello world") {
		t.Errorf("sse rollup = %q", body.ResponseAssembled)
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
