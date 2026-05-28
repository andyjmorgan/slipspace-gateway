package models

import (
	"encoding/json"
	"reflect"
	"sort"
)

// maxUnmappedDepth bounds the recursion in CollectUnmapped. Provider models
// are shallow trees with no cycles; the cap is a defensive backstop against a
// pathological or future self-referential type rather than an expected limit.
const maxUnmappedDepth = 64

var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

// CollectUnmapped walks v — a provider model value (or pointer to one) — and
// returns the sorted, de-duplicated dotted paths of every JSON field that
// landed in a DynamicProperties.Extra map anywhere in the tree. Each path is
// the chain of json field names from the root to the embedding struct, joined
// by ".", with the unmapped key appended; slice and map elements do not add an
// index segment, so "content.reasoning_id" collapses every element of a
// content array that carried an unmodelled reasoning_id field to one entry.
//
// The result is the set of fields the provider sent that this build does not
// model — the signal that drives gateway.unmapped_fields.total. An empty slice
// means every field round-tripped through a typed field.
//
// Fields typed as json.RawMessage (or any other []byte) are treated as opaque
// leaves: CollectUnmapped does not parse their bytes, so unmodelled fields
// nested inside a raw-encoded sub-document are not discovered. Anthropic
// request messages keep their content as json.RawMessage, so request-side
// walks surface top-level and message-level unknowns but not content-block
// internals; the response type keeps Content as typed []ContentBlock, so
// response-side walks do descend into blocks.
func CollectUnmapped(v any) []string {
	set := map[string]struct{}{}
	collectUnmapped(reflect.ValueOf(v), "", set, 0)
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func collectUnmapped(rv reflect.Value, path string, set map[string]struct{}, depth int) {
	if depth > maxUnmappedDepth {
		return
	}
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Struct:
		if extra := extraField(rv); extra.IsValid() && !extra.IsNil() {
			iter := extra.MapRange()
			for iter.Next() {
				set[joinPath(path, iter.Key().String())] = struct{}{}
			}
		}
		for _, f := range typedFields(rv.Type()) {
			collectUnmapped(rv.FieldByIndex(f.index), joinPath(path, f.name), set, depth+1)
		}
	case reflect.Slice, reflect.Array:
		if isByteSequence(rv.Type()) {
			return
		}
		for i := 0; i < rv.Len(); i++ {
			collectUnmapped(rv.Index(i), path, set, depth+1)
		}
	case reflect.Map:
		iter := rv.MapRange()
		for iter.Next() {
			collectUnmapped(iter.Value(), path, set, depth+1)
		}
	}
}

// isByteSequence reports whether t is json.RawMessage or any other []byte —
// the leaf types CollectUnmapped must not descend into as if they were a list
// of typed elements.
func isByteSequence(t reflect.Type) bool {
	if t == rawMessageType {
		return true
	}
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8
}

func joinPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}
