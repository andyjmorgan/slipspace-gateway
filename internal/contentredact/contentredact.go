// Package contentredact masks credential-shaped tokens in free-text GenAI
// content before it is recorded on a telemetry event.
//
// It is deliberately conservative: only well-known provider key and token
// shapes are masked, each anchored with a minimum length, so legitimate
// prompt/response prose is not mangled. It is not a general PII engine —
// the primary guard on content remains the opt-in flag (default off) and
// the connector spool being the system of record. This is defence in depth
// for the opt-in case so an obvious secret pasted into a prompt does not
// land verbatim on a span/log backend.
package contentredact

import "regexp"

// mask replaces a matched credential.
const mask = "[REDACTED]"

// patterns are credential token shapes. Each requires a substantial
// alphanumeric run to avoid matching ordinary hyphenated words.
var patterns = []*regexp.Regexp{
	// Sluice issued keys (sk_live_… / sk_test_…) — never echo these.
	regexp.MustCompile(`sk_(?:live|test)_[A-Za-z0-9]{8,}`),
	// OpenAI / Anthropic style keys (sk-…, sk-proj-…, sk-ant-…).
	regexp.MustCompile(`sk-(?:proj-|ant-)?[A-Za-z0-9_-]{16,}`),
	// Google API keys.
	regexp.MustCompile(`AIza[A-Za-z0-9_-]{20,}`),
	// Slack tokens.
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),
	// JWTs (three base64url segments).
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
	// Authorization: Bearer <token> (keeps the scheme, masks the token).
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._\-]{8,}`),
}

// Redact replaces credential-shaped tokens in s with a mask. The Bearer
// scheme word is preserved; everything else collapses to the mask.
func Redact(s string) string {
	if s == "" {
		return s
	}
	for i, p := range patterns {
		// The final pattern captures the "bearer " scheme to keep it.
		if i == len(patterns)-1 {
			s = p.ReplaceAllString(s, "${1}"+mask)
			continue
		}
		s = p.ReplaceAllString(s, mask)
	}
	return s
}
