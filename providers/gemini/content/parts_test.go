package content

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestUnmarshalPart_Text(t *testing.T) {
	in := []byte(`{"text":"hello"}`)
	v, err := UnmarshalPart(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tp, ok := v.(*TextPart)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if tp.Text != "hello" {
		t.Fatalf("text = %q", tp.Text)
	}
	if tp.PartKind() != "text" {
		t.Fatalf("part kind = %q", tp.PartKind())
	}
	roundTripPart(t, in, v)
}

func TestUnmarshalPart_TextWithThought(t *testing.T) {
	in := []byte(`{"text":"reasoning","thought":true,"thoughtSignature":"sig123"}`)
	v, err := UnmarshalPart(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tp, ok := v.(*TextPart)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if tp.Thought == nil || !*tp.Thought {
		t.Fatalf("thought = %v", tp.Thought)
	}
	if tp.ThoughtSignature == nil || *tp.ThoughtSignature != "sig123" {
		t.Fatalf("thought signature = %v", tp.ThoughtSignature)
	}
	roundTripPart(t, in, v)
}

func TestUnmarshalPart_InlineData(t *testing.T) {
	in := []byte(`{"inlineData":{"data":"AAA","mimeType":"image/png"}}`)
	v, err := UnmarshalPart(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ip, ok := v.(*InlineDataPart)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if ip.InlineData.MimeType != "image/png" || ip.InlineData.Data != "AAA" {
		t.Fatalf("inline data = %+v", ip.InlineData)
	}
	roundTripPart(t, in, v)
}

func TestUnmarshalPart_FileData(t *testing.T) {
	in := []byte(`{"fileData":{"fileUri":"gs://bucket/file.png","mimeType":"image/png"}}`)
	v, err := UnmarshalPart(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	fp, ok := v.(*FileDataPart)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if fp.FileData.FileURI != "gs://bucket/file.png" {
		t.Fatalf("file_uri = %q", fp.FileData.FileURI)
	}
	roundTripPart(t, in, v)
}

func TestUnmarshalPart_FunctionCall(t *testing.T) {
	in := []byte(`{"functionCall":{"args":{"q":"weather"},"id":"c1","name":"search"}}`)
	v, err := UnmarshalPart(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	fc, ok := v.(*FunctionCallPart)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if fc.FunctionCall.Name != "search" || fc.FunctionCall.ID != "c1" {
		t.Fatalf("function_call = %+v", fc.FunctionCall)
	}
	roundTripPart(t, in, v)
}

func TestUnmarshalPart_FunctionResponse(t *testing.T) {
	in := []byte(`{"functionResponse":{"id":"c1","name":"search","response":{"result":"ok"}}}`)
	v, err := UnmarshalPart(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	fr, ok := v.(*FunctionResponsePart)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if fr.FunctionResponse.Name != "search" {
		t.Fatalf("function_response = %+v", fr.FunctionResponse)
	}
	roundTripPart(t, in, v)
}

func TestUnmarshalPart_ExecutableCode(t *testing.T) {
	in := []byte(`{"executableCode":{"code":"print(1)","language":"PYTHON"}}`)
	v, err := UnmarshalPart(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ec, ok := v.(*ExecutableCodePart)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if ec.ExecutableCode.Language != "PYTHON" || ec.ExecutableCode.Code != "print(1)" {
		t.Fatalf("executable_code = %+v", ec.ExecutableCode)
	}
	roundTripPart(t, in, v)
}

func TestUnmarshalPart_CodeExecutionResult(t *testing.T) {
	in := []byte(`{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":"1\n"}}`)
	v, err := UnmarshalPart(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cr, ok := v.(*CodeExecutionResultPart)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if cr.CodeExecutionResult.Outcome != "OUTCOME_OK" {
		t.Fatalf("code_execution_result = %+v", cr.CodeExecutionResult)
	}
	roundTripPart(t, in, v)
}

func TestUnmarshalPart_UnknownShapeRoundTrips(t *testing.T) {
	in := []byte(`{"futurePart":{"flavour":"vanilla"},"meta":42}`)
	v, err := UnmarshalPart(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	u, ok := v.(*UnknownPart)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if string(u.Extra["futurePart"]) != `{"flavour":"vanilla"}` {
		t.Fatalf("futurePart not preserved: %v", u.Extra)
	}
	if string(u.Extra["meta"]) != `42` {
		t.Fatalf("meta not preserved: %v", u.Extra)
	}
	if u.PartKind() != "unknown" {
		t.Fatalf("part kind = %q", u.PartKind())
	}
	roundTripPart(t, in, v)
}

func TestUnmarshalPart_TextWithExtraFieldsRoundTrip(t *testing.T) {
	in := []byte(`{"newField":"keep","text":"hi"}`)
	v, err := UnmarshalPart(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tp, ok := v.(*TextPart)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if string(tp.Extra["newField"]) != `"keep"` {
		t.Fatalf("extras: %v", tp.Extra)
	}
	roundTripPart(t, in, v)
}

func TestUnmarshalPart_BlobExtraFieldsRoundTrip(t *testing.T) {
	in := []byte(`{"inlineData":{"data":"AAA","futureField":"keep","mimeType":"image/png"}}`)
	v, err := UnmarshalPart(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ip, ok := v.(*InlineDataPart)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if string(ip.InlineData.Extra["futureField"]) != `"keep"` {
		t.Fatalf("blob extras: %v", ip.InlineData.Extra)
	}
	roundTripPart(t, in, v)
}

func TestUnmarshalPart_Empty(t *testing.T) {
	_, err := UnmarshalPart([]byte(`{}`))
	if !errors.Is(err, ErrEmptyPart) {
		t.Fatalf("want ErrEmptyPart, got %v", err)
	}
}

func TestUnmarshalPart_NotJSON(t *testing.T) {
	_, err := UnmarshalPart([]byte(`not json`))
	if err == nil {
		t.Fatalf("expected error on invalid JSON")
	}
}

func TestUnmarshalPart_KnownKeyMalformedBody(t *testing.T) {
	_, err := UnmarshalPart([]byte(`{"text":["not","a","string"]}`))
	if err == nil {
		t.Fatalf("expected error on malformed text part body")
	}
}

func TestUnmarshalPart_UnknownKeyMalformedBody(t *testing.T) {
	_, err := UnmarshalPart([]byte(`{"futurePart":` + string(rune(0xFFFD)) + `}`))
	if err == nil {
		t.Fatalf("expected error on malformed unknown part body")
	}
}

func TestUnmarshalParts_MixedSlice(t *testing.T) {
	in := []byte(`[` +
		`{"text":"hi"},` +
		`{"inlineData":{"data":"AAA","mimeType":"image/png"}},` +
		`{"functionCall":{"name":"x","args":{}}},` +
		`{"functionResponse":{"name":"x","response":{}}},` +
		`{"executableCode":{"code":"1+1","language":"PYTHON"}},` +
		`{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":"2"}},` +
		`{"fileData":{"fileUri":"gs://b/f","mimeType":"text/plain"}},` +
		`{"futureThing":{}}` +
		`]`)
	parts, err := UnmarshalParts(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parts) != 8 {
		t.Fatalf("len = %d", len(parts))
	}
	wantKinds := []string{
		"text", "inlineData", "functionCall", "functionResponse",
		"executableCode", "codeExecutionResult", "fileData", "unknown",
	}
	for i, p := range parts {
		if p.PartKind() != wantKinds[i] {
			t.Fatalf("elem %d kind = %q want %q", i, p.PartKind(), wantKinds[i])
		}
	}
	if _, ok := parts[7].(*UnknownPart); !ok {
		t.Fatalf("elem 7 = %T", parts[7])
	}
}

func TestUnmarshalParts_NotArray(t *testing.T) {
	_, err := UnmarshalParts([]byte(`{"not":"array"}`))
	if err == nil {
		t.Fatalf("expected error on non-array input")
	}
}

func TestUnmarshalParts_ElementError(t *testing.T) {
	_, err := UnmarshalParts([]byte(`[{}]`))
	if err == nil {
		t.Fatalf("expected error on empty element")
	}
	if !errors.Is(err, ErrEmptyPart) {
		t.Fatalf("expected wrapped ErrEmptyPart, got %v", err)
	}
}

func TestPart_AllExportedFieldsHaveJSONTag(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(TextPart{}),
		reflect.TypeOf(InlineDataPart{}),
		reflect.TypeOf(FileDataPart{}),
		reflect.TypeOf(FunctionCallPart{}),
		reflect.TypeOf(FunctionResponsePart{}),
		reflect.TypeOf(ExecutableCodePart{}),
		reflect.TypeOf(CodeExecutionResultPart{}),
		reflect.TypeOf(UnknownPart{}),
		reflect.TypeOf(Blob{}),
		reflect.TypeOf(FileData{}),
		reflect.TypeOf(FunctionCall{}),
		reflect.TypeOf(FunctionResponse{}),
		reflect.TypeOf(ExecutableCode{}),
		reflect.TypeOf(CodeExecutionResult{}),
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

func TestPart_FactoriesCoverEveryConcreteType(t *testing.T) {
	want := map[string]bool{
		"text":                false,
		"inlineData":          false,
		"fileData":            false,
		"functionCall":        false,
		"functionResponse":    false,
		"executableCode":      false,
		"codeExecutionResult": false,
	}
	for k := range partFactories {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected factory key %q", k)
		}
		want[k] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("missing factory for %q", k)
		}
	}
}

func roundTripPart(t *testing.T, in []byte, v Part) {
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
