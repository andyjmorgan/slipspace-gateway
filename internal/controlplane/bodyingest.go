package controlplane

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/klauspost/compress/zstd"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// bodyStore is the write slice of the request_bodies store.
type bodyStore interface {
	UpsertRequestBody(ctx context.Context, b configdb.RequestBody) error
}

// SegmentIngestHandler accepts a connector spool segment — the CP acting as a
// spool destination — and writes each record's full envelope to request_bodies,
// the heavy audit/replay payload joined to request_events by correlation_id.
// POST /api/v1/ingest/segment with an ndjson.zst body.
type SegmentIngestHandler struct {
	store  bodyStore
	logger *slog.Logger
	mux    *http.ServeMux
}

// NewSegmentIngestHandler builds the handler over the request_bodies store.
func NewSegmentIngestHandler(store bodyStore, logger *slog.Logger) *SegmentIngestHandler {
	h := &SegmentIngestHandler{store: store, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/ingest/segment", h.ingest)
	h.mux = mux
	return h
}

func (h *SegmentIngestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *SegmentIngestHandler) ingest(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()
	n, err := IngestSegment(r.Context(), r.Body, h.store)
	if err != nil {
		h.logger.Warn("segment ingest failed", "ingested", n, "error", err)
		writeError(w, http.StatusInternalServerError, "ingest segment")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"ingested": n})
}

// IngestSegment decompresses a connector ndjson.zst segment and upserts each
// record's raw line into the body store (the record's own JSON is the body).
// Malformed lines are skipped, not fatal — a partial segment still yields its
// good records. Returns how many records were stored.
func IngestSegment(ctx context.Context, r io.Reader, store bodyStore) (int, error) {
	dec, err := zstd.NewReader(r)
	if err != nil {
		return 0, err
	}
	defer dec.Close()

	br := bufio.NewReaderSize(dec, 64*1024)
	count := 0
	for {
		line, rerr := br.ReadBytes('\n')
		if stored, perr := storeLine(ctx, line, store); perr != nil {
			return count, perr
		} else {
			count += stored
		}
		if rerr == io.EOF {
			return count, nil
		}
		if rerr != nil {
			return count, rerr
		}
	}
}

type recordHeader struct {
	CorrelationID string `json:"correlation_id"`
	InstanceID    string `json:"instance_id"`
	Seq           uint64 `json:"seq"`
	TsNs          int64  `json:"ts_ns"`
}

func storeLine(ctx context.Context, line []byte, store bodyStore) (int, error) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return 0, nil
	}
	var hdr recordHeader
	if err := json.Unmarshal(trimmed, &hdr); err != nil || hdr.CorrelationID == "" {
		return 0, nil // skip malformed / headerless lines
	}
	if err := store.UpsertRequestBody(ctx, configdb.RequestBody{
		CorrelationID: hdr.CorrelationID,
		InstanceID:    hdr.InstanceID,
		Seq:           hdr.Seq,
		TsNs:          hdr.TsNs,
		Body:          append([]byte(nil), trimmed...),
	}); err != nil {
		return 0, err
	}
	return 1, nil
}
