// Package translate performs cross-provider protocol translation: it rewrites a
// request from its inbound (source) wire protocol into a target protocol on the
// way upstream and translates the upstream response back on the way out.
//
// Translators are registered per ordered (source, target) protocol pair — a
// direct pairwise matrix, deliberately not a hub model, so each pair maps the
// other's quirks with full knowledge of both ends (see the "Cross-Provider
// Translation" design note). Concrete translators live in sibling files and
// register themselves at init; this file is the interface + registry only.
//
// Translation is never inferred: a request is translated only when an explicit
// `translate` rule action declared the target protocol. A declared mismatch
// with no registered translator is a resolution error (Lookup returns false),
// never a silent passthrough.
package translate

import "fmt"

// Protocol is a wire-protocol identifier. Values match the protocol names in
// contracts/config (e.g. "chat", "messages"), but the type stays a bare string
// alias here so this engine package carries no config dependency.
type Protocol = string

// Translator converts requests and non-streaming responses between one ordered
// pair of protocols. Streaming translation is provided by translators that also
// implement StreamCapable; callers type-assert for it.
//
// Every method must round-trip unknown fields intact (invariant #1):
// implementations decode into the typed protocol models, which preserve unknown
// fields via DynamicProperties / Unknown* fallbacks rather than dropping them.
type Translator interface {
	// Source is the protocol the inbound request is written in.
	Source() Protocol

	// Target is the protocol the request is translated into for the upstream.
	Target() Protocol

	// TranslateRequest rewrites an inbound source-protocol request body into
	// the target protocol.
	TranslateRequest(body []byte) ([]byte, error)

	// TranslateResponse rewrites a non-streaming target-protocol response body
	// back into the source protocol.
	TranslateResponse(body []byte) ([]byte, error)
}

// StreamCapable is the optional extension a Translator implements when it can
// translate a streaming response. Translators without streaming support omit
// it; the caller fails closed when a streaming request resolves to a
// non-StreamCapable translator.
type StreamCapable interface {
	// NewStreamTranslator returns a fresh, stateful translator scoped to one
	// streaming response.
	NewStreamTranslator() StreamTranslator
}

// StreamTranslator translates a streaming response incrementally. It is fed the
// upstream (target-protocol) SSE frames one at a time and emits zero or more
// source-protocol frames per call; Close flushes any trailing state at end of
// stream. A StreamTranslator is single-use and not safe for concurrent use.
type StreamTranslator interface {
	// Translate consumes one upstream SSE frame and returns the
	// source-protocol frame bytes to forward (may be empty).
	Translate(frame []byte) ([]byte, error)

	// Close flushes terminal state at end of stream, returning any final
	// frame bytes to forward (may be empty).
	Close() ([]byte, error)
}

// key is the registry lookup key: an ordered (source, target) protocol pair.
type key struct {
	src    Protocol
	target Protocol
}

// Registry maps an ordered (source, target) protocol pair to its Translator.
// It is populated at init by sibling translator files and treated read-only
// thereafter, so Lookup needs no synchronisation.
type Registry struct {
	m map[key]Translator
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry { return &Registry{m: make(map[key]Translator)} }

// Register adds t under its (Source, Target) pair. It panics on a duplicate
// pair or an identity (Source == Target) registration — both are programmer
// errors meant to surface at init, never at request time.
func (r *Registry) Register(t Translator) {
	if t.Source() == t.Target() {
		panic(fmt.Sprintf("translate: refusing to register identity translator for %q", t.Source()))
	}
	k := key{src: t.Source(), target: t.Target()}
	if _, dup := r.m[k]; dup {
		panic(fmt.Sprintf("translate: duplicate translator for %q->%q", k.src, k.target))
	}
	r.m[k] = t
}

// Lookup returns the translator for src->target, or (nil, false) when none is
// registered. A false result is the destination builder's fail-closed signal:
// a declared translation with no translator is a resolution error, not a
// silent passthrough.
func (r *Registry) Lookup(src, target Protocol) (Translator, bool) {
	t, ok := r.m[key{src: src, target: target}]
	return t, ok
}

// defaultRegistry is the process-wide registry that sibling translator files
// register into via the package-level Register at init.
var defaultRegistry = NewRegistry()

// Register adds t to the default registry. Intended for use from a translator
// file's init().
func Register(t Translator) { defaultRegistry.Register(t) }

// Lookup resolves src->target against the default registry.
func Lookup(src, target Protocol) (Translator, bool) { return defaultRegistry.Lookup(src, target) }
