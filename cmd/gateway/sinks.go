package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"sync"
	"time"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/pusher"
	"github.com/andyjmorgan/slipspace-gateway/internal/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/spool"
)

// trackUnregisterTimeout bounds the graceful stop of one spool track when
// a connector is removed or edited live. UnregisterTrack aborts a wedged
// upload once this elapses and waits the same again, so a live edit
// blocks the admin write for at most ~2x this in the worst case; the
// common case (idle track) returns in milliseconds. Shorter than
// spoolStopTimeout because a live edit is an operator waiting on an
// HTTP response, not a process exit.
const trackUnregisterTimeout = 10 * time.Second

// pusherSet is the swappable set of webhook pushers keyed by connector
// name. The reporter reads it on every webhook-bound record; the sink
// reconciler replaces entries when a connector is created, edited or
// deleted through the admin write API. A nil *pusherSet reads as empty
// so test wiring can pass nil.
type pusherSet struct {
	mu      sync.RWMutex
	pushers map[string]*pusher.Pusher
}

// newPusherSet wraps the boot-time pusher map. initial may be nil.
func newPusherSet(initial map[string]*pusher.Pusher) *pusherSet {
	if initial == nil {
		initial = map[string]*pusher.Pusher{}
	}
	return &pusherSet{pushers: initial}
}

// get returns the pusher for name, or nil when none is registered.
func (s *pusherSet) get(name string) *pusher.Pusher {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pushers[name]
}

// put registers p under name, replacing any previous entry. The caller
// owns closing a replaced pusher (remove first).
func (s *pusherSet) put(name string, p *pusher.Pusher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushers[name] = p
}

// remove unregisters name and returns the pusher that was there, or nil.
// The reporter stops routing to it the moment this returns; closing it
// is the caller's job.
func (s *pusherSet) remove(name string) *pusher.Pusher {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pushers[name]
	delete(s.pushers, name)
	return p
}

// names returns the registered connector names in lexical order.
func (s *pusherSet) names() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.pushers))
	for n := range s.pushers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// closeAll drains and stops every registered pusher, bounded by timeout
// in total. Shutdown only.
func (s *pusherSet) closeAll(timeout time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	pushers := s.pushers
	s.pushers = map[string]*pusher.Pusher{}
	s.mu.Unlock()
	if len(pushers) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for _, p := range pushers {
		p.Close(ctx)
	}
}

// connectorDiff is the set of runtime-sink actions one config swap
// implies. An edited connector (same name, different settings) appears
// in both lists — its old entry in removed, its new one in added — so
// applying removed-then-added realises the edit as unregister-then-
// register.
type connectorDiff struct {
	removed []contractsconfig.Connector
	added   []contractsconfig.Connector
}

func (d connectorDiff) empty() bool { return len(d.removed) == 0 && len(d.added) == 0 }

// diffConnectors compares two connector lists by name and content and
// returns the actions that take the runtime sinks from old to cur. Pure:
// no I/O, no goroutines, so the reconcile decision is unit-testable
// apart from the wiring that acts on it.
func diffConnectors(old, cur contractsconfig.ConnectorsConfig) connectorDiff {
	prev := make(map[string]contractsconfig.Connector, len(old))
	for _, c := range old {
		prev[c.Name] = c
	}
	next := make(map[string]contractsconfig.Connector, len(cur))
	for _, c := range cur {
		next[c.Name] = c
	}
	var d connectorDiff
	for _, c := range old {
		n, ok := next[c.Name]
		if !ok || connectorFingerprint(n) != connectorFingerprint(c) {
			d.removed = append(d.removed, c)
		}
	}
	for _, c := range cur {
		p, ok := prev[c.Name]
		if !ok || connectorFingerprint(p) != connectorFingerprint(c) {
			d.added = append(d.added, c)
		}
	}
	return d
}

// connectorFingerprint is the content identity of one connector entry:
// its JSON encoding. The contract type is a flat struct of scalars and
// two optional sub-structs with stable field order, so equal settings
// encode identically. A marshal failure (not reachable for this type)
// yields an empty fingerprint, which compares unequal to any real one
// and so errs towards re-creating the sink.
func connectorFingerprint(c contractsconfig.Connector) string {
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return string(b)
}

