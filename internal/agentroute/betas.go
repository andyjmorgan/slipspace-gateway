package agentroute

import (
	"net/http"
	"strings"
)

// betaHeader is the Anthropic beta opt-in request header. Clients send a
// comma-separated token list (possibly across multiple header values).
const betaHeader = "Anthropic-Beta"

// context1MPrefix marks the long-context beta token family
// (context-1m-YYYY-MM-DD).
const context1MPrefix = "context-1m"

// lacks1MContext reports whether a pinned model cannot honour the 1M
// long-context beta. Deliberately hardcoded: today's pin candidates are all
// Claude tiers, and the haiku family is the one the upstream rejects
// outright (400 "The long context beta is not yet available for this
// subscription") when the opt-in is forwarded across a model substitution.
// Revisit if the candidate set ever grows beyond Claude models.
func lacks1MContext(model string) bool {
	return strings.HasPrefix(model, "claude-haiku")
}

// ReconcileBetas drops inbound beta opt-ins the pinned model cannot serve:
// when a pin substitutes a haiku-family model, every context-1m-* token is
// removed from the Anthropic-Beta header. Remaining tokens keep their order,
// and the header disappears entirely when no tokens survive. It reports
// whether anything was stripped; when nothing needs stripping the header is
// left byte-verbatim (passthrough traffic otherwise forwards it untouched).
func ReconcileBetas(h http.Header, pinnedModel string) bool {
	if !lacks1MContext(pinnedModel) {
		return false
	}
	vals := h.Values(betaHeader)
	if len(vals) == 0 {
		return false
	}
	stripped := false
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		toks := strings.Split(v, ",")
		kept := make([]string, 0, len(toks))
		for _, tok := range toks {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(tok)), context1MPrefix) {
				stripped = true
				continue
			}
			kept = append(kept, tok)
		}
		if joined := strings.Trim(strings.Join(kept, ","), ", "); joined == "" {
			continue
		}
		out = append(out, strings.Join(kept, ","))
	}
	if !stripped {
		return false
	}
	h.Del(betaHeader)
	for _, v := range out {
		h.Add(betaHeader, v)
	}
	return true
}
