// Package models models Google Gemini's GET /v1beta/models wire surface —
// a paged list of available models with no polymorphism.
//
// Both ListModelsResponse and Model embed models.DynamicProperties so any
// field Google adds after this package was built still round-trips when
// the response flows through the gateway.
package models

import (
	sluicemodels "github.com/andyjmorgan/sluice-gateway/models"
)

// ListModelsResponse is the response body returned by Gemini's GET
// /v1beta/models endpoint. Unknown fields round-trip via the embedded
// DynamicProperties.
type ListModelsResponse struct {
	// Models is the page of model entries.
	Models []Model `json:"models,omitempty"`

	// NextPageToken is the opaque cursor for the next page; pass back
	// as pageToken on a subsequent request. Empty on the last page.
	NextPageToken string `json:"nextPageToken,omitempty"`

	sluicemodels.DynamicProperties
}

// UnmarshalJSON decodes data into r, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (r *ListModelsResponse) UnmarshalJSON(data []byte) error {
	return sluicemodels.UnmarshalDynamic(data, r)
}

// MarshalJSON encodes r and merges DynamicProperties.Extra back into the
// resulting object.
func (r ListModelsResponse) MarshalJSON() ([]byte, error) {
	return sluicemodels.MarshalDynamic(r)
}

// Model is one entry in ListModelsResponse.Models. Unknown fields
// round-trip via the embedded DynamicProperties.
type Model struct {
	// Name is the model resource name (e.g., "models/gemini-2.0-pro").
	Name string `json:"name,omitempty"`

	// BaseModelID is the underlying base model when this entry is a
	// tuned variant.
	BaseModelID string `json:"baseModelId,omitempty"`

	// Version is the model version string.
	Version string `json:"version,omitempty"`

	// DisplayName is the human-friendly model name surfaced in UIs.
	DisplayName string `json:"displayName,omitempty"`

	// Description is the model's free-form description.
	Description string `json:"description,omitempty"`

	// InputTokenLimit caps the prompt token count this model accepts.
	InputTokenLimit *int `json:"inputTokenLimit,omitempty"`

	// OutputTokenLimit caps the response token count this model emits.
	OutputTokenLimit *int `json:"outputTokenLimit,omitempty"`

	// SupportedGenerationMethods lists the API methods supported on
	// this model (e.g., "generateContent", "streamGenerateContent").
	SupportedGenerationMethods []string `json:"supportedGenerationMethods,omitempty"`

	// Temperature is the model's default sampling temperature.
	Temperature *float64 `json:"temperature,omitempty"`

	// MaxTemperature is the maximum sampling temperature this model
	// accepts.
	MaxTemperature *float64 `json:"maxTemperature,omitempty"`

	// TopP is the model's default nucleus-sampling cutoff.
	TopP *float64 `json:"topP,omitempty"`

	// TopK is the model's default top-K sampling cutoff.
	TopK *int `json:"topK,omitempty"`

	sluicemodels.DynamicProperties
}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *Model) UnmarshalJSON(data []byte) error {
	return sluicemodels.UnmarshalDynamic(data, m)
}

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m Model) MarshalJSON() ([]byte, error) {
	return sluicemodels.MarshalDynamic(m)
}
