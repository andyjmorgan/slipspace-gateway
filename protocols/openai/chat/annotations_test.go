package chat

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestAnnotation_AllVariants pins the polymorphic dispatch + byte-equivalent
// round-trip for every modelled annotation type plus the unknown fallback.
func TestAnnotation_AllVariants(t *testing.T) {
	cases := []struct {
		name string

		json string

		typ string

		want any
	}{
		{
			name: "url_citation",
			json: `{"type":"url_citation","url_citation":{"end_index":985,"start_index":764,"title":"Example","url":"https://example.com"}}`,
			typ:  "url_citation",
			want: &UrlCitationAnnotation{},
		},
		{
			name: "unknown",
			json: `{"file_citation":{"file_id":"f_1"},"type":"file_citation"}`,
			typ:  "file_citation",
			want: &UnknownAnnotation{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := UnmarshalAnnotation([]byte(tc.json))
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if reflect.TypeOf(a) != reflect.TypeOf(tc.want) {
				t.Fatalf("got %T want %T", a, tc.want)
			}
			if a.AnnotationType() != tc.typ {
				t.Fatalf("annotation type = %q want %q", a.AnnotationType(), tc.typ)
			}
			out, err := json.Marshal(a)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !jsonValueEqual(t, []byte(tc.json), out) {
				t.Fatalf("drift\n in: %s\nout: %s", tc.json, out)
			}
		})
	}
}

