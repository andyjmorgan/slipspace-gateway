package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

type fakeBodyStore struct {
	bodies []configdb.RequestBody
	err    error
}

func (f *fakeBodyStore) UpsertRequestBody(_ context.Context, b configdb.RequestBody) error {
	if f.err != nil {
		return f.err
	}
	f.bodies = append(f.bodies, b)
	return nil
}

// segment builds an ndjson.zst spool segment from raw lines.
func segment(t *testing.T, lines ...string) []byte {
	t.Helper()
	var raw bytes.Buffer
	for _, l := range lines {
		raw.WriteString(l)
		raw.WriteByte('\n')
	}
	var out bytes.Buffer
	enc, err := zstd.NewWriter(&out)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	if _, err := enc.Write(raw.Bytes()); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return out.Bytes()
}

func TestIngestSegment(t *testing.T) {
	store := &fakeBodyStore{}
	seg := segment(t,
		`{"correlation_id":"c1","instance_id":"gw","seq":1,"ts_ns":10,"request":{"body":"aGk="}}`,
		`{"correlation_id":"c2","instance_id":"gw","seq":2,"ts_ns":20}`,
		`not json`,             // skipped
		`{"instance_id":"gw"}`, // no correlation id, skipped
		``,                     // blank, skipped
	)

	n, err := IngestSegment(context.Background(), bytes.NewReader(seg), store)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if n != 2 || len(store.bodies) != 2 {
		t.Fatalf("ingested %d (store %d), want 2", n, len(store.bodies))
	}
	if store.bodies[0].CorrelationID != "c1" || store.bodies[0].Seq != 1 || store.bodies[0].TsNs != 10 {
		t.Errorf("body0 = %+v", store.bodies[0])
	}
	// The whole record line is stored as the body.
	if !bytes.Contains(store.bodies[0].Body, []byte(`"request"`)) {
		t.Errorf("body should be the full record line: %s", store.bodies[0].Body)
	}
}

func TestIngestSegment_BadZstd(t *testing.T) {
	if _, err := IngestSegment(context.Background(), bytes.NewReader([]byte("not zstd")), &fakeBodyStore{}); err == nil {
		t.Error("want error for non-zstd input")
	}
}

func TestIngestSegment_StoreError(t *testing.T) {
	seg := segment(t, `{"correlation_id":"c1"}`)
	if _, err := IngestSegment(context.Background(), bytes.NewReader(seg), &fakeBodyStore{err: errors.New("db down")}); err == nil {
		t.Error("want store error propagated")
	}
}

func TestSegmentIngestHandler(t *testing.T) {
	store := &fakeBodyStore{}
	h := NewSegmentIngestHandler(store, slog.New(slog.NewTextHandler(io.Discard, nil)))

	seg := segment(t, `{"correlation_id":"c1","seq":1}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/ingest/segment", bytes.NewReader(seg)))
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	var resp map[string]int
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["ingested"] != 1 {
		t.Errorf("ingested = %d, want 1", resp["ingested"])
	}

	// A bad segment is a 500.
	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/api/v1/ingest/segment", bytes.NewReader([]byte("garbage"))))
	if bad.Code != http.StatusInternalServerError {
		t.Errorf("bad segment = %d, want 500", bad.Code)
	}
}
