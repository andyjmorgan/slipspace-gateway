// Package bodypatch applies operator-authored body-field mutations to a
// serialized JSON request body using gjson (reads) and sjson (writes).
//
// The engine operates on bytes, after the typed body has been
// marshalled, so unknown provider fields and numeric precision survive
// byte-for-byte — sjson splices the one value in and leaves the rest of
// the document untouched. sjson auto-creates missing intermediate
// objects, which satisfies the "ensure-path" requirement natively; the
// one behaviour the engine adds on top is the scalar-collision guard
// (sjson would otherwise overwrite a scalar mid-path) and the
// append-to-non-array guard.
//
// The same engine serves both phases: request-body patches (applied on
// the request path) and response-body patches (applied in the proxy's
// ModifyResponse hook). Refs.Phase selects which document a template
// body reference resolves against.
package bodypatch

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

// OpKind discriminates the mutation a single Op performs.
type OpKind int

// Op kinds.
const (
	// OpSet sets the target path to Value, creating intermediates.
	OpSet OpKind = iota

	// OpRemove deletes the target path.
	OpRemove

	// OpAppend pushes Value onto the target array.
	OpAppend
)

// Op is one resolved mutation. Path is the gjson/sjson dotted path
// (the Target.Path), already stripped of its scope prefix.
type Op struct {
	// Kind selects the mutation.
	Kind OpKind

	// Path is the sjson/gjson path under request.body.
	Path string

	// Value is the value for OpSet / OpAppend; ignored for OpRemove.
	Value contractsrules.RewriteValue

	// ActionType is the originating action discriminator, carried
	// through to Result for telemetry.
	ActionType string
}

// Phase is the pipeline phase a batch of ops applies in. It decides
// which document a body reference resolves against — see Refs.
type Phase int

// Apply phases.
const (
	// PhaseRequest applies ops to the request body; {request.body.x}
	// reads the evolving working body and {response.body.x} is
	// unavailable.
	PhaseRequest Phase = iota

	// PhaseResponse applies ops to the response body; {response.body.x}
	// reads the evolving working body and {request.body.x} reads the
	// (immutable) RequestBody snapshot.
	PhaseResponse
)

// Refs is the resolution context for value templates. The working
// document being patched is passed to Apply; Refs carries the
// surrounding context a template may reference.
type Refs struct {
	// Phase selects how body references resolve (see Phase).
	Phase Phase

	// PathParams resolves {path_params.X} references.
	PathParams map[string]string

	// Provider resolves {state.provider}.
	Provider string

	// Protocol resolves {state.protocol}.
	Protocol string

	// ExternalURL resolves {external_url} — the gateway's externally
	// reachable base URL. Empty means unset; the ref then misses.
	ExternalURL string

	// RequestBody is the request body snapshot used to resolve
	// {request.body.x} in PhaseResponse. Ignored in PhaseRequest
	// (where {request.body.x} reads the working body instead).
	RequestBody []byte
}

// Drop reasons reported on Result.Reason.
const (
	ReasonPathTraversesPrimitive = "path_traverses_primitive"
	ReasonAppendNonArray         = "append_non_array"
	ReasonTemplateRefMiss        = "template_ref_miss"
	// ReasonStreamingResponse is reported by the response-side applier
	// when queued response rewrites are dropped because the upstream
	// response is server-sent events (not a single JSON document).
	ReasonStreamingResponse = "streaming_response"
	ReasonApplyError        = "apply_error"
)

// Result reports the outcome of one Op for telemetry and the rule-
// matched event payload.
type Result struct {
	// ActionType is the originating action discriminator.
	ActionType string

	// Path is the target path the Op addressed.
	Path string

	// Applied is true when the mutation changed the body.
	Applied bool

	// Reason is the drop reason when Applied is false; empty otherwise.
	Reason string
}

