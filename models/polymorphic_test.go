package models

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

type animal interface {
	Sound() string
}

type dog struct {
	Kind string `json:"kind"`

	Bark string `json:"bark"`

	DynamicProperties
}

func (d *dog) Sound() string { return d.Bark }
func (d *dog) UnmarshalJSON(data []byte) error {
	return UnmarshalDynamic(data, d)
}
func (d *dog) MarshalJSON() ([]byte, error) { return MarshalDynamic(d) }

type cat struct {
	Kind string `json:"kind"`

	Meow string `json:"meow"`

	DynamicProperties
}

func (c *cat) Sound() string { return c.Meow }
func (c *cat) UnmarshalJSON(data []byte) error {
	return UnmarshalDynamic(data, c)
}
func (c *cat) MarshalJSON() ([]byte, error) { return MarshalDynamic(c) }

type unknownAnimal struct {
	Kind string `json:"kind"`

	DynamicProperties
}

func (u *unknownAnimal) Sound() string { return "?" }
func (u *unknownAnimal) UnmarshalJSON(data []byte) error {
	return UnmarshalDynamic(data, u)
}
func (u *unknownAnimal) MarshalJSON() ([]byte, error) { return MarshalDynamic(u) }

func animalRegistry(withFallback bool) PolymorphicRegistry[animal] {
	r := PolymorphicRegistry[animal]{
		DiscriminatorField: "kind",
		Factories: map[string]func() animal{
			"dog": func() animal { return &dog{} },
			"cat": func() animal { return &cat{} },
		},
	}
	if withFallback {
		r.Fallback = func(disc string) animal { return &unknownAnimal{Kind: disc} }
	}
	return r
}

func TestPolymorphicRegistry_UnmarshalOne_KnownDiscriminator(t *testing.T) {
	r := animalRegistry(true)
	v, err := r.UnmarshalOne([]byte(`{"kind":"dog","bark":"woof"}`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	d, ok := v.(*dog)
	if !ok {
		t.Fatalf("expected *dog, got %T", v)
	}
	if d.Bark != "woof" {
		t.Fatalf("bark = %q", d.Bark)
	}
}

func TestPolymorphicRegistry_UnmarshalOne_FallbackPreservesDiscriminator(t *testing.T) {
	r := animalRegistry(true)
	v, err := r.UnmarshalOne([]byte(`{"kind":"dragon","fire":true,"scales":99}`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	u, ok := v.(*unknownAnimal)
	if !ok {
		t.Fatalf("expected *unknownAnimal, got %T", v)
	}
	if u.Kind != "dragon" {
		t.Fatalf("kind = %q", u.Kind)
	}
	if string(u.Extra["fire"]) != `true` || string(u.Extra["scales"]) != `99` {
		t.Fatalf("extras not preserved: %v", u.Extra)
	}
}

func TestPolymorphicRegistry_UnmarshalOne_MissingDiscriminator(t *testing.T) {
	r := animalRegistry(true)
	_, err := r.UnmarshalOne([]byte(`{"bark":"woof"}`))
	if !errors.Is(err, ErrMissingDiscriminator) {
		t.Fatalf("want ErrMissingDiscriminator, got %v", err)
	}
}

func TestPolymorphicRegistry_UnmarshalOne_NonObject(t *testing.T) {
	r := animalRegistry(true)
	cases := []string{`"text"`, `42`, `[1,2]`, `null`, `{not json`}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := r.UnmarshalOne([]byte(in))
			if !errors.Is(err, ErrNotObject) {
				t.Fatalf("want ErrNotObject, got %v", err)
			}
		})
	}
}

func TestPolymorphicRegistry_UnmarshalOne_DiscriminatorNotString(t *testing.T) {
	r := animalRegistry(true)
	_, err := r.UnmarshalOne([]byte(`{"kind":42}`))
	if !errors.Is(err, ErrDiscriminatorNotString) {
		t.Fatalf("want ErrDiscriminatorNotString, got %v", err)
	}
}

func TestPolymorphicRegistry_UnmarshalOne_UnknownDiscriminator_NoFallback(t *testing.T) {
	r := animalRegistry(false)
	_, err := r.UnmarshalOne([]byte(`{"kind":"dragon"}`))
	if !errors.Is(err, ErrUnknownDiscriminator) {
		t.Fatalf("want ErrUnknownDiscriminator, got %v", err)
	}
}

func TestPolymorphicRegistry_UnmarshalOne_FactoryUnmarshalError(t *testing.T) {
	r := animalRegistry(true)
	_, err := r.UnmarshalOne([]byte(`{"kind":"dog","bark":123}`))
	if err == nil || errors.Is(err, ErrUnknownDiscriminator) {
		t.Fatalf("want json type error, got %v", err)
	}
}

func TestPolymorphicRegistry_UnmarshalOne_FallbackUnmarshalError(t *testing.T) {
	r := PolymorphicRegistry[animal]{
		DiscriminatorField: "kind",
		Factories:          map[string]func() animal{},
		Fallback: func(disc string) animal {
			return &explodingAnimal{}
		},
	}
	_, err := r.UnmarshalOne([]byte(`{"kind":"dragon"}`))
	if err == nil {
		t.Fatalf("expected error from fallback unmarshal")
	}
}

type explodingAnimal struct{}

func (e *explodingAnimal) Sound() string                { return "" }
func (e *explodingAnimal) UnmarshalJSON(_ []byte) error { return errors.New("boom") }
func (e *explodingAnimal) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }

