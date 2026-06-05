// Package factory builds concrete connector.Connector instances from
// the validated YAML view (contracts/config.Connector). Type-switched
// dispatch — each connector type's New runs against its package and
// every per-type required-field invariant has already passed in
// contracts/config.Connector.Validate by the time we get here.
//
// Only the spool-backed durable types (s3, azure_blob) are built here. The
// webhook type is a real-time, non-spooled pusher realized by
// internal/telemetry/pusher and wired directly in cmd/gateway — it is not a
// connector.Connector and never reaches this factory (the gateway partitions
// it out before BuildAll); Build rejects it as a safety net.
package factory

import (
	"context"
	"fmt"
	"time"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/connector/azureblob"
	"github.com/andyjmorgan/sluice-gateway/internal/connector/s3"
)

// Options bundles the wiring deps every connector type may consume.
// Defaults applied per-impl when fields are zero.
type Options struct {
	// InstanceID is the gateway pod's stable identifier; embedded in
	// the partition path of cloud connectors so concurrent replicas
	// don't collide. "local" when empty.
	InstanceID string

	// Clock is the wall-clock seam for tests. time.Now when nil.
	Clock func() time.Time
}

// BuildAll constructs concrete connectors for every entry in cfgs.
// Returns the slice in the same order so the caller can register
// tracks predictably.
func BuildAll(ctx context.Context, cfgs []contractsconfig.Connector, opts Options) ([]connector.Connector, error) {
	out := make([]connector.Connector, 0, len(cfgs))
	for i := range cfgs {
		c, err := Build(ctx, cfgs[i], opts)
		if err != nil {
			return nil, fmt.Errorf("connectors[%d] %q: %w", i, cfgs[i].Name, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// Build constructs one concrete connector. Switches on cfg.Type. The
// contracts/config validator already enforced shape + per-type
// required fields so this layer trusts what comes in.
func Build(ctx context.Context, cfg contractsconfig.Connector, opts Options) (connector.Connector, error) {
	switch cfg.Type {
	case contractsconfig.ConnectorTypeS3:
		return s3.New(ctx, s3.Options{
			Config:     cfg,
			InstanceID: opts.InstanceID,
			Clock:      opts.Clock,
		})
	case contractsconfig.ConnectorTypeAzureBlob:
		return azureblob.New(ctx, azureblob.Options{
			Config:     cfg,
			InstanceID: opts.InstanceID,
			Clock:      opts.Clock,
		})
	case contractsconfig.ConnectorTypeWebhook:
		return nil, fmt.Errorf("factory: webhook is a real-time pusher, not a spool connector (build it via the telemetry pusher, not here)")
	default:
		return nil, fmt.Errorf("factory: unknown connector type %q", cfg.Type)
	}
}
