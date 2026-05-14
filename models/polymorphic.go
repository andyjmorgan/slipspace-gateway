package models

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrMissingDiscriminator is returned by PolymorphicRegistry when the input
// JSON object lacks the configured discriminator field.
var ErrMissingDiscriminator = errors.New("polymorphic: missing discriminator field")

// ErrNotObject is returned when the input JSON is not an object (the only shape
// from which a discriminator can be extracted).
var ErrNotObject = errors.New("polymorphic: input is not a JSON object")

// ErrDiscriminatorNotString is returned when the discriminator field is present
// but its JSON value is not a string.
var ErrDiscriminatorNotString = errors.New("polymorphic: discriminator value is not a string")

// ErrUnknownDiscriminator is returned when the discriminator value has no
// registered factory and the registry has no Fallback configured.
var ErrUnknownDiscriminator = errors.New("polymorphic: unknown discriminator and no fallback configured")

// PolymorphicRegistry dispatches JSON object payloads to a concrete factory
// keyed by a discriminator field. When the discriminator value is not known to
// Factories and Fallback is non-nil, Fallback receives the unknown value and
// produces a catch-all instance (typically an UnknownX type) into which the
// payload is unmarshalled so it round-trips intact.
//
// Together with DynamicProperties, the Fallback path is what guarantees the
// load-bearing invariant: a payload carrying a discriminator we haven't
// modelled still survives the gateway without lossy field-stripping.
type PolymorphicRegistry[T any] struct {
	// DiscriminatorField is the JSON object key whose value selects the
	// concrete factory (commonly "type" or "role").
	DiscriminatorField string

	// Factories maps each known discriminator value to a constructor that
	// returns a pointer to a fresh zero-value instance.
	Factories map[string]func() T

	// Fallback, when non-nil, is invoked for any discriminator value that
	// has no entry in Factories. It receives the unknown value and returns
	// a catch-all instance (conventionally an UnknownX type) into which the
	// payload is unmarshalled so the wire shape round-trips intact.
	Fallback func(discriminator string) T
}

// UnmarshalOne reads the discriminator from data, builds the matching concrete
// instance via Factories or Fallback, and unmarshals data into it.
func (r PolymorphicRegistry[T]) UnmarshalOne(data []byte) (T, error) {
	var zero T
	disc, err := r.peekDiscriminator(data)
	if err != nil {
		return zero, err
	}

	if factory, ok := r.Factories[disc]; ok {
		v := factory()
		if err := json.Unmarshal(data, &v); err != nil {
			return zero, fmt.Errorf("polymorphic: unmarshal %q: %w", disc, err)
		}
		return v, nil
	}

	if r.Fallback == nil {
		return zero, fmt.Errorf("polymorphic: discriminator %q: %w", disc, ErrUnknownDiscriminator)
	}

	v := r.Fallback(disc)
	if err := json.Unmarshal(data, &v); err != nil {
		return zero, fmt.Errorf("polymorphic: unmarshal fallback %q: %w", disc, err)
	}
	return v, nil
}

// UnmarshalSlice decodes a JSON array of polymorphic elements, dispatching each
// element through UnmarshalOne.
func (r PolymorphicRegistry[T]) UnmarshalSlice(data []byte) ([]T, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("polymorphic: unmarshal array: %w", err)
	}
	out := make([]T, len(raws))
	for i, raw := range raws {
		v, err := r.UnmarshalOne(raw)
		if err != nil {
			return nil, fmt.Errorf("polymorphic: element %d: %w", i, err)
		}
		out[i] = v
	}
	return out, nil
}

func (r PolymorphicRegistry[T]) peekDiscriminator(data []byte) (string, error) {
	var probe *map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", fmt.Errorf("polymorphic: %w: %v", ErrNotObject, err)
	}
	if probe == nil {
		return "", fmt.Errorf("polymorphic: %w", ErrNotObject)
	}
	raw, ok := (*probe)[r.DiscriminatorField]
	if !ok {
		return "", fmt.Errorf("polymorphic: field %q: %w", r.DiscriminatorField, ErrMissingDiscriminator)
	}
	var disc string
	if err := json.Unmarshal(raw, &disc); err != nil {
		return "", fmt.Errorf("polymorphic: field %q: %w", r.DiscriminatorField, ErrDiscriminatorNotString)
	}
	return disc, nil
}
