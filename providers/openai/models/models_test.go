package models

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestListModelsResponse_BasicRoundTrip(t *testing.T) {
	in := []byte(`{` +
		`"data":[` +
		`{"created":1700000000,"id":"gpt-4o-mini","object":"model","owned_by":"openai"},` +
		`{"created":1700000100,"id":"gpt-4o","object":"model","owned_by":"openai"}` +
		`],` +
		`"object":"list"` +
		`}`)
	var resp ListModelsResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Object != "list" {
		t.Fatalf("object = %q", resp.Object)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("data = %d", len(resp.Data))
	}
	if resp.Data[0].ID != "gpt-4o-mini" {
		t.Fatalf("data[0].id = %q", resp.Data[0].ID)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}
}

func TestListModelsResponse_UnknownFieldsPreserved(t *testing.T) {
	in := []byte(`{` +
		`"data":[{"created":1700000000,"future_field":"keep","id":"gpt-4o-mini","object":"model","owned_by":"openai"}],` +
		`"object":"list",` +
		`"page_token":"abc"` +
		`}`)
	var resp ListModelsResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(resp.Extra["page_token"]) != `"abc"` {
		t.Fatalf("top extras: %v", resp.Extra)
	}
	if string(resp.Data[0].Extra["future_field"]) != `"keep"` {
		t.Fatalf("model extras: %v", resp.Data[0].Extra)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}
}

func TestModels_AllExportedFieldsHaveJSONTag(t *testing.T) {
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

func FuzzListModelsResponse(f *testing.F) {
	seeds := []string{
		`{"object":"list","data":[]}`,
		`{"object":"list","data":[{"id":"m","object":"model","created":1,"owned_by":"x"}]}`,
		`{"object":"list","data":[{"id":"m","object":"model","created":1,"owned_by":"x","future":1}],"extra":"keep"}`,
		`{}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		var resp ListModelsResponse
		if err := json.Unmarshal([]byte(in), &resp); err != nil {
			return
		}
		if _, err := json.Marshal(resp); err != nil {
			t.Fatalf("marshal after parse: %v\nin: %s", err, in)
		}
	})
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