func TestPolymorphicRegistry_UnmarshalSlice_Mixed(t *testing.T) {
	r := animalRegistry(true)
	in := []byte(`[{"kind":"dog","bark":"woof"},{"kind":"cat","meow":"mrow"},{"kind":"dragon","fire":true}]`)
	out, err := r.UnmarshalSlice(in)
	if err != nil {
		t.Fatalf("unmarshal slice: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("len = %d", len(out))
	}
	if _, ok := out[0].(*dog); !ok {
		t.Fatalf("elem 0 = %T", out[0])
	}
	if _, ok := out[1].(*cat); !ok {
		t.Fatalf("elem 1 = %T", out[1])
	}
	u, ok := out[2].(*unknownAnimal)
	if !ok {
		t.Fatalf("elem 2 = %T", out[2])
	}
	if u.Kind != "dragon" || string(u.Extra["fire"]) != `true` {
		t.Fatalf("unknown not preserved: %+v", u)
	}
}

func TestPolymorphicRegistry_UnmarshalSlice_NonArray(t *testing.T) {
	r := animalRegistry(true)
	_, err := r.UnmarshalSlice([]byte(`{"not":"an array"}`))
	if err == nil {
		t.Fatalf("expected error on non-array input")
	}
}

func TestPolymorphicRegistry_UnmarshalSlice_PropagatesElementError(t *testing.T) {
	r := animalRegistry(false)
	_, err := r.UnmarshalSlice([]byte(`[{"kind":"dog","bark":"ok"},{"kind":"dragon"}]`))
	if !errors.Is(err, ErrUnknownDiscriminator) {
		t.Fatalf("want ErrUnknownDiscriminator, got %v", err)
	}
}

func TestPolymorphicRegistry_UnmarshalSlice_Empty(t *testing.T) {
	r := animalRegistry(true)
	out, err := r.UnmarshalSlice([]byte(`[]`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("len = %d", len(out))
	}
}

func TestPolymorphicRegistry_RoundTrip_UnknownPreservedByteEquivalent(t *testing.T) {
	r := animalRegistry(true)
	in := []byte(`{"kind":"dragon","fire":true,"scales":99}`)
	v, err := r.UnmarshalOne(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var av, bv any
	if err := json.Unmarshal(in, &av); err != nil {
		t.Fatalf("in parse: %v", err)
	}
	if err := json.Unmarshal(out, &bv); err != nil {
		t.Fatalf("out parse: %v", err)
	}
	if !reflect.DeepEqual(av, bv) {
		t.Fatalf("round-trip drift:\n in: %s\nout: %s", in, out)
	}
}
