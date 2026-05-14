package models

import (
	"testing"
)

func FuzzPolymorphicUnmarshalOne(f *testing.F) {
	seeds := []string{
		`{"kind":"dog","bark":"woof"}`,
		`{"kind":"cat","meow":"mrow"}`,
		`{"kind":"dragon","fire":true,"scales":99}`,
		`{"kind":"dog"}`,
		`{"kind":""}`,
		`{}`,
		`{"bark":"woof"}`,
		`{"kind":42}`,
		`{"kind":null}`,
		`[]`,
		`"text"`,
		`42`,
		`null`,
		`{"kind":"unknown","nested":{"a":[1,2,3]}}`,
		`{"kind":"dog","bark":"woof","extra":{"nested":true}}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	registry := animalRegistry(true)

	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := registry.UnmarshalOne(data)
		if err != nil {
			return
		}
		if v == nil {
			t.Fatalf("nil value with nil error\ninput: %s", data)
		}
	})
}
