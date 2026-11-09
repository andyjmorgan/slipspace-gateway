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

// Path-param keys ProtocolForPath populates for generate_content. The same
// names are the placeholders a provider's protocol path template may carry
// ("/{version}/models/{model}:{op}") — substitutePlaceholders in cmd/gateway
// fills them by key, so a rename here is a rename on the wire template.
const (
	// ParamModel is the model segment of a Gemini generate_content path.
	ParamModel = "model"
	// ParamOp is the operation suffix (generateContent / streamGenerateContent).
	ParamOp = "op"
	// ParamVersion is the Gemini API version segment the client addressed
	// ("v1" or "v1beta").
	ParamVersion = "version"
)

// ProtocolForPath maps an inbound request path to its generative protocol.
// The path is the protocol in the v2 model: there is one canonical base path
// per wire shape (plus a no-version-segment alias for the OpenAI-style ones).
//
// For Gemini's generate_content the model and operation ride in the path
// (/{version}/models/{model}:{op}), so params carries "model", "op" and
// "version" — the only protocol whose model is path-derived rather than
// body-derived. version is the API version segment the client addressed;
// the provider's path template re-inserts it through the {version}
// placeholder so the client's choice reaches the upstream intact.
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

	// Gemini generate_content: /{version}/models/{model}:{op}
	if version, model, op, isGen := parseGenerateContent(path); isGen {
		return ProtocolGenerateContent, map[string]string{
			ParamModel:   model,
			ParamOp:      op,
			ParamVersion: version,
		}, true
	}
	return "", nil, false
}

// generateContentVersions is the set of Gemini API versions whose
// generate_content path shape the gateway recognises. Google documents both
// `v1` (stable) and `v1beta` (preview features) with the identical
// `/{version}/models/{model}:{op}` shape — see
// https://ai.google.dev/gemini-api/docs/api-versions and the REST reference
// at https://ai.google.dev/api/generate-content. google-genai 3.0.0 switched
// the SDK default from v1beta to v1 (CHANGELOG, 2026-11-06:
// https://github.com/googleapis/python-genai/blob/main/CHANGELOG.md), so a
// vanilla client now addresses /v1/models/... (issue #631).
var generateContentVersions = map[string]struct{}{
	"v1":     {},
	"v1beta": {},
}

// generateContentModelsSegment is the fixed segment between the version and
// "{model}:{op}" in a Gemini generate_content path.
const generateContentModelsSegment = "/models/"

// generateContentOps is the set of operations the generate_content protocol
// accepts after the model segment.
var generateContentOps = map[string]struct{}{
	"generateContent":       {},
	"streamGenerateContent": {},
}

// parseGenerateContent extracts the API version, model and operation from a
// Gemini generate_content path. isGen is false for any path that is not
// exactly "/{version}/models/{model}:{op}" with a recognised version, a
// recognised op and a non-empty, single-segment model.
func parseGenerateContent(path string) (version, model, op string, isGen bool) {
	if path == "" || path[0] != '/' {
		return "", "", "", false
	}
	rest := path[1:]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return "", "", "", false
	}
	version = rest[:slash]
	if _, okVer := generateContentVersions[version]; !okVer {
		return "", "", "", false
	}
	rest = rest[slash:]
	if !strings.HasPrefix(rest, generateContentModelsSegment) {
		return "", "", "", false
	}
	rest = rest[len(generateContentModelsSegment):]
	colon := strings.IndexByte(rest, ':')
	if colon <= 0 {
		return "", "", "", false
	}
	model = rest[:colon]
	op = rest[colon+1:]
	if strings.ContainsRune(model, '/') {
		return "", "", "", false // model must be a single path segment
	}
	if _, okOp := generateContentOps[op]; !okOp {
		return "", "", "", false
	}
	return version, model, op, true
}
