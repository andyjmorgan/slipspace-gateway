package models

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestListModelsResponse_RoundTrip(t *testing.T) {
	in := []byte(`{
		"data": [
			{"id":"claude-sonnet-4-5","display_name":"Claude Sonnet 4.5","type":"model","created_at":"2026-01-01T00:00:00Z"},
			{"id":"claude-opus-4-7","display_name":"Claude Opus 4.7","type":"model","created_at":"2026-04-01T00:00:00Z","future_field":"keep"}
		],
		"has_more": false,
		"first_id": "claude-sonnet-4-5",
		"last_id": "claude-opus-4-7",
		"unknown_top":"x"
	}`)
	var resp ListModelsResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("data len = %d", len(resp.Data))
	}
	if resp.Data[0].ID != "claude-sonnet-4-5" || resp.Data[0].DisplayName != "Claude Sonnet 4.5" {
		t.Fatalf("data[0] = %+v", resp.Data[0])
	}
	if string(resp.Data[1].Extra["future_field"]) != `"keep"` {
		t.Fatalf("data[1] extras: %v", resp.Data[1].Extra)
	}
	if resp.FirstID == nil || *resp.FirstID != "claude-sonnet-4-5" {
		t.Fatalf("first_id = %v", resp.FirstID)
	}
	if string(resp.Extra["unknown_top"]) != `"x"` {
		t.Fatalf("top-level extras: %v", resp.Extra)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func TestListModelsResponse_Minimal(t *testing.T) {
	in := []byte(`{"data":[],"has_more":false}`)
	var resp ListModelsResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 0 || resp.HasMore {
		t.Fatalf("resp = %+v", resp)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

func TestModel_AllExportedFieldsHaveJSONTag(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(ListModelsResponse{}),
		reflect.TypeOf(Model{}),
	}
	for _, rt := range types {
		t.Run(rt.Name(), func(t *testing.T) {
			for i := 0; i < rt.NumField(); i++ {
				sf := rt.Field(i)
				if sf.Anonymous || !sf.IsExported() {
					continue
				}
				if _, ok := sf.Tag.Lookup("json"); !ok {
					t.Errorf("%s.%s missing json tag", rt.Name(), sf.Name)
				}
			}
		})
	}
}

func jsonValueEqual(t *testing.T, a, b []byte) bool {
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