// Apply runs ops against body in order and returns the rewritten bytes
// plus a per-op Result slice. Template references resolve against the
// evolving working body, so a later op observes earlier mutations.
//
// Apply cannot fail fatally: sjson is byte-surgical and every failure
// mode — missing template refs, scalar collisions, appending to a
// non-array, an sjson splice error — is a per-op drop recorded on
// Result, never an aborted batch. A dropped op leaves the working body
// untouched and the next op proceeds.
func Apply(body []byte, ops []Op, refs Refs) ([]byte, []Result) {
	if len(ops) == 0 {
		return body, nil
	}
	working := string(body)
	results := make([]Result, 0, len(ops))

	for _, op := range ops {
		res := Result{ActionType: op.ActionType, Path: op.Path}
		switch op.Kind {
		case OpRemove:
			next, err := sjson.Delete(working, op.Path)
			if err != nil {
				res.Reason = ReasonApplyError
				results = append(results, res)
				continue
			}
			working = next
			res.Applied = true

		case OpSet:
			if traversesPrimitive(working, op.Path) {
				res.Reason = ReasonPathTraversesPrimitive
				results = append(results, res)
				continue
			}
			raw, drop := resolveValue(working, op.Value, refs)
			if drop {
				res.Reason = ReasonTemplateRefMiss
				results = append(results, res)
				continue
			}
			next, err := sjson.SetRaw(working, op.Path, raw)
			if err != nil {
				res.Reason = ReasonApplyError
				results = append(results, res)
				continue
			}
			working = next
			res.Applied = true

		case OpAppend:
			if existing := gjson.Get(working, op.Path); existing.Exists() && !existing.IsArray() {
				res.Reason = ReasonAppendNonArray
				results = append(results, res)
				continue
			}
			raw, drop := resolveValue(working, op.Value, refs)
			if drop {
				res.Reason = ReasonTemplateRefMiss
				results = append(results, res)
				continue
			}
			next, err := sjson.SetRaw(working, op.Path+".-1", raw)
			if err != nil {
				res.Reason = ReasonApplyError
				results = append(results, res)
				continue
			}
			working = next
			res.Applied = true
		}
		results = append(results, res)
	}

	return []byte(working), results
}

// traversesPrimitive reports whether any strict prefix of path resolves
// to a non-container scalar. sjson would otherwise silently overwrite
// that scalar with an object; the rewrite contract is to no-op + warn
// instead (e.g. setting request.body.model.foo when model is a string).
func traversesPrimitive(body, path string) bool {
	segs := strings.Split(path, ".")
	if len(segs) < 2 {
		return false
	}
	prefix := ""
	for _, seg := range segs[:len(segs)-1] {
		if prefix == "" {
			prefix = seg
		} else {
			prefix = prefix + "." + seg
		}
		r := gjson.Get(body, prefix)
		if r.Exists() && !r.IsArray() && !r.IsObject() {
			return true
		}
	}
	return false
}

// resolveValue produces the raw JSON bytes to emit for v, or signals a
// drop. Literal and structured values emit verbatim. Templates follow
// the typing rules: a string that is exactly one {ref} passes the
// referenced value's JSON type through; any other template stringifies
// the substituted result. A single-ref template whose reference cannot
// be resolved drops the op.
func resolveValue(working string, v contractsrules.RewriteValue, refs Refs) (raw string, drop bool) {
	switch v.Kind {
	case contractsrules.RewriteValueLiteral, contractsrules.RewriteValueStructured:
		if len(v.Literal) == 0 {
			return "null", false
		}
		return string(v.Literal), false
	case contractsrules.RewriteValueTemplate:
		return resolveTemplate(working, v.Template, refs)
	default:
		return "", true
	}
}

func resolveTemplate(working, tmpl string, refs Refs) (string, bool) {
	toks := tokenize(tmpl)
	if len(toks) == 1 && toks[0].isRef {
		if raw, ok := resolveRefRaw(working, toks[0].text, refs); ok {
			return raw, false
		}
		return "", true
	}
	var sb strings.Builder
	for _, t := range toks {
		if !t.isRef {
			sb.WriteString(t.text)
			continue
		}
		if s, ok := resolveRefString(working, t.text, refs); ok {
			sb.WriteString(s)
		}
		// Missing reference substitutes empty string per the design's
		// failure semantics — the surrounding literal text still emits.
	}
	encoded, err := json.Marshal(sb.String())
	if err != nil {
		return "", true
	}
	return string(encoded), false
}

