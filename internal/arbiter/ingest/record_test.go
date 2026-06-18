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
	"time"

	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/registry"
)

// recordStore captures the verbatim blob the Record handler upserts (no DB).
type recordStore struct {
	mu       sync.Mutex
	corrID   string
	body     []byte
	stored   int
	storeErr error
}

func (s *recordStore) UpsertRecord(_ context.Context, correlationID string, _ time.Time, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storeErr != nil {
		return s.storeErr
	}
	s.corrID = correlationID
	s.body = body
	s.stored++
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
		AgentID:       "agt-1",
		AgentIDSource: "X-Claude-Code-Agent-Id",
		UserID:        "usr-1",
		UserIDSource:  "X-Sluice-User-Id",
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
	if st.stored != 1 {
		t.Fatalf("records stored = %d, want 1", st.stored)
	}
	if st.corrID != "corr-1" {
		t.Errorf("correlation id = %q, want corr-1", st.corrID)
	}
	// The handler stores the RAW request bytes verbatim — byte-for-byte the
	// signed body — and never fans them into columns/detail/payload rows.
	if string(st.body) != string(body) {
		t.Errorf("stored body not verbatim:\n got %s\nwant %s", st.body, body)
	}
	// The verbatim blob deserializes back into the full cc.Record the inspector
	// reads lazily.
	var rec cc.Record
	if err := json.Unmarshal(st.body, &rec); err != nil {
		t.Fatalf("stored blob does not decode: %v", err)
	}
	if rec.Configuration != "default" || len(rec.RulesFired) != 2 || len(rec.Attempts) != 2 {
		t.Errorf("decoded record = %+v", rec)
	}
}

func TestRecord_BadSignature(t *testing.T) {
	st := &recordStore{}
	h := NewRecordHandler(testReg(), st, discard())
	body := recordJSON(t, sampleRecord())
	if resp := post(t, h, "gw-a", sign("wrong", body), body); resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}
	if st.stored != 0 {
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

func TestRecord_StoreError(t *testing.T) {
	st := &recordStore{storeErr: io.ErrClosedPipe}
	h := NewRecordHandler(testReg(), st, discard())
	body := recordJSON(t, sampleRecord())
	if resp := post(t, h, "gw-a", sign("secret-a", body), body); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}
