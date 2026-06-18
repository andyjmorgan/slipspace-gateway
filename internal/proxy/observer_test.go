package proxy

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/contracts/events"
)

const (
	eventStart    = "start"
	eventHeaders  = "headers"
	eventChunk    = "chunk"
	eventError    = "upstream_error"
	eventComplete = "complete"
	eventRule     = "rule_matched"
)

type observerEvent struct {
	kind       string
	statusCode int
	streaming  bool
	headers    http.Header
	durationMs int64
	err        error
	dest       Destination
	rule       events.RuleMatched
	chunkAt    time.Time
}

type recordingObserver struct {
	mu     sync.Mutex
	events []observerEvent
}

func newRecordingObserver() *recordingObserver { return &recordingObserver{} }

func (o *recordingObserver) snapshot() []observerEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]observerEvent, len(o.events))
	copy(out, o.events)
	return out
}

func (o *recordingObserver) OnRequestStart(_ context.Context, dest Destination) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, observerEvent{kind: eventStart, dest: dest})
}

func (o *recordingObserver) OnResponseHeaders(_ context.Context, status int, h http.Header, streaming bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, observerEvent{
		kind:       eventHeaders,
		statusCode: status,
		streaming:  streaming,
		headers:    h.Clone(),
	})
}

func (o *recordingObserver) OnResponseChunk(_ context.Context, at time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, observerEvent{kind: eventChunk, chunkAt: at})
}

func (o *recordingObserver) OnUpstreamError(_ context.Context, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, observerEvent{kind: eventError, err: err})
}

func (o *recordingObserver) OnComplete(_ context.Context, status int, durationMs int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, observerEvent{
		kind:       eventComplete,
		statusCode: status,
		durationMs: durationMs,
	})
}

func (o *recordingObserver) OnRuleMatched(_ context.Context, match events.RuleMatched) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, observerEvent{kind: eventRule, rule: match})
}

// staticObserver returns an ObserverFactory that hands every Forward the
// same Observer instance. Tests that own a recordingObserver and want to
// read its events after the call use this to bridge to the factory API.
func staticObserver(o Observer) ObserverFactory {
	return func(context.Context, Destination) Observer { return o }
}
