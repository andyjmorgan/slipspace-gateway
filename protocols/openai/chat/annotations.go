package chat

import (
	"encoding/json"
	"fmt"

	"github.com/andyjmorgan/slipspace-gateway/models"
)

// Annotation is the polymorphic interface implemented by every concrete
// chat-completions message annotation — UrlCitationAnnotation and the
// UnknownAnnotation fallback.
//
// Annotations appear on choices[].message.annotations when a web-search-enabled
// model cites sources in its reply. The discriminator is the "type" field;
// today OpenAI only ships "url_citation", but the field is documented as
// extensible, so unknown discriminator values are dispatched to
// UnknownAnnotation, which preserves both the type value AND any sibling JSON
// fields via DynamicProperties so the payload round-trips intact.
//
// Decode a JSON annotations array via UnmarshalAnnotations (or let
// AnnotationList do it during ResponseMessage decoding) rather than calling
// json.Unmarshal into a []Annotation directly — Go cannot pick the concrete
// type from a bare interface, so registry dispatch is what makes the
// round-trip work.
//
// Ref: OpenAI Chat Completions API reference, choices[].message.annotations
// (https://developers.openai.com/api/reference/chat-completions). Note the
// Chat Completions nesting ({type:"url_citation", url_citation:{...}}) differs
// from the Responses API, which flattens the citation fields onto the
// annotation object.
type Annotation interface {
	// AnnotationType returns the wire discriminator value of the concrete
	// annotation type ("url_citation", ...).
	AnnotationType() string

	isAnnotation()
}

// URLCitation is the nested object on a UrlCitationAnnotation carrying the
// cited web source and the span of the assistant message it backs. Unknown
// fields round-trip via the embedded DynamicProperties.
//
// Ref: OpenAI Chat Completions API reference — the url_citation object holds
// start_index/end_index (character offsets into the message content), title,
// and url.
type URLCitation struct {
	// StartIndex is the index of the first character of the citation span in
	// the assistant message content.
	StartIndex int `json:"start_index"`

	// EndIndex is the index of the last character of the citation span in the
	// assistant message content.
	EndIndex int `json:"end_index"`

	// Title is the title of the cited web resource.
	Title string `json:"title,omitempty"`

	// URL is the URL of the cited web resource.
	URL string `json:"url,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *URLCitation) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c URLCitation) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// UrlCitationAnnotation is the "url_citation" annotation variant — a single web
// source the model cited, with the message span the citation backs. Unknown
// fields round-trip via the embedded DynamicProperties.
type UrlCitationAnnotation struct {
	// Type is the wire "type" discriminator, always "url_citation".
	Type string `json:"type"`

	// URLCitation carries the cited source URL/title and the cited span's
	// character indices.
	URLCitation URLCitation `json:"url_citation"`

	models.DynamicProperties
}

// AnnotationType returns the "url_citation" discriminator.
func (UrlCitationAnnotation) AnnotationType() string { return "url_citation" }

func (UrlCitationAnnotation) isAnnotation() {}

// UnmarshalJSON decodes data into a, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (a *UrlCitationAnnotation) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON encodes a and merges DynamicProperties.Extra back into the
// resulting object.
func (a UrlCitationAnnotation) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// UnknownAnnotation preserves any annotation whose "type" discriminator this
// package has not modelled. Type carries the unknown discriminator verbatim and
// every other JSON field lands in DynamicProperties.Extra so the annotation
// round-trips intact — critical for forward-compatibility with new annotation
// kinds OpenAI ships beyond url_citation.
type UnknownAnnotation struct {
	// Type is the unmodelled wire discriminator, preserved verbatim.
	Type string `json:"type"`

	models.DynamicProperties
}

// AnnotationType returns the unknown discriminator value verbatim.
func (a UnknownAnnotation) AnnotationType() string { return a.Type }

func (UnknownAnnotation) isAnnotation() {}

// UnmarshalJSON decodes data into a, routing every non-type field into
// DynamicProperties.Extra so the unknown annotation round-trips byte-equivalent.
func (a *UnknownAnnotation) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, a) }

// MarshalJSON encodes a and merges DynamicProperties.Extra back into the
// resulting object.
func (a UnknownAnnotation) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

var annotationRegistry = models.PolymorphicRegistry[Annotation]{
	DiscriminatorField: "type",
	Factories: map[string]func() Annotation{
		"url_citation": func() Annotation { return &UrlCitationAnnotation{} },
	},
	Fallback: func(disc string) Annotation { return &UnknownAnnotation{Type: disc} },
}

// UnmarshalAnnotation decodes a single chat-completions annotation JSON object,
// dispatching on the "type" discriminator. Any type this package has not
// modelled is returned as an UnknownAnnotation so the payload still round-trips.
func UnmarshalAnnotation(data []byte) (Annotation, error) {
	return annotationRegistry.UnmarshalOne(data)
}

// UnmarshalAnnotations decodes a JSON array of chat-completions annotations,
// dispatching each element on its "type" discriminator. Unmodelled types return
// as UnknownAnnotation.
func UnmarshalAnnotations(data []byte) ([]Annotation, error) {
	return annotationRegistry.UnmarshalSlice(data)
}

// AnnotationList is the typed-yet-polymorphic projection of the
// choices[].message.annotations array. It exists so the interface-typed
// []Annotation slice can be a plain struct field on ResponseMessage: that
// struct decodes through models.UnmarshalDynamic, which json.Unmarshals each
// known field into its address — and Go cannot decode into a bare []Annotation.
// AnnotationList carries the registry dispatch in its own UnmarshalJSON so the
// field decodes correctly and unknown annotation types still round-trip.
//
// This mirrors how MessageContent wraps the string-or-array content field for
// the same reason.
type AnnotationList []Annotation

// UnmarshalJSON decodes a JSON array of polymorphic annotations via the
// registry, dispatching unknown discriminators to UnknownAnnotation. A JSON
// null decodes to a nil list so an explicit null and an absent field behave
// identically.
func (l *AnnotationList) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*l = nil
		return nil
	}
	anns, err := UnmarshalAnnotations(data)
	if err != nil {
		return fmt.Errorf("openai/chat: decode annotations: %w", err)
	}
	*l = anns
	return nil
}

// MarshalJSON encodes the annotation list as a JSON array. A nil list marshals
// to null, but ResponseMessage tags the field omitempty so an empty list is
// dropped entirely rather than emitted as null.
func (l AnnotationList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("null"), nil
	}
	return json.Marshal([]Annotation(l))
}
