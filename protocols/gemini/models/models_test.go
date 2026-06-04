package models

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestListModelsResponse_FullRoundTrip(t *testing.T) {
	in := []byte(`{` +
		`"models":[{` +
		`"baseModelId":"gemini-2.5-pro",` +
		`"description":"Pro reasoning model",` +
		`"displayName":"Gemini 2.5 Pro",` +
		`"inputTokenLimit":2000000,` +
		`"maxTemperature":2,` +
		`"name":"models/gemini-2.5-pro",` +
		`"outputTokenLimit":8192,` +
		`"supportedGenerationMethods":["generateContent","streamGenerateContent","countTokens"],` +
		`"temperature":1,` +
		`"topK":64,` +
		`"topP":0.95,` +
		`"version":"2.5"` +
		`}],` +
		`"nextPageToken":"abc"` +
		`}`)
	var resp ListModelsResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("models len = %d", len(resp.Models))
	}
	m := resp.Models[0]
	if m.Name != "models/gemini-2.5-pro" {
		t.Fatalf("name = %q", m.Name)
	}
	if m.InputTokenLimit == nil || *m.InputTokenLimit != 2000000 {
		t.Fatalf("input token limit = %v", m.InputTokenLimit)
	}
	if m.MaxTemperature == nil || *m.MaxTemperature != 2 {
		t.Fatalf("max temperature = %v", m.MaxTemperature)
	}
	if len(m.SupportedGenerationMethods) != 3 {
		t.Fatalf("supported methods = %v", m.SupportedGenerationMethods)
	}
	if resp.NextPageToken != "abc" {
		t.Fatalf("next page token = %q", resp.NextPageToken)
	}
	roundTripJSON(t, in, resp)
}

func TestListModelsResponse_UnknownFieldRoundTrips(t *testing.T) {
	in := []byte(`{"futureField":42,"models":[{"name":"models/x"}]}`)
	var resp ListModelsResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(resp.Extra["futureField"]) != `42` {
		t.Fatalf("extras: %v", resp.Extra)
	}
	roundTripJSON(t, in, resp)
}

func TestModel_UnknownFieldRoundTrips(t *testing.T) {
	in := []byte(`{"futureField":"keep","name":"models/x"}`)
	var m Model
	if err := json.Unmarshal(in, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(m.Extra["futureField"]) != `"keep"` {
		t.Fatalf("extras: %v", m.Extra)
	}
	roundTripJSON(t, in, m)
}

func TestListModelsResponse_EmptyRoundTrip(t *testing.T) {
	in := []byte(`{}`)
	var resp ListModelsResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	roundTripJSON(t, in, resp)
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

func FuzzUnmarshalListModelsResponse(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"models":[{"name":"models/x"}]}`,
		`{"models":[{"name":"models/x","inputTokenLimit":1024,"topK":40}],"nextPageToken":"t"}`,
		`{"models":[{"name":"models/x","futureField":"keep"}]}`,
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
			t.Fatalf("marshal: %v", err)
		}
		var again ListModelsResponse
		if err := json.Unmarshal(out, &again); err != nil {
			t.Fatalf("re-unmarshal failed: %v\norig: %s\nmarshalled: %s", err, data, out)
		}
	})
}

func roundTripJSON(t *testing.T, in []byte, v any) {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
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