type token struct {
	isRef bool

	text string
}

// tokenize splits a template into literal and {ref} tokens. Unbalanced
// braces are treated as literal text — the engine never errors on a
// malformed template, it just emits what it can.
func tokenize(s string) []token {
	var toks []token
	i := 0
	for i < len(s) {
		open := strings.IndexByte(s[i:], '{')
		if open < 0 {
			toks = append(toks, token{text: s[i:]})
			break
		}
		open += i
		if open > i {
			toks = append(toks, token{text: s[i:open]})
		}
		closeRel := strings.IndexByte(s[open:], '}')
		if closeRel < 0 {
			toks = append(toks, token{text: s[open:]})
			break
		}
		closeIdx := open + closeRel
		toks = append(toks, token{isRef: true, text: s[open+1 : closeIdx]})
		i = closeIdx + 1
	}
	return toks
}

const (
	refRequestBody  = "request.body."
	refResponseBody = "response.body."
	refPathParams   = "path_params."
	refStateProv    = "state.provider"
	refStateProto   = "state.protocol"
	refExternalURL  = "external_url"
)

// bodyRefField returns the gjson Result for a request.body/response.body
// reference, picking the source document by phase: in PhaseResponse the
// working document is the response and request.body reads the request
// snapshot; in PhaseRequest the working document is the request and
// response.body is unavailable. isBody is false when ref is not a body
// reference at all.
func bodyRefField(working, ref string, refs Refs) (result gjson.Result, isBody bool) {
	switch {
	case strings.HasPrefix(ref, refRequestBody):
		path := strings.TrimPrefix(ref, refRequestBody)
		if refs.Phase == PhaseResponse {
			return gjson.GetBytes(refs.RequestBody, path), true
		}
		return gjson.Get(working, path), true
	case strings.HasPrefix(ref, refResponseBody):
		if refs.Phase != PhaseResponse {
			// response.body is not in scope on the request path.
			return gjson.Result{}, true
		}
		return gjson.Get(working, strings.TrimPrefix(ref, refResponseBody)), true
	default:
		return gjson.Result{}, false
	}
}

// resolveRefRaw resolves a single-reference template to the referenced
// value's raw JSON, preserving its type. Returns ok=false when the
// reference is absent or names a namespace not available in this phase.
func resolveRefRaw(working, ref string, refs Refs) (string, bool) {
	if r, isBody := bodyRefField(working, ref, refs); isBody {
		if r.Exists() {
			return r.Raw, true
		}
		return "", false
	}
	switch {
	case strings.HasPrefix(ref, refPathParams):
		if v := refs.PathParams[strings.TrimPrefix(ref, refPathParams)]; v != "" {
			return marshalString(v), true
		}
		return "", false
	case ref == refStateProv:
		if refs.Provider != "" {
			return marshalString(refs.Provider), true
		}
		return "", false
	case ref == refStateProto:
		if refs.Protocol != "" {
			return marshalString(refs.Protocol), true
		}
		return "", false
	case ref == refExternalURL:
		if refs.ExternalURL != "" {
			return marshalString(refs.ExternalURL), true
		}
		return "", false
	default:
		return "", false
	}
}

// resolveRefString resolves a reference to its string form for mixed-
// content templates.
func resolveRefString(working, ref string, refs Refs) (string, bool) {
	if r, isBody := bodyRefField(working, ref, refs); isBody {
		if r.Exists() {
			return r.String(), true
		}
		return "", false
	}
	switch {
	case strings.HasPrefix(ref, refPathParams):
		if v := refs.PathParams[strings.TrimPrefix(ref, refPathParams)]; v != "" {
			return v, true
		}
		return "", false
	case ref == refStateProv:
		return refs.Provider, refs.Provider != ""
	case ref == refStateProto:
		return refs.Protocol, refs.Protocol != ""
	case ref == refExternalURL:
		return refs.ExternalURL, refs.ExternalURL != ""
	default:
		return "", false
	}
}

func marshalString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
