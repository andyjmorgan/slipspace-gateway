package models

import (
	"encoding/json"
	"reflect"
	"testing"
)

type umLeaf struct {
	Name string `json:"name"`

	DynamicProperties
}

type umChild struct {
	Role string `json:"role"`

	DynamicProperties
}

type umParent struct {
	Model string `json:"model"`

	Child umChild `json:"child"`

	Children []umChild `json:"children"`

	ByName map[string]umChild `json:"by_name"`

	PtrChild *umChild `json:"ptr_child,omitempty"`

	Iface any `json:"iface,omitempty"`

	Raw json.RawMessage `json:"raw,omitempty"`

	Bytes []byte `json:"bytes,omitempty"`

	DynamicProperties
}

func (p *umParent) UnmarshalJSON(b []byte) error { return UnmarshalDynamic(b, p) }
func (p umParent) MarshalJSON() ([]byte, error)  { return MarshalDynamic(p) }
func (c *umChild) UnmarshalJSON(b []byte) error  { return UnmarshalDynamic(b, c) }
func (c umChild) MarshalJSON() ([]byte, error)   { return MarshalDynamic(c) }
func (l *umLeaf) UnmarshalJSON(b []byte) error   { return UnmarshalDynamic(b, l) }
func (l umLeaf) MarshalJSON() ([]byte, error)    { return MarshalDynamic(l) }

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CollectUnmapped = %v, want %v", got, want)
	}
}

func TestCollectUnmapped_NoUnknowns(t *testing.T) {
	var p umParent
	if err := json.Unmarshal([]byte(`{"model":"m","child":{"role":"user"}}`), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := CollectUnmapped(&p); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestCollectUnmapped_TopLevelAndNested(t *testing.T) {
	in := `{
		"model":"m",
		"future_top":1,
		"child":{"role":"user","child_unknown":2},
		"children":[{"role":"a"},{"role":"b","elem_unknown":3}],
		"by_name":{"k":{"role":"x","map_unknown":4}},
		"ptr_child":{"role":"p","ptr_unknown":5}
	}`
	var p umParent
	if err := json.Unmarshal([]byte(in), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Slice and map elements collapse to one path (no index segment).
	eq(t, CollectUnmapped(&p), []string{
		"by_name.map_unknown",
		"child.child_unknown",
		"children.elem_unknown",
		"future_top",
		"ptr_child.ptr_unknown",
	})
}

func TestCollectUnmapped_RawMessageIsOpaque(t *testing.T) {
	// Unknown fields inside a json.RawMessage / []byte leaf are NOT walked.
	in := `{"model":"m","raw":{"deep_unknown":1},"bytes":"aGVsbG8="}`
	var p umParent
	if err := json.Unmarshal([]byte(in), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := CollectUnmapped(&p); got != nil {
		t.Fatalf("raw/bytes should be opaque, got %v", got)
	}
}

func TestCollectUnmapped_InterfaceConcrete(t *testing.T) {
	p := umParent{Iface: &umLeaf{Name: "n", DynamicProperties: DynamicProperties{
		Extra: map[string]json.RawMessage{"iface_unknown": json.RawMessage(`1`)},
	}}}
	eq(t, CollectUnmapped(&p), []string{"iface.iface_unknown"})
}

func TestCollectUnmapped_DedupesAcrossSliceElements(t *testing.T) {
	// The same unknown key on two elements collapses to one entry.
	in := `{"model":"m","children":[{"role":"a","dup":1},{"role":"b","dup":2}]}`
	var p umParent
	if err := json.Unmarshal([]byte(in), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	eq(t, CollectUnmapped(&p), []string{"children.dup"})
}

func TestCollectUnmapped_NonStructInputs(t *testing.T) {
	if got := CollectUnmapped(nil); got != nil {
		t.Fatalf("nil: got %v", got)
	}
	if got := CollectUnmapped(42); got != nil {
		t.Fatalf("int: got %v", got)
	}
	var nilPtr *umParent
	if got := CollectUnmapped(nilPtr); got != nil {
		t.Fatalf("nil ptr: got %v", got)
	}
	if got := CollectUnmapped("string"); got != nil {
		t.Fatalf("string: got %v", got)
	}
}

func TestCollectUnmapped_ValueNotPointer(t *testing.T) {
	in := `{"model":"m","top_unknown":1}`
	var p umParent
	if err := json.Unmarshal([]byte(in), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Passing the struct by value (not pointer) still walks it.
	eq(t, CollectUnmapped(p), []string{"top_unknown"})
}
