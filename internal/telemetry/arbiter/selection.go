package arbiter

// Check types. A detector is configured per check type; the explode step only
// emits tasks for configured check types, and the dispatcher only calls their
// endpoints.
const (
	CheckInjection = "injection"
	CheckToxicity  = "toxicity"
	CheckPII       = "pii"
)

// Roles a unit may carry.
const (
	roleUser      = "user"
	roleAssistant = "assistant"
)

// appliesTo is the selection policy (ADR-018): which check types consume which
// content blocks. Selection is policy, not just plumbing — injection looks at
// the untrusted surfaces (tool results, user text, residual), not the system
// prompt; toxicity at user/assistant text; PII anywhere text or tool payloads
// appear. An opaque (residual-captured) unit always goes to injection so an
// unmodeled block can never slip an injection through unscanned (#1).
func appliesTo(checkType string, u Unit) bool {
	switch checkType {
	case CheckInjection:
		switch u.Kind {
		case kindToolCallResponse, kindOpaque:
			return true
		case kindText:
			return u.Role == roleUser
		default:
			return false
		}
	case CheckToxicity:
		return u.Kind == kindText && (u.Role == roleUser || u.Role == roleAssistant)
	case CheckPII:
		switch u.Kind {
		case kindText, kindToolCall, kindToolCallResponse:
			return true
		default:
			return false
		}
	default:
		return false
	}
}