// TestAnnotation_UrlCitationFieldsTyped confirms the nested url_citation object
// decodes onto the typed struct fields (not just round-trips via Extra).
func TestAnnotation_UrlCitationFieldsTyped(t *testing.T) {
	in := []byte(`{"type":"url_citation","url_citation":{"end_index":985,"start_index":764,"title":"Example","url":"https://example.com"}}`)
	a, err := UnmarshalAnnotation(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	uc, ok := a.(*UrlCitationAnnotation)
	if !ok {
		t.Fatalf("got %T", a)
	}
	if uc.URLCitation.StartIndex != 764 || uc.URLCitation.EndIndex != 985 {
		t.Fatalf("indices: %+v", uc.URLCitation)
	}
	if uc.URLCitation.Title != "Example" || uc.URLCitation.URL != "https://example.com" {
		t.Fatalf("citation: %+v", uc.URLCitation)
	}
}

// TestAnnotation_UnknownDiscriminatorRoundTrips exercises the UnknownAnnotation
// fallback: an unmodelled type preserves its discriminator AND sibling fields
// via DynamicProperties so the payload round-trips byte-equivalent.
func TestAnnotation_UnknownDiscriminatorRoundTrips(t *testing.T) {
	in := []byte(`{"file_citation":{"file_id":"f_42"},"future_field":7,"type":"file_citation"}`)
	a, err := UnmarshalAnnotation(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	u, ok := a.(*UnknownAnnotation)
	if !ok {
		t.Fatalf("got %T", a)
	}
	if u.AnnotationType() != "file_citation" {
		t.Fatalf("type = %q", u.AnnotationType())
	}
	if string(u.Extra["file_citation"]) != `{"file_id":"f_42"}` {
		t.Fatalf("extras: %v", u.Extra)
	}
	out, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}
}

// TestAnnotation_UrlCitationUnknownField confirms an unknown field on a typed
// url_citation annotation rides along via DynamicProperties.
func TestAnnotation_UrlCitationUnknownField(t *testing.T) {
	in := []byte(`{"confidence":0.9,"type":"url_citation","url_citation":{"end_index":2,"start_index":0,"url":"https://x.test"}}`)
	a, err := UnmarshalAnnotation(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	uc, ok := a.(*UrlCitationAnnotation)
	if !ok {
		t.Fatalf("got %T", a)
	}
	if string(uc.Extra["confidence"]) != `0.9` {
		t.Fatalf("extras: %v", uc.Extra)
	}
	out, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}
}

// TestAnnotation_MissingType errors when the discriminator is absent.
func TestAnnotation_MissingType(t *testing.T) {
	if _, err := UnmarshalAnnotation([]byte(`{"url_citation":{}}`)); err == nil {
		t.Fatalf("expected error for missing type")
	}
}

// TestAnnotations_NonArray errors when the array decoder gets a non-array.
func TestAnnotations_NonArray(t *testing.T) {
	if _, err := UnmarshalAnnotations([]byte(`{"not":"array"}`)); err == nil {
		t.Fatalf("expected error on non-array input")
	}
}

// TestAnnotationList_UnmarshalNull decodes a JSON null to a nil list so an
// explicit null and an absent field behave identically.
func TestAnnotationList_UnmarshalNull(t *testing.T) {
	var l AnnotationList
	if err := l.UnmarshalJSON([]byte(`null`)); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if l != nil {
		t.Fatalf("expected nil list, got %v", l)
	}
}

// TestAnnotationList_UnmarshalError surfaces a decode error on a malformed
// array element.
func TestAnnotationList_UnmarshalError(t *testing.T) {
	var l AnnotationList
	if err := l.UnmarshalJSON([]byte(`[{"no":"type"}]`)); err == nil {
		t.Fatalf("expected error on malformed element")
	}
}

// TestAnnotationList_MarshalNil marshals a nil list to JSON null.
func TestAnnotationList_MarshalNil(t *testing.T) {
	var l AnnotationList
	out, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != "null" {
		t.Fatalf("got %s", out)
	}
}

// TestResponseMessage_AnnotationsRoundTrip is the load-bearing case: the
// interface-typed annotations slice must decode onto the typed field through
// ResponseMessage's models.UnmarshalDynamic path and round-trip
// byte-equivalent (modulo key order), with unknown annotation types preserved.
func TestResponseMessage_AnnotationsRoundTrip(t *testing.T) {
	in := []byte(`{` +
		`"annotations":[` +
		`{"type":"url_citation","url_citation":{"end_index":985,"start_index":764,"title":"Example","url":"https://example.com"}},` +
		`{"future_field":1,"type":"file_citation"}` +
		`],` +
		`"content":"see sources",` +
		`"role":"assistant"` +
		`}`)
	var m ResponseMessage
	if err := json.Unmarshal(in, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.Annotations) != 2 {
		t.Fatalf("annotations len = %d", len(m.Annotations))
	}
	uc, ok := m.Annotations[0].(*UrlCitationAnnotation)
	if !ok {
		t.Fatalf("annotation[0] = %T", m.Annotations[0])
	}
	if uc.URLCitation.URL != "https://example.com" {
		t.Fatalf("url = %q", uc.URLCitation.URL)
	}
	if _, ok := m.Annotations[1].(*UnknownAnnotation); !ok {
		t.Fatalf("annotation[1] = %T", m.Annotations[1])
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}
}

// TestResponseMessage_NoAnnotations confirms an absent annotations field stays
// absent (omitempty drops the nil list) so reply messages without web search
// round-trip unchanged.
func TestResponseMessage_NoAnnotations(t *testing.T) {
	in := []byte(`{"content":"hi","role":"assistant"}`)
	var m ResponseMessage
	if err := json.Unmarshal(in, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Annotations != nil {
		t.Fatalf("expected nil annotations, got %v", m.Annotations)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("drift\n in: %s\nout: %s", in, out)
	}
}

// TestResponseMessage_EmptyAnnotationsCollapsesToAbsent documents the
// intentional omitempty collapse on the real ResponseMessage path (distinct
// from TestAnnotationList_MarshalNil, which exercises MarshalJSON in
// isolation): an explicit "annotations":[] decodes to an empty list and
// re-marshals to an omitted key, because encoding/json checks len==0 for
// omitempty before ever calling AnnotationList.MarshalJSON. Lossless for a
// gateway that forwards bodies verbatim and only uses the typed model for
// capture/translation/projection.
func TestResponseMessage_EmptyAnnotationsCollapsesToAbsent(t *testing.T) {
	var m ResponseMessage
	if err := json.Unmarshal([]byte(`{"annotations":[],"content":"hi","role":"assistant"}`), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, []byte(`{"content":"hi","role":"assistant"}`), out) {
		t.Fatalf("empty annotations did not collapse to absent: %s", out)
	}
}

// FuzzUnmarshalAnnotation drives the annotation registry: if it parses, it
// must re-marshal without error.
func FuzzUnmarshalAnnotation(f *testing.F) {
	seeds := []string{
		`{"type":"url_citation","url_citation":{"start_index":0,"end_index":2,"title":"t","url":"https://x.test"}}`,
		`{"type":"url_citation","url_citation":{}}`,
		`{"type":"file_citation","file_citation":{"file_id":"f_1"}}`,
		`{"type":"future","extra":1}`,
		`{}`,
		`null`,
		`[]`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		a, err := UnmarshalAnnotation([]byte(in))
		if err != nil {
			return
		}
		if _, err := json.Marshal(a); err != nil {
			t.Fatalf("marshal after parse: %v\nin: %s", err, in)
		}
	})
}
