package selection

import (
	"strings"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
)

// Protocol name constants, re-exported from contracts/config so callers of the
// selection package have one import for the protocol vocabulary.
const (
	ProtocolChat            = contractsconfig.ProtocolChat
	ProtocolResponses       = contractsconfig.ProtocolResponses
	ProtocolMessages        = contractsconfig.ProtocolMessages
	ProtocolGenerateContent = contractsconfig.ProtocolGenerateContent
	ProtocolEmbeddings      = contractsconfig.ProtocolEmbeddings
)

// ProtocolForPath maps an inbound request path to its generative protocol.
// The path is the protocol in the v2 model: there is one canonical base path
// per wire shape (plus a no-version-segment alias for the OpenAI-style ones).
//
// For Gemini's generate_content the model and operation ride in the path
// (/v1beta/models/{model}:generateContent), so params carries "model" and "op"
// — the only protocol whose model is path-derived rather than body-derived.
//
// ok is false when the path is not a recognised generative endpoint; the
// caller then tries per-configuration passthrough matching (MatchPassthrough).
func ProtocolForPath(path string) (protocol string, params map[string]string, ok bool) {
	switch path {
	case "/v1/chat/completions", "/chat/completions":
		return ProtocolChat, nil, true
	case "/v1/responses", "/responses":
		return ProtocolResponses, nil, true
	case "/v1/messages", "/messages":
		return ProtocolMessages, nil, true
	case "/v1/embeddings", "/embeddings":
		return ProtocolEmbeddings, nil, true
	}

	// Gemini generate_content: /v1beta/models/{model}:{op}
	if model, op, isGen := parseGenerateContent(path); isGen {
		return ProtocolGenerateContent, map[string]string{"model": model, "op": op}, true
	}
	return "", nil, false
}

// generateContentPrefix is the fixed prefix of the Gemini generate_content
// path; the segment after it is "{model}:{op}".
const generateContentPrefix = "/v1beta/models/"

// generateContentOps is the set of operations the generate_content protocol
// accepts after the model segment.
var generateContentOps = map[string]struct{}{
	"generateContent":       {},
	"streamGenerateContent": {},
}

// parseGenerateContent extracts the model and operation from a Gemini
// generate_content path. isGen is false for any path that is not exactly
// "/v1beta/models/{model}:{op}" with a recognised op and a non-empty,
// single-segment model.
func parseGenerateContent(path string) (model, op string, isGen bool) {
	if !strings.HasPrefix(path, generateContentPrefix) {
		return "", "", false
	}
	rest := path[len(generateContentPrefix):]
	colon := strings.IndexByte(rest, ':')
	if colon <= 0 {
		return "", "", false
	}
	model = rest[:colon]
	op = rest[colon+1:]
	if strings.ContainsRune(model, '/') {
		return "", "", false // model must be a single path segment
	}
	if _, okOp := generateContentOps[op]; !okOp {
		return "", "", false
	}
	return model, op, true
}
