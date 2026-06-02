// Package reconciler is the gateway-side client of the control plane: it
// registers the gateway on startup and heartbeats on an interval, entirely out
// of band from the request path.
//
// Cardinal invariant CP-0 (design note "Central Control Plane"): the control
// plane is never on the data-plane request path. Run is launched in a
// background goroutine bound to the process context; a dial failure, an RPC
// error, or an unreachable control plane never blocks the gateway or fails a
// request — they are logged and the heartbeat loop retries.
package reconciler

import (
	"context"
	"crypto/tls"
	"errors"
	"time"

	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	fleetpb "github.com/andyjmorgan/sluice-gateway/internal/controlplane/fleetpb"
)

// rpcTimeout bounds a single Register/Heartbeat call so a hung control plane
// cannot wedge the loop — the next tick retries.
const rpcTimeout = 10 * time.Second

// Options configures a Reconciler.
type Options struct {
	// Endpoint is the control-plane gRPC target (host:port). Required.
	Endpoint string

	// Token is the bootstrap token sent as call metadata. Empty omits auth.
	Token string

	// TLS selects transport security. When true, TLSConfig (or a default
	// system-roots config) wraps the connection; when false, the channel is
	// plaintext — trusted-network only.
	TLS bool

	// TLSConfig overrides the client TLS config (e.g. a custom CA). Ignored
	// when TLS is false; defaulted to system roots when TLS is true and this
	// is nil.
	TLSConfig *tls.Config

	// GatewayID is this instance's stable identity. Required.
	GatewayID string

	// Version is the gateway binary version reported to the control plane.
	Version string

	// Labels is operator metadata reported to the control plane.
	Labels map[string]string

	// Applied, when set, supplies the hash of the config closure this gateway is
	// currently serving, reported on heartbeat so the control plane can detect
	// config drift. Nil for register-only / standalone gateways.
	Applied *AppliedHash

	// Interval is the heartbeat cadence. Defaults to 20s when non-positive.
	Interval time.Duration

	// Logger is used for out-of-band status. May be nil.
	Logger *slog.Logger
}

// Reconciler maintains a gateway's registration with the control plane.
type Reconciler struct {
	opts Options
}

// New validates opts and constructs a Reconciler. Returns an error when the
// endpoint or gateway id is missing.
func New(opts Options) (*Reconciler, error) {
	if opts.Endpoint == "" {
		return nil, errors.New("reconciler: endpoint is required")
	}
	if opts.GatewayID == "" {
		return nil, errors.New("reconciler: gateway id is required")
	}
	if opts.Interval <= 0 {
		opts.Interval = 20 * time.Second
	}
	return &Reconciler{opts: opts}, nil
}

// Run dials the control plane, registers once, then heartbeats on Interval
// until ctx is cancelled. It never returns an error — every failure is logged
// and retried (or, for a dial-construction failure, logged once and the loop
// exits) so a control-plane problem can never propagate into the data path.
// Intended to be launched via safego.Go bound to the process context.
func (r *Reconciler) Run(ctx context.Context) {
	conn, err := r.dial()
	if err != nil {
		r.logError("control-plane dial setup failed; reconciler not started", err)
		return
	}
	defer func() { _ = conn.Close() }()

	client := fleetpb.NewFleetServiceClient(conn)
	r.register(ctx, client)

	ticker := time.NewTicker(r.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.heartbeat(ctx, client)
		}
	}
}

func (r *Reconciler) dial() (*grpc.ClientConn, error) {
	return dialControlPlane(r.opts.Endpoint, r.opts.Token, r.opts.TLS, r.opts.TLSConfig)
}

// dialControlPlane builds a lazy gRPC client to the control plane with the
// channel's transport security and the per-RPC bootstrap token. Shared by the
// reconciler and the config syncer.
func dialControlPlane(endpoint, token string, useTLS bool, tlsCfg *tls.Config) (*grpc.ClientConn, error) {
	var transport credentials.TransportCredentials
	if useTLS {
		cfg := tlsCfg
		if cfg == nil {
			cfg = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		transport = credentials.NewTLS(cfg)
	} else {
		transport = insecure.NewCredentials()
	}

	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(transport)}
	if token != "" {
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(tokenCreds{
			token:  token,
			secure: useTLS,
		}))
	}
	return grpc.NewClient(endpoint, dialOpts...)
}

func (r *Reconciler) register(ctx context.Context, client fleetpb.FleetServiceClient) {
	callCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	if _, err := client.Register(callCtx, &fleetpb.RegisterRequest{
		GatewayId: r.opts.GatewayID,
		Version:   r.opts.Version,
		Labels:    r.opts.Labels,
	}); err != nil {
		// Non-fatal: the server self-registers on heartbeat, so a missed
		// Register self-heals on the next tick.
		r.logWarn("control-plane register failed; will retry via heartbeat", err)
		return
	}
	r.logInfo("registered with control plane")
}

func (r *Reconciler) heartbeat(ctx context.Context, client fleetpb.FleetServiceClient) {
	callCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	var cachedHashes []string
	if h := r.opts.Applied.Get(); h != "" {
		cachedHashes = []string{h}
	}
	if _, err := client.Heartbeat(callCtx, &fleetpb.HeartbeatRequest{
		GatewayId:          r.opts.GatewayID,
		Version:            r.opts.Version,
		Labels:             r.opts.Labels,
		CachedConfigHashes: cachedHashes,
	}); err != nil {
		r.logWarn("control-plane heartbeat failed", err)
	}
}

func (r *Reconciler) logInfo(msg string) {
	if r.opts.Logger != nil {
		r.opts.Logger.Info(msg, "endpoint", r.opts.Endpoint, "gateway_id", r.opts.GatewayID)
	}
}

func (r *Reconciler) logWarn(msg string, err error) {
	if r.opts.Logger != nil {
		r.opts.Logger.Warn(msg, "endpoint", r.opts.Endpoint, "err", err.Error())
	}
}

func (r *Reconciler) logError(msg string, err error) {
	if r.opts.Logger != nil {
		r.opts.Logger.Error(msg, "endpoint", r.opts.Endpoint, "err", err.Error())
	}
}

// tokenCreds attaches the bootstrap token as call metadata. RequireTransport
// Security mirrors the channel's TLS setting so the token may ride a plaintext
// channel on a trusted network (gRPC otherwise refuses per-RPC creds without
// transport security).
type tokenCreds struct {
	token  string
	secure bool
}

func (t tokenCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + t.token}, nil
}

func (t tokenCreds) RequireTransportSecurity() bool { return t.secure }
