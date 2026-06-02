package otlpingest

import (
	"context"
	"encoding/json"
	"log/slog"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/receipt"
)

// eventStore is the slice of the Postgres store the receiver writes through:
// the queryable event row plus the tamper-evidence chain append.
type eventStore interface {
	UpsertRequestEvent(ctx context.Context, e configdb.RequestEvent) error
	AppendReceipt(ctx context.Context, rec receipt.Record, signer receipt.Signer) (receipt.Receipt, error)
}

// Receiver is the CP's OTLP trace ingest: for every gen_ai span a gateway
// pushes, it stores a request_events row and appends a signed receipt to that
// gateway's tamper-evidence chain. It implements the OTLP collector
// TraceService and is registered on the CP gRPC server beside the fleet channel
// (so the bootstrap token already authenticates it). It never enriches beyond
// the gateway-supplied attributes and never sits on a request path (CP-0).
type Receiver struct {
	collectortrace.UnimplementedTraceServiceServer

	store  eventStore
	signer receipt.Signer
	logger *slog.Logger
}

// NewReceiver builds the OTLP trace receiver over store, signing receipts with
// signer.
func NewReceiver(store eventStore, signer receipt.Signer, logger *slog.Logger) *Receiver {
	return &Receiver{store: store, signer: signer, logger: logger}
}

type receiptPayload struct {
	CorrelationID string `json:"correlation_id"`
	GatewayID     string `json:"gateway_id"`
	Configuration string `json:"configuration"`
	Backend       string `json:"backend"`
	Model         string `json:"model"`
	Protocol      string `json:"protocol"`
	StatusCode    int    `json:"status_code"`
	TokensIn      int64  `json:"tokens_in"`
	TokensOut     int64  `json:"tokens_out"`
}

// Export ingests a batch of OTLP spans. A per-span failure is logged and skipped
// — telemetry ingest must never reject the whole batch — so the response is
// always success (partial-success counting is a later refinement).
func (r *Receiver) Export(ctx context.Context, req *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	for _, rs := range req.GetResourceSpans() {
		resourceAttrs := rs.GetResource().GetAttributes()
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				e, ok := EventFromSpan(resourceAttrs, span)
				if !ok {
					continue
				}
				if err := r.store.UpsertRequestEvent(ctx, e); err != nil {
					r.logger.Warn("otlp ingest: upsert request event", "correlation_id", e.CorrelationID, "error", err)
					continue
				}
				// A receipt needs a gateway identity to chain under; skip the
				// (signed) tamper-evidence when the span carries none, but the
				// event is still stored.
				if e.GatewayID == "" {
					continue
				}
				payload, _ := json.Marshal(receiptPayload{
					CorrelationID: e.CorrelationID,
					GatewayID:     e.GatewayID,
					Configuration: e.Configuration,
					Backend:       e.Backend,
					Model:         e.Model,
					Protocol:      e.Protocol,
					StatusCode:    e.StatusCode,
					TokensIn:      e.TokensIn,
					TokensOut:     e.TokensOut,
				})
				if _, err := r.store.AppendReceipt(ctx, receipt.Record{
					GatewayID:     e.GatewayID,
					CorrelationID: e.CorrelationID,
					Payload:       payload,
				}, r.signer); err != nil {
					r.logger.Warn("otlp ingest: append receipt", "correlation_id", e.CorrelationID, "error", err)
				}
			}
		}
	}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}
