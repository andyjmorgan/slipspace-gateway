package ingest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/config"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/registry"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// recordStore captures the event + payloads the Record handler upserts (no DB).
type recordStore struct {
	mu       sync.Mutex
	event    store.RequestEvent
	events   int
	payloads []store.Payload
	eventErr error
	payErr   error
}

func (s *recordStore) UpsertGatewayRecord(_ context.Context, e store.RequestEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eventErr != nil {
		return s.eventErr
	}
	s.event = e
	s.events++
	return nil
}

func (s *recordStore) UpsertPayload(_ context.Context, p store.Payload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.payErr != nil {
		return s.payErr
	}
	s.payloads = append(s.payloads, p)
	return nil
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func testReg() *registry.Registry {
	return registry.New([]config.Gateway{{ID: "gw-a", HMACSecret: "secret-a"}})
}

// post builds a (maybe-signed) request and runs it through the handler,
// returning the recorder.
func post(t *testing.T, h http.Handler, gwID, sig string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/record", strings.NewReader(string(body)))
	if gwID != "" {
		req.Header.Set(HeaderGatewayID, gwID)
	}
	if sig != "" {
		req.Header.Set(HeaderSignature, sig)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func sampleRecord() cc.Record {
	return cc.Record{
		V:             1,
		ID:            "rec-1",
		TsNs:          1_000_000_000,
		Seq:           3,
		InstanceID:    "inst-1",
		CorrelationID: "corr-1",
		Configuration: "default",
		Provider:      "anthropic",
		Protocol:      "messages",
		Model:         "claude-x",
		APIKeyName:    "key-1",
		PolicyRef:     "pol-1",
		Tags:          []string{"a", "b"},
		Request: cc.RequestPart{
			Method:  "POST",
			Headers: map[string]string{"content-type": "application/json"},
			Body:    json.RawMessage(`{"hello":"world"}`),
		},
		Response: cc.ResponsePart{
			Status:       200,
			Body:         json.RawMessage(`{"ok":true}`),
			LastByteNs:   1_500_000_000, // 500ms after start
			StreamChunks: 0,
		},
		UpstreamStatus: 200,
		Tokens:         &cc.Tokens{Input: 10, Output: 20, Cached: 5, CacheCreation: 2},
		RulesFired: []cc.RuleFired{
			{Name: "r1", ActionsApplied: []string{"changeProvider"}, Terminated: false},
			{Name: "r2", Terminated: true},
		},
		Attempts: []cc.Attempt{
			{Target: "primary", StartedAtNs: 1_000_000_000, DurationMs: 80, StatusCode: 503, Outcome: "failure_status"},
			{Target: "backup", StartedAtNs: 1_080_000_000, DurationMs: 100, StatusCode: 200, Outcome: "success"},
		},
		SchemaVersion: cc.SchemaVersion,
	}
}

func recordJSON(t *testing.T, rec cc.Record) []byte {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	return b
}

func TestRecord_Valid(t *testing.T) {
	st := &recordStore{}
	h := NewRecordHandler(testReg(), st, discard())
	body := recordJSON(t, sampleRecord())
	resp := post(t, h, "gw-a", sign("secret-a", body), body)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.events != 1 {
		t.Fatalf("events stored = %d, want 1", st.events)
	}
	e := st.event
	if e.CorrelationID != "corr-1" || e.Configuration != "default" || e.Protocol != "messages" || e.Method != "POST" {
		t.Errorf("gateway columns = %+v", e)
	}
	if e.GatewayID != "gw-a" {
		t.Errorf("gateway id = %q, want gw-a (registry id, not instance id)", e.GatewayID)
	}
	if e.StatusCode != 200 || e.UpstreamStatus != 200 || e.LatencyMs != 500 {
		t.Errorf("outcome = status %d/%d latency %d", e.StatusCode, e.UpstreamStatus, e.LatencyMs)
	}
	if e.TokensIn != 10 || e.TokensOut != 20 {
		t.Errorf("tokens = %+v", e)
	}
	var d store.EventDetail
	if err := json.Unmarshal(e.Detail, &d); err != nil {
		t.Fatalf("detail: %v", err)
	}
	if len(d.Tags) != 2 || len(d.RulesFired) != 2 || len(d.RuleChain) != 2 || len(d.Attempts) != 2 {
		t.Errorf("detail = %+v", d)
	}
	if d.RuleChain[0].Name != "r1" || d.RuleChain[0].ActionsApplied[0] != "changeProvider" || !d.RuleChain[1].Terminated {
		t.Errorf("rule chain = %+v", d.RuleChain)
	}
	if d.Attempts[1].Target != "backup" || d.Attempts[1].Outcome != "success" {
		t.Errorf("attempts = %+v", d.Attempts)
	}
	// request body + response body + request headers (no response headers set).
	kinds := map[string]int{}
	for _, p := range st.payloads {
		kinds[p.Kind]++
		if p.GatewayID != "gw-a" || p.InstanceID != "inst-1" || p.Seq != 3 {
			t.Errorf("payload provenance = %+v", p)
		}
	}
	if kinds[store.KindRequestBody] != 1 || kinds[store.KindResponseBody] != 1 || kinds[store.KindRequestHeaders] != 1 {
		t.Errorf("payload kinds = %+v", kinds)
	}
}

func TestRecord_AssembledRollupStoredAndPartialFlagged(t *testing.T) {
	st := &recordStore{}
	h := NewRecordHandler(testReg(), st, discard())
	rec := sampleRecord()
	rec.Response.StreamChunks = 3
	rec.Response.Assembled = json.RawMessage(`{"id":"msg_x","content":[{"type":"text","text":"hi"}]}`)
	rec.Response.AssemblyPartial = true
	body := recordJSON(t, rec)
	if resp := post(t, h, "gw-a", sign("secret-a", body), body); resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	// The assembled rollup becomes its own sse_rollup payload.
	var rollup *store.Payload
	for i := range st.payloads {
		if st.payloads[i].Kind == store.KindSSERollup {
			rollup = &st.payloads[i]
		}
	}
	if rollup == nil {
		t.Fatal("no sse_rollup payload stored for an assembled response")
	}
	if !strings.Contains(string(rollup.Body), `"text":"hi"`) {
		t.Errorf("sse_rollup body = %s", rollup.Body)
	}

	// The partial flag rides the event detail.
	var d store.EventDetail
	if err := json.Unmarshal(st.event.Detail, &d); err != nil {
		t.Fatalf("detail: %v", err)
	}
	if !d.AssemblyPartial {
		t.Errorf("EventDetail.AssemblyPartial = false, want true; detail = %+v", d)
	}
}

func TestRecord_OmittedBodiesSkipPayloads(t *testing.T) {
	st := &recordStore{}
	h := NewRecordHandler(testReg(), st, discard())
	rec := sampleRecord()
	rec.Request.Body = nil
	rec.Request.BodyOmitted = true
	rec.Response.Body = nil
	rec.Response.BodyOmitted = true
	rec.Request.Headers = nil
	rec.Response.Headers = nil
	body := recordJSON(t, rec)
	if resp := post(t, h, "gw-a", sign("secret-a", body), body); resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.payloads) != 0 {
		t.Errorf("payloads = %d, want 0 (all bodies/headers omitted)", len(st.payloads))
	}
}

func TestRecord_BadSignature(t *testing.T) {
	st := &recordStore{}
	h := NewRecordHandler(testReg(), st, discard())
	body := recordJSON(t, sampleRecord())
	if resp := post(t, h, "gw-a", sign("wrong", body), body); resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}
	if st.events != 0 {
		t.Fatalf("stored on bad sig, want 0")
	}
}

func TestRecord_UnknownGateway(t *testing.T) {
	h := NewRecordHandler(testReg(), &recordStore{}, discard())
	body := recordJSON(t, sampleRecord())
	if resp := post(t, h, "gw-x", sign("secret-a", body), body); resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}
}

