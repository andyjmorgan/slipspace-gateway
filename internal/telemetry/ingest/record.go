package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/registry"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// Webhook headers a gateway sets on each Record push.
const (
	// HeaderGatewayID names the registered gateway the push claims to be from.
	HeaderGatewayID = "X-Sluice-Gateway-Id"
	// HeaderSignature carries the hex HMAC-SHA256 of the raw request body.
	HeaderSignature = "X-Sluice-Signature"
)

// maxRecordBytes caps a single Record POST. The gateway bounds captured bodies
// at 10 MiB inbound; this leaves headroom for the JSON envelope around them. A
// runaway record is rejected rather than buffered without limit.
const maxRecordBytes = 16 << 20 // 16 MiB

// recordVerifier is the registry slice the handler needs: HMAC trust.
type recordVerifier interface {
	Verify(gatewayID string, body []byte, sigHex string) error
}

// recordWriter is the store slice the Record handler writes through: the
// gateway half of the request event plus the captured payload rows.
type recordWriter interface {
	UpsertGatewayRecord(ctx context.Context, e store.RequestEvent) error
	UpsertPayload(ctx context.Context, p store.Payload) error
}

// RecordHandler accepts one HMAC-trusted cc.Record per HTTP request — the full
// per-request digital record the gateway pushes in real time (telemetry design
// channel 3) — and fans it into the store: the gateway columns of its
// request_events row (configuration, protocol, method, status, rule chain,
// attempts, …) plus one request_payloads row per captured body/header item. It
// is the gateway→service trust boundary: a record is stored iff its signature
// verifies against the claimed gateway's registered secret.
type RecordHandler struct {
	registry recordVerifier
	store    recordWriter
	logger   *slog.Logger
}

// NewRecordHandler builds the Record ingest handler.
func NewRecordHandler(reg recordVerifier, st recordWriter, logger *slog.Logger) *RecordHandler {
	return &RecordHandler{registry: reg, store: st, logger: logger}
}

// ServeHTTP handles POST of a single cc.Record. It verifies the HMAC, decodes
// the record, upserts the gateway half of the request event, then upserts a
// payload row per present body/header.
func (h *RecordHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()

	gatewayID := r.Header.Get(HeaderGatewayID)
	sig := r.Header.Get(HeaderSignature)
	if gatewayID == "" || sig == "" {
		writeError(w, http.StatusUnauthorized, "missing gateway id or signature")
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRecordBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "record too large")
		return
	}

	if err := h.registry.Verify(gatewayID, raw, sig); err != nil {
		// Both unknown-gateway and bad-signature collapse to 401 — never reveal
		// which check failed.
		if errors.Is(err, registry.ErrUnknownGateway) || errors.Is(err, registry.ErrBadSignature) {
			writeError(w, http.StatusUnauthorized, "signature rejected")
			return
		}
		writeError(w, http.StatusInternalServerError, "verify")
		return
	}

	var rec cc.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		writeError(w, http.StatusBadRequest, "malformed record")
		return
	}
	if rec.CorrelationID == "" {
		writeError(w, http.StatusBadRequest, "missing correlation_id")
		return
	}

	if err := h.store.UpsertGatewayRecord(r.Context(), eventFromRecord(rec, gatewayID)); err != nil {
		h.logger.Warn("record ingest: upsert event", "correlation_id", rec.CorrelationID, "error", err)
		writeError(w, http.StatusInternalServerError, "store event")
		return
	}

	stored := 1
	for _, p := range payloadsFromRecord(rec, gatewayID) {
		if err := h.store.UpsertPayload(r.Context(), p); err != nil {
			h.logger.Warn("record ingest: upsert payload", "correlation_id", rec.CorrelationID, "kind", p.Kind, "error", err)
			writeError(w, http.StatusInternalServerError, "store payload")
			return
		}
		stored++
	}

	writeJSON(w, http.StatusOK, map[string]int{"stored": stored})
}

