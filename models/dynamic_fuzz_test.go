package models

import (
	"encoding/json"
	"reflect"
	"testing"
)

type fuzzTarget struct {
	Model string `json:"model"`

	MaxTokens int `json:"max_tokens"`

	Temperature *float64 `json:"temperature,omitempty"`

	Tags []string `json:"tags,omitempty"`

	DynamicProperties
}

func FuzzUnmarshalDynamic(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"model":"m"}`,
		`{"model":"m","max_tokens":1,"future_field":"keep"}`,
		`{"model":"m","temperature":0.5,"tags":["a","b"]}`,
		`{"nested":{"deep":{"deeper":[1,2,3]}},"model":"m"}`,
		`{"unicode":"héllo 世界","model":"m"}`,
		`{"big_number":1e308,"model":"m"}`,
		`{"empty_arr":[],"empty_obj":{},"null_field":null,"model":"m"}`,
		`{"model":"m","max_tokens":0}`,
		`{"model":""}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var first fuzzTarget
		if err := UnmarshalDynamic(data, &first); err != nil {
			return
		}

		out, err := MarshalDynamic(first)
		if err != nil {
			t.Fatalf("marshal after successful unmarshal: %v\ninput: %s", err, data)
		}

		var second fuzzTarget
		if err := UnmarshalDynamic(out, &second); err != nil {
			t.Fatalf("re-unmarshal failed: %v\nintermediate: %s", err, out)
		}

		if !semanticEqual(first, second) {
			a, _ := json.Marshal(first)
			b, _ := json.Marshal(second)
			t.Fatalf("round-trip drift\nfirst:  %s\nsecond: %s", a, b)
		}
	})
}

func semanticEqual(a, b fuzzTarget) bool {
	if a.Model != b.Model || a.MaxTokens != b.MaxTokens {
		return false
	}
	if (a.Temperature == nil) != (b.Temperature == nil) {
		return false
	}
	if a.Temperature != nil && *a.Temperature != *b.Temperature {
		return false
	}
	if len(a.Tags) != len(b.Tags) {
		return false
	}
	for i := range a.Tags {
		if a.Tags[i] != b.Tags[i] {
			return false
		}
	}
	if len(a.Extra) != len(b.Extra) {
		return false
	}
	for k, v := range a.Extra {
		other, ok := b.Extra[k]
		if !ok {
			return false
		}
		if !jsonValueEqual(v, other) {
			return false
		}
	}
	return true
}

func jsonValueEqual(a, b json.RawMessage) bool {
	var av, bv any
	errA := json.Unmarshal(a, &av)
	errB := json.Unmarshal(b, &bv)
	if errA != nil || errB != nil {
		return string(a) == string(b)
	}
	return reflect.DeepEqual(av, bv)
}
