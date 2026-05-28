// Package models holds the public, shared types used by the Sluice gateway's
// provider model packages. DynamicProperties is the load-bearing helper that
// guarantees unknown JSON fields round-trip back to upstream providers intact.
package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// ErrNotPointer is returned when MarshalDynamic or UnmarshalDynamic receives a
// value that is not a pointer to a struct (or, for MarshalDynamic, a struct).
var ErrNotPointer = errors.New("dynamic: v must be a non-nil pointer to a struct")

// ErrNotStruct is returned when the value (after dereferencing pointers) is
// not a struct.
var ErrNotStruct = errors.New("dynamic: v must reference a struct")

// DynamicProperties captures unknown JSON fields so they survive round-tripping.
// Embed it on any struct whose wire format may carry provider-defined fields
// beyond the ones we model.
//
// This type underpins the load-bearing invariant from CLAUDE.md: every
// provider model type must embed DynamicProperties so unknown JSON fields
// round-trip back to upstream providers intact. Dropping a field on the way
// out subtly breaks customer requests with no error to log; the safety net
// here is what prevents that.
type DynamicProperties struct {
	// Extra holds JSON object keys that did not match any typed field on the
	// embedding struct. Keys are preserved verbatim and re-emitted on JSON
	// marshal so the wire payload survives a parse-and-reserialise cycle.
	//
	// The yaml:"-" tag keeps Extra out of YAML output entirely. YAML is the
	// operator-authored format for our own schemas (rules, configurations);
	// it never carries provider-side unknown fields, so there is nothing
	// for YAML to preserve here. Provider model YAML round-trips are not
	// a supported path either — providers are JSON-only.
	Extra map[string]json.RawMessage `json:"-" yaml:"-"`
}

// MarshalDynamic merges v's typed fields and its embedded DynamicProperties.Extra
// into a single JSON object. Typed fields win on collision. Keys are emitted in
// sorted order so output is deterministic.
func MarshalDynamic(v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, ErrNotPointer
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, ErrNotStruct
	}

	out := make(map[string]json.RawMessage)
	if extra := extraField(rv); extra.IsValid() && !extra.IsNil() {
		iter := extra.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = iter.Value().Interface().(json.RawMessage)
		}
	}

	for _, f := range typedFields(rv.Type()) {
		fv := rv.FieldByIndex(f.index)
		if f.omitEmpty && isEmptyValue(fv) {
			delete(out, f.name)
			continue
		}
		raw, err := json.Marshal(fv.Interface())
		if err != nil {
			return nil, fmt.Errorf("dynamic: marshal field %q: %w", f.name, err)
		}
		out[f.name] = raw
	}

	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(out[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// UnmarshalDynamic populates v's typed fields from data and routes every other
// JSON object key into v's embedded DynamicProperties.Extra. v must be a
// non-nil pointer to a struct that embeds DynamicProperties.
func UnmarshalDynamic(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return ErrNotPointer
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return ErrNotStruct
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("dynamic: unmarshal object: %w", err)
	}

	fields := typedFields(rv.Type())
	byName := make(map[string]fieldInfo, len(fields))
	for _, f := range fields {
		byName[f.name] = f
	}

	for name, payload := range raw {
		if f, ok := byName[name]; ok {
			target := rv.FieldByIndex(f.index).Addr().Interface()
			if err := json.Unmarshal(payload, target); err != nil {
				return fmt.Errorf("dynamic: unmarshal field %q: %w", name, err)
			}
			delete(raw, name)
		}
	}

	extra := extraField(rv)
	if !extra.IsValid() {
		return fmt.Errorf("dynamic: %w: missing embedded DynamicProperties", ErrNotStruct)
	}
	if len(raw) == 0 {
		extra.Set(reflect.Zero(extra.Type()))
		return nil
	}
	extra.Set(reflect.ValueOf(raw))
	return nil
}

type fieldInfo struct {
	name string

	index []int

	omitEmpty bool
}

func typedFields(t reflect.Type) []fieldInfo {
	var out []fieldInfo
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.Anonymous && sf.Type == reflect.TypeOf(DynamicProperties{}) {
			continue
		}
		if !sf.IsExported() {
			continue
		}
		tag, ok := sf.Tag.Lookup("json")
		if !ok {
			out = append(out, fieldInfo{name: sf.Name, index: sf.Index})
			continue
		}
		name, opts, hasOpts := strings.Cut(tag, ",")
		if name == "-" && !hasOpts {
			continue
		}
		if name == "" {
			name = sf.Name
		}
		out = append(out, fieldInfo{
			name:      name,
			index:     sf.Index,
			omitEmpty: hasOption(opts, "omitempty"),
		})
	}
	return out
}

func hasOption(opts, want string) bool {
	for opts != "" {
		var o string
		o, opts, _ = strings.Cut(opts, ",")
		if o == want {
			return true
		}
	}
	return false
}

func extraField(rv reflect.Value) reflect.Value {
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.Anonymous && sf.Type == reflect.TypeOf(DynamicProperties{}) {
			return rv.Field(i).FieldByName("Extra")
		}
	}
	return reflect.Value{}
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	}
	return false
}
