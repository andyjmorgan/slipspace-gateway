package contentredact

import (
	"bytes"
	"strconv"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// bodyCredentialField names one JSON field inside a provider request body
// whose value is a credential by contract (not merely credential-shaped).
// Each entry is an array path plus the key on every element — the only
// shape needed today. Adding a row here is the whole change for a new
// provider field; the two projection sites (connector Record, admin live
// feed body envelope) call RedactBody unconditionally.
type bodyCredentialField struct {
	// array is the gjson path of the array holding the objects.
	array string
	// key is the field on each element whose value is the credential.
	key string
}

// bodyCredentialFields is the table of body-embedded credentials.
//
//   - Anthropic Messages MCP connector (anthropic-beta: mcp-client-2025-11-20):
//     mcp_servers[].authorization_token is the OAuth bearer the API presents
//     to the remote MCP server.
//     https://platform.claude.com/docs/en/agents-and-tools/mcp-connector
var bodyCredentialFields = []bodyCredentialField{
	{array: "mcp_servers", key: "authorization_token"},
}

// RedactBody returns body with every known body-embedded credential field
// replaced by the mask. It is key-driven, not shape-driven: the value is
// masked whatever it looks like, because the field is a credential by the
// provider's contract. Everything else is left byte-for-byte intact, so a
// body carrying none of the fields is returned unchanged (same backing
// slice). Truncated or otherwise malformed JSON is masked best-effort —
// gjson/sjson tolerate it — because leaking a token out of a damaged body
// is the worse error.
//
// Callers project the result for operators (connector Record, admin live
// feed); the bytes forwarded upstream are never passed through here.
func RedactBody(body []byte) []byte {
	out := body
	for _, f := range bodyCredentialFields {
		// Cheap pre-check so the overwhelming majority of bodies skip the
		// JSON walk entirely.
		if !bytes.Contains(out, []byte(`"`+f.key+`"`)) {
			continue
		}
		arr := gjson.GetBytes(out, f.array)
		if !arr.IsArray() {
			continue
		}
		for i, el := range arr.Array() {
			if !el.Get(f.key).Exists() {
				continue
			}
			path := f.array + "." + strconv.Itoa(i) + "." + f.key
			patched, err := sjson.SetBytes(out, path, mask)
			if err != nil {
				// Unpatchable input: leave the body as it stands rather than
				// drop the record. The gjson parse above already succeeded,
				// so this is effectively unreachable.
				continue
			}
			out = patched
		}
	}
	return out
}
