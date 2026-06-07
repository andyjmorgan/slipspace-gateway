package translate

// Drop records one field or feature dropped during translation because the
// target protocol has no equivalent. Translators return a slice of Drops
// alongside the translated bytes; the middleware layer feeds them to the
// always-on lossy counter and the flag-gated lossy response header (see the
// "Cross-Provider Translation" design note, decisions #5/#6).
//
// A Drop is a deliberate, modelled omission — not an unknown field. Unknown
// fields are preserved by the protocol models' DynamicProperties (invariant
// #1); a Drop is for a field we understand but the target cannot express.
type Drop struct {
	// Field is the source-side field or feature path that was dropped, e.g.
	// "top_k", "thinking", or "messages[2].content[0].thinking".
	Field string

	// Reason is a short human-readable explanation, e.g.
	// "no OpenAI chat equivalent".
	Reason string
}

// reasonNoTargetEquivalent is the common Drop reason: the source feature has
// no representation in the target protocol.
const reasonNoTargetEquivalent = "no target-protocol equivalent"
