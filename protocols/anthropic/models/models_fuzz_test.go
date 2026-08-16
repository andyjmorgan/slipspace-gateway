package models

import (
	"encoding/json"
	"testing"
)

// FuzzUnmarshalListModelsResponse asserts the "if it parses, it
// round-trips" property CLAUDE.md requires of every UnmarshalJSON: any
// input this type accepts must survive a marshal/unmarshal cycle without
// becoming undecodable.
//
// /v1/models is a surface Anthropic changes without notice, and the
// DynamicProperties guarantee (invariant #1) applies to it exactly as it
// does to the messages surface — an unknown field on a Model must land in
// Extra and come back out intact rather than being dropped on the floor.
// The sibling openai/models and gemini/models packages already carry this
// target; anthropic was the asymmetric hole.
func FuzzUnmarshalListModelsResponse(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"data":[]}`,
		`{"data":[{"id":"claude-sonnet-4-5"}]}`,
		`{"data":[{"id":"claude-sonnet-4-5","display_name":"Claude Sonnet 4.5","type":"model","created_at":"2026-01-01T00:00:00Z"}],"has_more":false}`,
		// Pagination cursors present (pointer fields).
		`{"data":[{"id":"a"},{"id":"b"}],"has_more":true,"first_id":"a","last_id":"b"}`,
		// Unknown fields at both levels — the DynamicProperties path.
		`{"data":[{"id":"x","futureField":"keep"}],"unknownTop":{"nested":[1,2,3]}}`,
		// Explicit nulls on the optional pointer cursors.
		`{"data":null,"first_id":null,"last_id":null}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var resp ListModelsResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return
		}
		out, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal: %v\norig: %s", err, data)
		}
		var again ListModelsResponse
		if err := json.Unmarshal(out, &again); err != nil {
			t.Fatalf("re-unmarshal failed: %v\norig: %s\nmarshalled: %s", err, data, out)
		}
	})
}

// FuzzUnmarshalModel exercises the element type directly. A Model reached
// through ListModelsResponse is always inside an array, so fuzzing it on
// its own reaches shapes the wrapper's seeds do not.
func FuzzUnmarshalModel(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"id":"claude-opus-4-1"}`,
		`{"id":"claude-opus-4-1","display_name":"Claude Opus 4.1","type":"model","created_at":"2026-01-01T00:00:00Z"}`,
		`{"id":"x","futureField":{"deeply":{"nested":true}}}`,
		`{"id":"x","display_name":null}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var m Model
		if err := json.Unmarshal(data, &m); err != nil {
			return
		}
		out, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v\norig: %s", err, data)
		}
		var again Model
		if err := json.Unmarshal(out, &again); err != nil {
			t.Fatalf("re-unmarshal failed: %v\norig: %s\nmarshalled: %s", err, data, out)
		}
	})
}
