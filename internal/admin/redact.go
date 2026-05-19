package admin

// RedactedSecret is the wire shape every secret-bearing field is projected
// into before leaving the admin handlers. Length + last-four lets operators
// visually confirm a value without exposing the whole credential. The
// original secret is never serialised.
type RedactedSecret struct {
	// Length is the rune count of the original secret, before redaction.
	Length int `json:"length"`

	// Last4 is the last four runes of the original secret. When the secret
	// is shorter than four runes the entire value is returned — operator
	// visibility wins for short tokens, where length already telegraphs
	// "this is a non-credential placeholder".
	Last4 string `json:"last4"`
}

// redact projects an arbitrary secret string onto its visible-but-safe form.
// Empty input returns an empty RedactedSecret so JSON renders {length:0,last4:""}
// rather than null — keeps the SPA's type assumptions stable.
func redact(secret string) RedactedSecret {
	runes := []rune(secret)
	n := len(runes)
	if n == 0 {
		return RedactedSecret{}
	}
	if n <= 4 {
		return RedactedSecret{Length: n, Last4: string(runes)}
	}
	return RedactedSecret{Length: n, Last4: string(runes[n-4:])}
}

// redactMap projects a map of provider → upstream-credential through redact,
// preserving the keys so the SPA can render "openai · ••••abcd" rows.
func redactMap(in map[string]string) map[string]RedactedSecret {
	if len(in) == 0 {
		return map[string]RedactedSecret{}
	}
	out := make(map[string]RedactedSecret, len(in))
	for k, v := range in {
		out[k] = redact(v)
	}
	return out
}
