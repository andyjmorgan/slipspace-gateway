package agentroute

import (
	"github.com/andyjmorgan/slipspace-gateway/internal/bodypatch"
	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
)

// pinActionType is the ActionType discriminator on body patches the pin
// machinery emits, so rewrite metrics attribute them to agent routing rather
// than an authored rule.
const pinActionType = "agentRoutePin"

// lacksEffort reports whether a pinned model rejects the adaptive-thinking
// output_config.effort parameter (400 "This model does not support the
// effort parameter"). Same hardcoded posture as lacks1MContext: today's pin
// candidates are all Claude tiers and the haiku family is the one that
// rejects it; revisit if the candidate set grows.
func lacksEffort(model string) bool {
	return lacks1MContext(model)
}

// ReconcileBody returns the body patches that drop request parameters the
// pinned model cannot serve — the body-side sibling of ReconcileBetas. A
// fable/opus client sends output_config.effort with every request; forwarded
// across a haiku pin the upstream rejects the whole request, breaking the
// conversation the pin was meant to cheapen. When effort is the only
// output_config content the whole block is removed rather than leaving an
// empty object behind. Nil when nothing needs stripping, so the inbound body
// forwards byte-verbatim.
func ReconcileBody(req *messages.MessagesRequest, pinnedModel string) []bodypatch.Op {
	if req == nil || !lacksEffort(pinnedModel) {
		return nil
	}
	oc := req.OutputConfig
	if oc == nil || oc.Effort == "" {
		return nil
	}
	path := "output_config.effort"
	if oc.Format == nil && len(oc.Extra) == 0 {
		path = "output_config"
	}
	return []bodypatch.Op{{Kind: bodypatch.OpRemove, Path: path, ActionType: pinActionType}}
}
