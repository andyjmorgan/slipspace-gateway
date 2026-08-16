package selection_test

import (
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/selection"
)

// knownProtocols is the closed set ProtocolForPath may return on a match.
// Declared here rather than derived from the package so that a protocol
// added to selection without a deliberate update to this test fails the
// fuzz rather than silently widening the contract.
var knownProtocols = map[string]struct{}{
	selection.ProtocolChat:            {},
	selection.ProtocolResponses:       {},
	selection.ProtocolMessages:        {},
	selection.ProtocolGenerateContent: {},
	selection.ProtocolEmbeddings:      {},
}

// FuzzProtocolForPath fuzzes gateway URL-path route detection, one of the
// two legs CLAUDE.md names explicitly ("fuzz every UnmarshalJSON + the
// YAML loader + route detection").
//
// The input is attacker-controlled in the most literal sense: it is the
// request path, taken straight off the wire before any authentication has
// happened. The properties asserted are the ones the rest of routing
// relies on rather than merely "it did not panic":
//
//   - a match returns a protocol from the closed known set;
//   - a miss returns the zero values, so a caller that ignores ok cannot
//     act on a half-populated result;
//   - generate_content always yields both params, with a single-segment
//     model and a recognised op — the downstream target builder indexes
//     both keys unconditionally;
//   - the function is deterministic and free of hidden state.
func FuzzProtocolForPath(f *testing.F) {
	seeds := []string{
		"",
		"/",
		"/v1/chat/completions",
		"/chat/completions",
		"/v1/responses",
		"/responses",
		"/v1/messages",
		"/messages",
		"/v1/embeddings",
		"/embeddings",
		"/v1beta/models/gemini-2.0-flash:generateContent",
		"/v1beta/models/gemini-2.0-flash:streamGenerateContent",
		// Near-misses that must NOT match.
		"/v1beta/models/:generateContent",             // empty model
		"/v1beta/models/a/b:generateContent",          // model spans segments
		"/v1beta/models/gemini:countTokens",           // unrecognised op
		"/v1beta/models/gemini-2.0-flash",             // no colon
		"/v1beta/models/gemini:generateContent:extra", // op carries a colon
		"/V1/CHAT/COMPLETIONS",                        // case sensitivity
		"/v1/chat/completions/",                       // trailing slash
		"//v1/chat/completions",                       // doubled separator
		"/v1beta/models/" + strings.Repeat("m", 4096) + ":generateContent",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, path string) {
		protocol, params, ok := selection.ProtocolForPath(path)

		if !ok {
			// A miss must be fully zero-valued. A caller that forgets to
			// check ok must not find a usable protocol or params map.
			if protocol != "" {
				t.Fatalf("no match but protocol = %q (path %q)", protocol, path)
			}
			if params != nil {
				t.Fatalf("no match but params = %v (path %q)", params, path)
			}
			return
		}

		if _, known := knownProtocols[protocol]; !known {
			t.Fatalf("matched unknown protocol %q for path %q", protocol, path)
		}

		if protocol == selection.ProtocolGenerateContent {
			model, hasModel := params["model"]
			op, hasOp := params["op"]
			if !hasModel || !hasOp {
				t.Fatalf("generate_content missing params: %v (path %q)", params, path)
			}
			if model == "" {
				t.Fatalf("generate_content matched with empty model (path %q)", path)
			}
			if strings.Contains(model, "/") {
				t.Fatalf("model %q spans path segments (path %q)", model, path)
			}
			if op != "generateContent" && op != "streamGenerateContent" {
				t.Fatalf("generate_content matched unrecognised op %q (path %q)", op, path)
			}
		} else if params != nil {
			t.Fatalf("protocol %q returned params %v; only generate_content carries any (path %q)",
				protocol, params, path)
		}

		// Deterministic and stateless: a second call must agree.
		protocol2, params2, ok2 := selection.ProtocolForPath(path)
		if protocol2 != protocol || ok2 != ok || len(params2) != len(params) {
			t.Fatalf("non-deterministic: (%q,%v,%v) then (%q,%v,%v) for path %q",
				protocol, params, ok, protocol2, params2, ok2, path)
		}
	})
}