// sinkReconciler keeps the runtime record sinks — spool tracks for the
// durable connector types, pushers for webhook — in step with the live
// connector list. It subscribes to config.Store (invariant #9: the
// snapshot is the only source of the connector list) and, on every
// swap, diffs the previous connector list against the new one and
// applies the result.
//
// Without it, a connector created or edited through the admin write API
// existed only in the snapshot: no track, no pusher, every bound record
// dropped without a signal until restart (#567).
type sinkReconciler struct {
	ctx    context.Context
	logger *slog.Logger

	spool   *spool.Spool
	pushers *pusherSet

	// buildConnector constructs the spool-backed connector for a
	// non-webhook entry (factory.Build in production; a stub in tests).
	buildConnector func(ctx context.Context, cfg contractsconfig.Connector) (connector.Connector, error)
	// buildPusher constructs the real-time pusher for a webhook entry
	// (newWebhookPusher in production; a stub in tests).
	buildPusher func(cfg contractsconfig.Connector) (*pusher.Pusher, error)

	unregisterTimeout time.Duration

	// mu serialises apply: Store.Replace dispatches subscribers outside
	// its own lock, so two admin writes can fire this concurrently.
	mu   sync.Mutex
	last contractsconfig.ConnectorsConfig
}

// onSnapshot is the config.Store subscriber. Store.Subscribe invokes it
// once immediately with the snapshot in effect; because last is seeded
// with the boot connector list that first call diffs to nothing, so the
// sinks built at boot are left alone.
func (r *sinkReconciler) onSnapshot(cfg *config.ResolvedConfig) {
	if cfg == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d := diffConnectors(r.last, cfg.Connectors)
	r.last = cfg.Connectors
	if d.empty() {
		return
	}
	r.apply(d)
}

// apply realises one diff: every removed connector's sink is torn down
// first, then every added connector's sink is built, so an edit lands as
// unregister-then-register under the same name and the spool backlog
// follows the name (Spool.UnregisterTrack leaves sealed segments on
// disk; the replacement track's RegisterTrack recovers them). A failure
// on one connector is logged and does not stop the others — the loss
// it implies is counted at dispatch time (gateway.spool.dropped.total
// reason=no_track, gateway.telemetry.push.dropped.total reason=no_sink).
//
// Called with mu held.
func (r *sinkReconciler) apply(d connectorDiff) {
	for _, c := range d.removed {
		r.removeSink(c)
	}
	for _, c := range d.added {
		r.addSink(c)
	}
}

func (r *sinkReconciler) removeSink(c contractsconfig.Connector) {
	if c.Type == contractsconfig.ConnectorTypeWebhook {
		p := r.pushers.remove(c.Name)
		if p == nil {
			return
		}
		// The pusher is already unrouted; draining its queue is best
		// effort, bounded by the same window shutdown uses so a wedged
		// receiver cannot hold the admin write open.
		closeCtx, cancel := context.WithTimeout(context.Background(), pushStopTimeout)
		p.Close(closeCtx)
		cancel()
		r.logger.Info("webhook connector removed live", "connector", c.Name)
		return
	}
	if r.spool == nil {
		return
	}
	if err := r.spool.UnregisterTrack(c.Name, r.unregisterTimeout); err != nil {
		r.logger.Error("spool track unregister failed; sealed segments remain on disk for recovery",
			"connector", c.Name, "err", err.Error())
		return
	}
	r.logger.Info("spool connector removed live; sealed segments remain on disk for recovery",
		"connector", c.Name, "type", c.Type)
}

func (r *sinkReconciler) addSink(c contractsconfig.Connector) {
	if c.Type == contractsconfig.ConnectorTypeWebhook {
		p, err := r.buildPusher(c)
		if err != nil {
			r.logger.Error("webhook connector added but its pusher could not be built; bound records will be dropped (reason=no_sink)",
				"connector", c.Name, "err", err.Error())
			return
		}
		r.pushers.put(c.Name, p)
		r.logger.Info("webhook connector enabled live", "connector", c.Name, "url", c.URL, "gateway_id", c.GatewayID)
		return
	}
	if r.spool == nil {
		r.logger.Error("spool connector added but the spool is not running; bound records will be dropped",
			"connector", c.Name, "type", c.Type)
		return
	}
	conn, err := r.buildConnector(r.ctx, c)
	if err != nil {
		r.logger.Error("spool connector added but could not be built; bound records will be dropped (reason=no_track)",
			"connector", c.Name, "type", c.Type, "err", err.Error())
		return
	}
	if err := r.spool.RegisterTrack(spoolTrackOptions(c, conn)); err != nil {
		r.logger.Error("spool connector added but its track could not be registered; bound records will be dropped (reason=no_track)",
			"connector", c.Name, "type", c.Type, "err", err.Error())
		return
	}
	r.logger.Info("spool connector enabled live", "connector", c.Name, "type", c.Type)
}
