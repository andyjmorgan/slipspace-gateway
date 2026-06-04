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

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/config"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/registry"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// captureStore records the last upserted payload (no DB).
type captureStore struct {
	mu   sync.Mutex
	last store.Payload
	n    int
	err  error
}

func (c *captureStore) UpsertPayload(_ context.Context, p store.Payload) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	c.last = p
	c.n++
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

// post builds a signed (or not) request and runs it through the handler,
// returning the recorder (no http.Response body to close — quiets bodyclose).
func post(t *testing.T, h http.Handler, gwID, sig string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/payload", strings.NewReader(string(body)))
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

func validEnvelope(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(PayloadEnvelope{
		CorrelationID: "corr-1",
		Kind:          store.KindRequestBody,
		InstanceID:    "inst-1",
		Seq:           3,
		TsNs:          1234,
		Body:          json.RawMessage(`{"hello":"world"}`),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestWebhook_Valid(t *testing.T) {
	st := &captureStore{}
	h := NewWebhookHandler(testReg(), st, discard())
	body := validEnvelope(t)
	resp := post(t, h, "gw-a", sign("secret-a", body), body)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	if st.n != 1 {
		t.Fatalf("stored %d, want 1", st.n)
	}
	if st.last.CorrelationID != "corr-1" || st.last.Kind != store.KindRequestBody {
		t.Errorf("stored = %+v", st.last)
	}
	if st.last.GatewayID != "gw-a" {
		t.Errorf("gateway id = %q, want gw-a", st.last.GatewayID)
	}
	if st.last.Seq != 3 || st.last.TsNs != 1234 {
		t.Errorf("seq/ts = %d/%d", st.last.Seq, st.last.TsNs)
	}
}

func TestWebhook_BadSignature(t *testing.T) {
	st := &captureStore{}
	h := NewWebhookHandler(testReg(), st, discard())
	body := validEnvelope(t)
	resp := post(t, h, "gw-a", sign("wrong-secret", body), body)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}
	if st.n != 0 {
		t.Fatalf("stored %d on bad sig, want 0", st.n)
	}
}

func TestWebhook_UnknownGateway(t *testing.T) {
	st := &captureStore{}
	h := NewWebhookHandler(testReg(), st, discard())
	body := validEnvelope(t)
	resp := post(t, h, "gw-x", sign("secret-a", body), body)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}
}

func TestWebhook_MissingHeaders(t *testing.T) {
	h := NewWebhookHandler(testReg(), &captureStore{}, discard())
	body := validEnvelope(t)
	if resp := post(t, h, "", sign("secret-a", body), body); resp.Code != http.StatusUnauthorized {
		t.Fatalf("no gateway id: status = %d, want 401", resp.Code)
	}
	if resp := post(t, h, "gw-a", "", body); resp.Code != http.StatusUnauthorized {
		t.Fatalf("no signature: status = %d, want 401", resp.Code)
	}
}

func TestWebhook_MalformedEnvelope(t *testing.T) {
	st := &captureStore{}
	h := NewWebhookHandler(testReg(), st, discard())
	body := []byte("not json")
	resp := post(t, h, "gw-a", sign("secret-a", body), body)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

func TestWebhook_MissingCorrelationID(t *testing.T) {
	st := &captureStore{}
	h := NewWebhookHandler(testReg(), st, discard())
	body, _ := json.Marshal(PayloadEnvelope{Kind: store.KindRequestBody})
	resp := post(t, h, "gw-a", sign("secret-a", body), body)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

func TestWebhook_UnknownKind(t *testing.T) {
	st := &captureStore{}
	h := NewWebhookHandler(testReg(), st, discard())
	body, _ := json.Marshal(PayloadEnvelope{CorrelationID: "c", Kind: "bogus"})
	resp := post(t, h, "gw-a", sign("secret-a", body), body)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}

func TestWebhook_StoreError(t *testing.T) {
	st := &captureStore{err: io.ErrClosedPipe}
	h := NewWebhookHandler(testReg(), st, discard())
	body := validEnvelope(t)
	resp := post(t, h, "gw-a", sign("secret-a", body), body)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}