// eventFromRecord maps a cc.Record to the gateway half of a request event. The
// gen_ai columns (provider/model/tokens/...) are also populated so a Record that
// creates the row (push enabled, OTLP off) still carries them; on conflict the
// OTLP feed owns them (see store.UpsertGatewayRecord's set clause). GatewayID is
// the registry id from the verified header, not the record's instance id.
func eventFromRecord(rec cc.Record, gatewayID string) store.RequestEvent {
	e := store.RequestEvent{
		CorrelationID:   rec.CorrelationID,
		GatewayID:       gatewayID,
		Configuration:   rec.Configuration,
		Provider:        rec.Provider,
		Model:           rec.Model,
		Protocol:        rec.Protocol,
		Method:          rec.Request.Method,
		StatusCode:      rec.Response.Status,
		UpstreamStatus:  rec.UpstreamStatus,
		LatencyMs:       recordLatencyMs(rec),
		SessionID:       rec.SessionID,
		SessionIDSource: rec.SessionIDSource,
		APIKeyName:      rec.APIKeyName,
		PolicyRef:       rec.PolicyRef,
		Streaming:       rec.Response.StreamChunks > 0,
		Detail:          detailFromRecord(rec),
	}
	if rec.Tokens != nil {
		e.TokensIn = int64(rec.Tokens.Input)
		e.TokensOut = int64(rec.Tokens.Output)
		e.TokensCached = int64(rec.Tokens.Cached)
		e.TokensCacheCreation = int64(rec.Tokens.CacheCreation)
	}
	return e
}

// recordLatencyMs derives request wall time from the record's start (TsNs) and
// last-byte time. Zero when the timestamps are absent or non-monotonic (e.g. a
// transport failure before the response completed).
func recordLatencyMs(rec cc.Record) int64 {
	if rec.TsNs == 0 || rec.Response.LastByteNs <= rec.TsNs {
		return 0
	}
	return (rec.Response.LastByteNs - rec.TsNs) / 1_000_000
}

// detailFromRecord packs the record's gateway-only request facts into the
// request_events.detail JSONB: post-rule tags, the flat fired-rule name list,
// the full per-rule chain, and the resilience attempts. Returns nil when there
// is nothing to record so the column stays SQL NULL.
func detailFromRecord(rec cc.Record) []byte {
	d := store.EventDetail{Tags: rec.Tags, AssemblyPartial: rec.Response.AssemblyPartial}
	for _, rf := range rec.RulesFired {
		d.RulesFired = append(d.RulesFired, rf.Name)
		d.RuleChain = append(d.RuleChain, store.RuleChainEntry{
			Name:           rf.Name,
			ActionsApplied: rf.ActionsApplied,
			Terminated:     rf.Terminated,
			ErrorMessage:   rf.ErrorMessage,
		})
	}
	for _, a := range rec.Attempts {
		d.Attempts = append(d.Attempts, store.AttemptDetail{
			Target:      a.Target,
			StartedAtNs: a.StartedAtNs,
			DurationMs:  a.DurationMs,
			StatusCode:  a.StatusCode,
			Error:       a.Error,
			Outcome:     a.Outcome,
		})
	}
	if len(d.Tags) == 0 && len(d.RuleChain) == 0 && len(d.Attempts) == 0 && !d.AssemblyPartial {
		return nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil
	}
	return b
}

// payloadsFromRecord derives the captured-item rows from a record: the request
// and response bodies (when present and not stripped by the binding's oversize
// cap) and the request/response header maps. Each shares the record's
// (instance_id, seq, ts_ns) so cross-instance ordering uses the stable composite
// key (invariant #8). Bodies marked omitted contribute no row.
func payloadsFromRecord(rec cc.Record, gatewayID string) []store.Payload {
	var out []store.Payload
	add := func(kind string, body []byte) {
		if len(body) == 0 {
			return
		}
		out = append(out, store.Payload{
			CorrelationID: rec.CorrelationID,
			Kind:          kind,
			InstanceID:    rec.InstanceID,
			Seq:           rec.Seq,
			TsNs:          rec.TsNs,
			GatewayID:     gatewayID,
			Body:          body,
		})
	}
	if !rec.Request.BodyOmitted {
		add(store.KindRequestBody, rec.Request.Body)
	}
	if !rec.Response.BodyOmitted {
		add(store.KindResponseBody, rec.Response.Body)
	}
	// The accumulator's assembled rollup of a streamed response — the
	// inspector's "Response (assembled)" tab. add() no-ops when empty
	// (non-streaming, unrecognised stream, or stripped on oversize).
	add(store.KindSSERollup, rec.Response.Assembled)
	add(store.KindRequestHeaders, marshalHeaders(rec.Request.Headers))
	add(store.KindResponseHeaders, marshalHeaders(rec.Response.Headers))
	return out
}

// marshalHeaders renders a header map to JSON bytes, or nil when empty so the
// caller emits no payload row.
func marshalHeaders(h map[string]string) []byte {
	if len(h) == 0 {
		return nil
	}
	b, err := json.Marshal(h)
	if err != nil {
		return nil
	}
	return b
}

// writeJSON writes a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON {"error":msg} body with the given status.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
