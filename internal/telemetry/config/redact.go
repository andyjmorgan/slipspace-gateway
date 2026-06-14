package config

import (
	"net/url"
	"strings"
)

// RedactedPlaceholder is the literal substituted for every secret-bearing leaf
// in a redacted Config. Fixed and short so an operator scanning the applied
// config in the console sees "***" everywhere a credential lived, with no
// partial reveal. Mirrors internal/admin/configexport.RedactedPlaceholder.
const RedactedPlaceholder = "***"

// Redacted returns a deep copy of the Config with every secret-bearing leaf
// replaced by RedactedPlaceholder: the Postgres DSN password, the console
// password hash, each gateway HMAC secret, and the scanner evidence key. It is
// the read-only "applied config" the operator console serves at
// GET /api/v1/settings — an operator can confirm what is in effect (listeners,
// caps, the scanner block, the gateway registry) without ever seeing a secret.
//
// The walker is deny-by-default in spirit: it copies the struct wholesale, then
// explicitly zeroes the known secret fields. A new secret-bearing field added to
// Config will pass through in cleartext until it is added here — which is the
// intended forcing function (the same discipline as the gateway's config
// export), backed by TestRedacted asserting no secret survives.
func (c Config) Redacted() Config {
	out := c // value copy: scalars + pointers; slices are copied below

	if out.Postgres.DSN != "" {
		out.Postgres.DSN = redactDSNPassword(out.Postgres.DSN)
	}
	if out.Console.PasswordHash != "" {
		out.Console.PasswordHash = RedactedPlaceholder
	}
	if out.Scanner.EvidenceKey != "" {
		out.Scanner.EvidenceKey = RedactedPlaceholder
	}

	// Copy the gateway slice before mutating so the live config (still held by
	// the running service) is never touched.
	if len(c.Gateways) > 0 {
		gws := make([]Gateway, len(c.Gateways))
		copy(gws, c.Gateways)
		for i := range gws {
			if gws[i].HMACSecret != "" {
				gws[i].HMACSecret = RedactedPlaceholder
			}
		}
		out.Gateways = gws
	}

	// Detectors carry no secret today, but copy the slice so the returned Config
	// never aliases the live one's backing array.
	if len(c.Scanner.Detectors) > 0 {
		ds := make([]Detector, len(c.Scanner.Detectors))
		copy(ds, c.Scanner.Detectors)
		out.Scanner.Detectors = ds
	}

	return out
}

// redactDSNPassword replaces only the password in a libpq/pgx URL-style DSN
// (postgres://user:pw@host/db…), preserving the rest (user, host, db, query
// params) so the operator can still see where the service stores. Only the
// URL form is handled surgically; any DSN that is NOT a postgres:// URL — most
// importantly the keyword/value "host=… password=… " form, which can embed the
// password in arbitrary places — is redacted wholesale, since we cannot safely
// edit a shape we did not parse.
//
// The surgery is done on the raw string rather than via url.URL.String(), which
// percent-encodes the userinfo and would mangle the "***" placeholder.
func redactDSNPassword(dsn string) string {
	const sep = "://"
	scheme := strings.Index(dsn, sep)
	if scheme < 0 {
		// Not a URL DSN (keyword/value form, or junk): redact the whole thing.
		return RedactedPlaceholder
	}
	if _, err := url.Parse(dsn); err != nil {
		return RedactedPlaceholder
	}
	rest := dsn[scheme+len(sep):]
	// userinfo is everything up to the first '@' (host part has no '@').
	at := strings.Index(rest, "@")
	if at < 0 {
		return dsn // no userinfo, nothing to redact
	}
	userinfo := rest[:at]
	colon := strings.Index(userinfo, ":")
	if colon < 0 {
		return dsn // user only, no password
	}
	user := userinfo[:colon]
	return dsn[:scheme+len(sep)] + user + ":" + RedactedPlaceholder + rest[at:]
}
