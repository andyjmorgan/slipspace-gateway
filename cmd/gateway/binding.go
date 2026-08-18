package main

import (
	"hash/fnv"
	"math/rand/v2"
	"strings"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
)

// sampleRandFloat64 is the package-level RNG seam the random-sampling
// path uses. Tests substitute for determinism. rand/v2 is intentional —
// sampling doesn't need a cryptographic source.
var sampleRandFloat64 = rand.Float64 //nolint:gosec // G404: non-crypto by design

// evaluateBinding applies one binding's sampling, filter, and
// size-cap knobs to rec. Returns (modifiedRec, true) to ship,
// (zero, false) to skip. The contract:
//
//   - Sampling out → skip (no record reaches the connector).
//   - Filter mismatch → skip.
//   - Oversize + OversizeDropRecord → skip.
//   - Oversize + OversizeMetadataOnly (default) → ship with bodies
//     stripped and Request.BodyOmitted / Response.BodyOmitted set.
//   - Anything else → ship as-is.
//
// The returned Record is a value-copy so the caller's original is
// unmodified across bindings; multiple bindings on one configuration
// can each apply different size caps without polluting each other.
//
// The third return value reports oversize handling so the caller can
// log it — body capping is silent on the wire, and an operator chasing
// a destination missing its bodies needs the breadcrumb.
func evaluateBinding(rec cc.Record, b contractsconfig.ConnectorBinding, connectorType string) (cc.Record, bool, oversizeOutcome) {
	if !samplingIncludes(rec, b) {
		return cc.Record{}, false, oversizeOutcome{}
	}
	if !matchesFilter(rec, b.Filter) {
		return cc.Record{}, false, oversizeOutcome{}
	}
	return applyOversize(rec, b, connectorType)
}

// oversizeOutcome describes how a binding's body cap acted on a record.
// The zero value (Triggered=false) means the body was within cap or no
// cap applied — nothing to log.
type oversizeOutcome struct {
	// Triggered is true when the record's largest body exceeded the
	// effective cap.
	Triggered bool

	// Dropped is true when Triggered and the behaviour was drop_record
	// (the whole record was discarded, not just its bodies).
	Dropped bool

	// CapBytes is the effective cap that was exceeded.
	CapBytes int

	// BodyBytes is the larger of the request/response body length that
	// tripped the cap.
	BodyBytes int
}

// samplingIncludes decides whether rec is in the sampled set for b.
// Defaults: Sampling 0 → 1.0 (everything in). SamplingKey "" →
// correlation_id (deterministic) so retries / tool-call follow-ups
// stay grouped.
func samplingIncludes(rec cc.Record, b contractsconfig.ConnectorBinding) bool {
	s := b.Sampling
	if s <= 0 {
		s = 1.0
	}
	if s >= 1.0 {
		return true
	}
	var bucket float64
	switch b.SamplingKey {
	case contractsconfig.SamplingKeyRandom:
		bucket = sampleRandFloat64()
	default:
		// correlation_id (default). FNV-1a 64-bit hash → uniform
		// fraction in [0, 1). Same correlation_id always lands on
		// the same side of the threshold.
		h := fnv.New64a()
		_, _ = h.Write([]byte(rec.CorrelationID))
		bucket = float64(h.Sum64()&0xFFFFFFFFFFFF) / float64(0xFFFFFFFFFFFF+1)
	}
	return bucket < s
}

// matchesFilter applies the filter predicates with the design-note
// semantics: empty list = no constraint; multiple values within a
// list are OR; different fields are AND.
func matchesFilter(rec cc.Record, f *contractsconfig.ConnectorFilter) bool {
	if f == nil {
		return true
	}
	if !inStringList(rec.Provider, f.Providers) {
		return false
	}
	if !inStringList(rec.Protocol, f.Protocols) {
		return false
	}
	if !matchesModelPatterns(rec.Model, f.Models) {
		return false
	}
	statusMin := f.StatusMin
	if statusMin == 0 {
		statusMin = 200
	}
	statusMax := f.StatusMax
	if statusMax == 0 {
		statusMax = 599
	}
	// Prefer the upstream status when we have it; for short-circuited
	// requests it equals Response.Status. Records where neither was
	// populated (e.g. a transport-error path that never saw headers)
	// land at zero, which the [statusMin, statusMax] range excludes
	// unless the operator explicitly broadens the lower bound.
	status := rec.UpstreamStatus
	if status == 0 {
		status = rec.Response.Status
	}
	if status < statusMin || status > statusMax {
		return false
	}
	if !matchesTagsAny(rec.Tags, f.TagsAny) {
		return false
	}
	if !matchesTagsAll(rec.Tags, f.TagsAll) {
		return false
	}
	return true
}