func TestRecord_MissingHeaders(t *testing.T) {
	h := NewRecordHandler(testReg(), &recordStore{}, discard())
	body := recordJSON(t, sampleRecord())
	if resp := post(t, h, "", sign("secret-a", body), body); resp.Code != http.StatusUnauthorized {
		t.Fatalf("no gateway id: status = %d, want 401", resp.Code)
	}
	if resp := post(t, h, "gw-a", "", body); resp.Code != http.StatusUnauthorized {
		t.Fatalf("no signature: status = %d, want 401", resp.Code)
	}
}

func TestRecord_Malformed(t *testing.T) {
	h := NewRecordHandler(testReg(), &recordStore{}, discard())
	body := []byte("not json")
	if resp := post(t, h, "gw-a", sign("secret-a", body), body); resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

func TestRecord_MissingCorrelationID(t *testing.T) {
	h := NewRecordHandler(testReg(), &recordStore{}, discard())
	body := recordJSON(t, cc.Record{Provider: "openai"})
	if resp := post(t, h, "gw-a", sign("secret-a", body), body); resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

func TestRecord_EventStoreError(t *testing.T) {
	st := &recordStore{eventErr: io.ErrClosedPipe}
	h := NewRecordHandler(testReg(), st, discard())
	body := recordJSON(t, sampleRecord())
	if resp := post(t, h, "gw-a", sign("secret-a", body), body); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}

func TestRecord_PayloadStoreError(t *testing.T) {
	st := &recordStore{payErr: io.ErrClosedPipe}
	h := NewRecordHandler(testReg(), st, discard())
	body := recordJSON(t, sampleRecord())
	if resp := post(t, h, "gw-a", sign("secret-a", body), body); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}
