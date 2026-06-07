package main

import (
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/translate"
)

// translationActive reports whether a translate rule retargeted the upstream
// protocol away from the inbound protocol for this request. SourceProtocol is
// set (and differs from the post-rule Protocol) only when a TranslateAction
// ran; a translate whose target equals the source is a no-op.
func translationActive(state *rules.MutableState) bool {
	return state != nil && state.SourceProtocol != "" && state.SourceProtocol != state.Protocol
}

// translatorRegistered reports whether a translator exists for an active
// translation's (source, target) protocol pair. The final handler fails closed
// when translation is active but this returns false — an undeclared or
// unsupported protocol pair must never forward silently (decision #3,
// fail-closed at destination resolution).
func translatorRegistered(state *rules.MutableState) bool {
	_, ok := translate.Lookup(state.SourceProtocol, state.Protocol)
	return ok
}