// inStringList reports whether value is in list, or true when list
// is empty (no constraint).
func inStringList(value string, list []string) bool {
	if len(list) == 0 {
		return true
	}
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

// matchesModelPatterns supports the design-note's trailing-* wildcard
// (e.g. "claude-*"). Exact-equal for non-wildcard entries; mixing
// wildcards in the middle of a pattern is rejected by config
// validation before we get here.
func matchesModelPatterns(model string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if strings.HasSuffix(p, "*") {
			if strings.HasPrefix(model, p[:len(p)-1]) {
				return true
			}
			continue
		}
		if p == model {
			return true
		}
	}
	return false
}

// matchesTagsAny is true when at least one of want is in recTags,
// or when want is empty.
func matchesTagsAny(recTags, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		for _, t := range recTags {
			if t == w {
				return true
			}
		}
	}
	return false
}

// matchesTagsAll is true when every entry in want appears in
// recTags, or when want is empty.
func matchesTagsAll(recTags, want []string) bool {
	if len(want) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(recTags))
	for _, t := range recTags {
		set[t] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}

// applyOversize compares the record's request + response body lengths
// against the binding's effective body cap. Within cap → ship as-is.
// Oversize → either drop (drop_record) or strip bodies and mark
// omitted (metadata_only, default).
//
// The effective cap is the binding's MaxBodyBytes when set, or
// DefaultMaxBodyBytes for the connector type when unset — which is 0
// for every type since the 1 MiB webhook default was removed. A
// resolved cap of <= 0 means no cap; a positive one caps the larger of
// the captured request and response bodies.
func applyOversize(rec cc.Record, b contractsconfig.ConnectorBinding, connectorType string) (cc.Record, bool, oversizeOutcome) {
	maxBytes := contractsconfig.DefaultMaxBodyBytes(connectorType)
	if b.MaxBodyBytes != nil {
		maxBytes = *b.MaxBodyBytes
	}
	if maxBytes <= 0 {
		return rec, true, oversizeOutcome{}
	}
	maxLen := rec.Request.BodyBytes
	if rec.Response.BodyBytes > maxLen {
		maxLen = rec.Response.BodyBytes
	}
	if maxLen <= maxBytes {
		return rec, true, oversizeOutcome{}
	}
	behaviour := b.OversizeBehaviour
	if behaviour == "" {
		behaviour = contractsconfig.OversizeMetadataOnly
	}
	switch behaviour {
	case contractsconfig.OversizeDropRecord:
		return cc.Record{}, false, oversizeOutcome{Triggered: true, Dropped: true, CapBytes: maxBytes, BodyBytes: maxLen}
	case contractsconfig.OversizeMetadataOnly:
		out := rec
		out.Request.Body = nil
		out.Request.BodyOmitted = true
		out.Response.Body = nil
		out.Response.BodyOmitted = true
		// The assembled rollup is part of the response body family — drop it
		// alongside the raw body so a metadata-only record never ships a large
		// reconstruction the operator asked to cap.
		out.Response.Assembled = nil
		out.Response.AssemblyPartial = false
		return out, true, oversizeOutcome{Triggered: true, CapBytes: maxBytes, BodyBytes: maxLen}
	default:
		// Unknown behaviour — config validation should have rejected
		// it at load time; surface as ship-as-is so the operator
		// gets the record rather than silent drop.
		return rec, true, oversizeOutcome{}
	}
}
