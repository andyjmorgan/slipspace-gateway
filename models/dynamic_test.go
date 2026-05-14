package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type testRequest struct {
	Model string `json:"model"`

	MaxTokens int `json:"max_tokens"`

	Temperature *float64 `json:"temperature,omitempty"`

	Stop []string `json:"stop,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`

	Skipped string `json:"-"`

	DynamicProperties
}

type testWithUntagged struct {
	Model string `json:"model"`

	Untagged int

	DynamicProperties
}

type testWithLiteralDash struct {
	Model string `json:"model"`

	Literal string `json:"-,"` //nolint:staticcheck // intentional: exercise literal "-" name handling.

	DynamicProperties
}

type testOmitemptyKinds struct {
	B bool `json:"b,omitempty"`

	I int64 `json:"i,omitempty"`

	U uint32 `json:"u,omitempty"`

	F float64 `json:"f,omitempty"`

	S struct{ X int } `json:"s,omitempty"`

	EmptyName string `json:",omitempty"`

	hidden int

	DynamicProperties
}

func (r *testRequest) UnmarshalJSON(data []byte) error { return UnmarshalDynamic(data, r) }
func (r testRequest) MarshalJSON() ([]byte, error)     { return MarshalDynamic(r) }

func TestUnmarshalDynamic_KnownAndUnknownFields(t *testing.T) {
	in := []byte(`{"model":"claude-sonnet-4-5","max_tokens":1024,"future_field":"keep","nested":{"a":1}}`)
	var r testRequest
	if err := UnmarshalDynamic(in, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Model != "claude-sonnet-4-5" {
		t.Fatalf("model = %q", r.Model)
	}
	if r.MaxTokens != 1024 {
		t.Fatalf("max_tokens = %d", r.MaxTokens)
	}
	if got := string(r.Extra["future_field"]); got != `"keep"` {
		t.Fatalf("extra future_field = %s", got)
	}
	if got := string(r.Extra["nested"]); got != `{"a":1}` {
		t.Fatalf("extra nested = %s", got)
	}
	if _, ok := r.Extra["model"]; ok {
		t.Fatalf("typed field leaked into extra")
	}
}

func TestMarshalDynamic_TypedAndExtraMerged(t *testing.T) {
	temp := 0.7
	r := testRequest{
		Model:       "gpt-4o",
		MaxTokens:   256,
		Temperature: &temp,
		DynamicProperties: DynamicProperties{
			Extra: map[string]json.RawMessage{
				"future_field": json.RawMessage(`"hello"`),
			},
		},
	}
	out, err := MarshalDynamic(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := mustParse(t, out)
	want := map[string]any{
		"model":        "gpt-4o",
		"max_tokens":   float64(256),
		"temperature":  0.7,
		"future_field": "hello",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMarshalDynamic_TypedWinsOnCollision(t *testing.T) {
	r := testRequest{
		Model: "typed-wins",
		DynamicProperties: DynamicProperties{
			Extra: map[string]json.RawMessage{
				"model": json.RawMessage(`"extra-loses"`),
			},
		},
	}
	out, err := MarshalDynamic(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed := mustParse(t, out)
	if parsed["model"] != "typed-wins" {
		t.Fatalf("collision: typed should win, got %v", parsed["model"])
	}
}

func TestMarshalDynamic_OmitemptyDropsCollidingExtra(t *testing.T) {
	r := testRequest{
		DynamicProperties: DynamicProperties{
			Extra: map[string]json.RawMessage{
				"temperature": json.RawMessage(`0.5`),
			},
		},
	}
	out, err := MarshalDynamic(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed := mustParse(t, out)
	if _, ok := parsed["temperature"]; ok {
		t.Fatalf("omitempty zero typed field should drop colliding extra key")
	}
}

func TestMarshalDynamic_OmitemptyAcrossKinds(t *testing.T) {
	t.Run("all empty", func(t *testing.T) {
		out, err := MarshalDynamic(testOmitemptyKinds{})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		parsed := mustParse(t, out)
		for _, k := range []string{"b", "i", "u", "f", "EmptyName"} {
			if _, ok := parsed[k]; ok {
				t.Fatalf("expected key %q absent: %v", k, parsed)
			}
		}
		if _, ok := parsed["s"]; !ok {
			t.Fatalf("struct kind is not handled by isEmptyValue and must emit: %v", parsed)
		}
	})
	t.Run("all populated", func(t *testing.T) {
		v := testOmitemptyKinds{B: true, I: -1, U: 2, F: 3.14, EmptyName: "x"}
		v.S.X = 5
		out, err := MarshalDynamic(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		parsed := mustParse(t, out)
		for _, k := range []string{"b", "i", "u", "f", "s", "EmptyName"} {
			if _, ok := parsed[k]; !ok {
				t.Fatalf("expected key %q present: %v", k, parsed)
			}
		}
		if _, ok := parsed["hidden"]; ok {
			t.Fatalf("unexported field leaked: %v", parsed)
		}
	})
}

func TestMarshalDynamic_OmitemptyRespected(t *testing.T) {
	r := testRequest{Model: "m", MaxTokens: 1}
	out, err := MarshalDynamic(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed := mustParse(t, out)
	for _, k := range []string{"temperature", "stop", "metadata"} {
		if _, ok := parsed[k]; ok {
			t.Fatalf("omitempty: key %q should be absent", k)
		}
	}
}

func TestRoundTrip_PreservesUnknownFields(t *testing.T) {
	in := []byte(`{"future_field":"keep","max_tokens":42,"model":"m","nested":{"a":1,"b":[1,2,3]}}`)
	var r testRequest
	if err := UnmarshalDynamic(in, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := MarshalDynamic(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonEqual(t, in, out) {
		t.Fatalf("round-trip differs\n in: %s\nout: %s", in, out)
	}
}

func TestRoundTrip_PointerFields(t *testing.T) {
	in := []byte(`{"max_tokens":0,"model":"m","temperature":0.42}`)
	var r testRequest
	if err := UnmarshalDynamic(in, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Temperature == nil || *r.Temperature != 0.42 {
		t.Fatalf("temperature did not round-trip: %+v", r.Temperature)
	}
	out, err := MarshalDynamic(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonEqual(t, in, out) {
		t.Fatalf("pointer field round-trip differs\n in: %s\nout: %s", in, out)
	}
}

func TestUnmarshalDynamic_EmptyExtraNotAllocated(t *testing.T) {
	var r testRequest
	if err := UnmarshalDynamic([]byte(`{"model":"m","max_tokens":1}`), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Extra != nil {
		t.Fatalf("Extra should remain nil when no unknown fields: %v", r.Extra)
	}
}

func TestUnmarshalDynamic_LiteralDashName(t *testing.T) {
	in := []byte(`{"-":"literal-dash","model":"m"}`)
	var r testWithLiteralDash
	if err := UnmarshalDynamic(in, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Literal != "literal-dash" {
		t.Fatalf("literal dash name not routed: %q", r.Literal)
	}
	out, err := MarshalDynamic(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonEqual(t, in, out) {
		t.Fatalf("literal dash round-trip differs\n in: %s\nout: %s", in, out)
	}
}

func TestUnmarshalDynamic_UntaggedFieldUsesFieldName(t *testing.T) {
	in := []byte(`{"Untagged":7,"model":"m"}`)
	var r testWithUntagged
	if err := UnmarshalDynamic(in, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Untagged != 7 {
		t.Fatalf("untagged field not routed: %d", r.Untagged)
	}
	out, err := MarshalDynamic(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed := mustParse(t, out)
	if parsed["Untagged"] != float64(7) {
		t.Fatalf("untagged round-trip lost field: %v", parsed)
	}
}

func TestUnmarshalDynamic_SkippedFieldStaysOnExtra(t *testing.T) {
	in := []byte(`{"Skipped":"ignored","model":"m"}`)
	var r testRequest
	if err := UnmarshalDynamic(in, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Skipped != "" {
		t.Fatalf("json:\"-\" field should not be populated: %q", r.Skipped)
	}
	if string(r.Extra["Skipped"]) != `"ignored"` {
		t.Fatalf("unknown key with skipped field name should land in Extra: %v", r.Extra)
	}
}

func TestMarshalDynamic_DeterministicKeyOrder(t *testing.T) {
	r := testRequest{
		Model: "m", MaxTokens: 1,
		DynamicProperties: DynamicProperties{Extra: map[string]json.RawMessage{
			"z": json.RawMessage(`1`),
			"a": json.RawMessage(`2`),
			"m": json.RawMessage(`3`),
		}},
	}
	out1, err := MarshalDynamic(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out2, err := MarshalDynamic(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(out1, out2) {
		t.Fatalf("not deterministic:\n%s\n%s", out1, out2)
	}
	if !bytes.Contains(out1, []byte(`"a":2,`)) || strings.Index(string(out1), `"a"`) > strings.Index(string(out1), `"m"`) {
		t.Fatalf("keys not sorted: %s", out1)
	}
}

func TestMarshalDynamic_ErrorPaths(t *testing.T) {
	t.Run("nil pointer", func(t *testing.T) {
		var r *testRequest
		_, err := MarshalDynamic(r)
		if !errors.Is(err, ErrNotPointer) {
			t.Fatalf("want ErrNotPointer, got %v", err)
		}
	})
	t.Run("non-struct", func(t *testing.T) {
		v := 7
		_, err := MarshalDynamic(&v)
		if !errors.Is(err, ErrNotStruct) {
			t.Fatalf("want ErrNotStruct, got %v", err)
		}
	})
	t.Run("unmarshalable field value", func(t *testing.T) {
		type bad struct {
			Ch chan int `json:"ch"`
			DynamicProperties
		}
		_, err := MarshalDynamic(&bad{Ch: make(chan int)})
		if err == nil || !strings.Contains(err.Error(), "marshal field") {
			t.Fatalf("want marshal field error, got %v", err)
		}
	})
}

func TestUnmarshalDynamic_ErrorPaths(t *testing.T) {
	t.Run("non-pointer", func(t *testing.T) {
		var r testRequest
		if err := UnmarshalDynamic([]byte(`{}`), r); !errors.Is(err, ErrNotPointer) {
			t.Fatalf("want ErrNotPointer, got %v", err)
		}
	})
	t.Run("nil pointer", func(t *testing.T) {
		var r *testRequest
		if err := UnmarshalDynamic([]byte(`{}`), r); !errors.Is(err, ErrNotPointer) {
			t.Fatalf("want ErrNotPointer, got %v", err)
		}
	})
	t.Run("pointer to non-struct", func(t *testing.T) {
		v := 0
		if err := UnmarshalDynamic([]byte(`{}`), &v); !errors.Is(err, ErrNotStruct) {
			t.Fatalf("want ErrNotStruct, got %v", err)
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		var r testRequest
		err := UnmarshalDynamic([]byte(`{not json`), &r)
		if err == nil || !strings.Contains(err.Error(), "unmarshal object") {
			t.Fatalf("want unmarshal object error, got %v", err)
		}
	})
	t.Run("typed field wrong type", func(t *testing.T) {
		var r testRequest
		err := UnmarshalDynamic([]byte(`{"max_tokens":"not-a-number"}`), &r)
		if err == nil || !strings.Contains(err.Error(), `unmarshal field "max_tokens"`) {
			t.Fatalf("want unmarshal field error, got %v", err)
		}
	})
	t.Run("missing embedded DynamicProperties", func(t *testing.T) {
		type plain struct {
			Model string `json:"model"`
		}
		var p plain
		err := UnmarshalDynamic([]byte(`{"model":"m","extra":"x"}`), &p)
		if !errors.Is(err, ErrNotStruct) {
			t.Fatalf("want ErrNotStruct, got %v", err)
		}
	})
}

func TestUnmarshalDynamic_ResetsExtraWhenNoUnknowns(t *testing.T) {
	r := testRequest{DynamicProperties: DynamicProperties{Extra: map[string]json.RawMessage{
		"stale": json.RawMessage(`true`),
	}}}
	if err := UnmarshalDynamic([]byte(`{"model":"m"}`), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Extra != nil {
		t.Fatalf("Extra should reset to nil, got %v", r.Extra)
	}
}

func TestAllExportedFieldsHaveJSONTag(t *testing.T) {
	t.Run("testRequest", func(t *testing.T) { assertTagged(t, reflect.TypeOf(testRequest{})) })
	t.Run("testWithLiteralDash", func(t *testing.T) { assertTagged(t, reflect.TypeOf(testWithLiteralDash{})) })
	t.Run("DynamicProperties", func(t *testing.T) { assertTagged(t, reflect.TypeOf(DynamicProperties{})) })
}

func assertTagged(t *testing.T, rt reflect.Type) {
	t.Helper()
	for i := 0; i < rt.NumField(); i++ {
		sf := rt.Field(i)
		if sf.Anonymous {
			continue
		}
		if !sf.IsExported() {
			continue
		}
		if _, ok := sf.Tag.Lookup("json"); !ok {
			t.Errorf("%s.%s missing json tag", rt.Name(), sf.Name)
		}
	}
}

func mustParse(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return out
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("b: %v", err)
	}
	return reflect.DeepEqual(av, bv)
}
