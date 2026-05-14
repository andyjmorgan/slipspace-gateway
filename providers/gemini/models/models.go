// Package models models the Google Gemini v1beta /models list endpoint.
// Both ListModelsResponse and Model embed models.DynamicProperties so
// provider-added fields round-trip intact.
package models

import (
	sluicemodels "github.com/andyjmorgan/sluice-gateway/models"
)

// ListModelsResponse is the payload returned by GET /v1beta/models.
type ListModelsResponse struct {
	Models []Model `json:"models,omitempty"`

	NextPageToken string `json:"nextPageToken,omitempty"`

	sluicemodels.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (r *ListModelsResponse) UnmarshalJSON(data []byte) error {
	return sluicemodels.UnmarshalDynamic(data, r)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (r ListModelsResponse) MarshalJSON() ([]byte, error) {
	return sluicemodels.MarshalDynamic(r)
}

// Model is one entry in the ListModelsResponse.Models slice.
type Model struct {
	Name string `json:"name,omitempty"`

	BaseModelID string `json:"baseModelId,omitempty"`

	Version string `json:"version,omitempty"`

	DisplayName string `json:"displayName,omitempty"`

	Description string `json:"description,omitempty"`

	InputTokenLimit *int `json:"inputTokenLimit,omitempty"`

	OutputTokenLimit *int `json:"outputTokenLimit,omitempty"`

	SupportedGenerationMethods []string `json:"supportedGenerationMethods,omitempty"`

	Temperature *float64 `json:"temperature,omitempty"`

	MaxTemperature *float64 `json:"maxTemperature,omitempty"`

	TopP *float64 `json:"topP,omitempty"`

	TopK *int `json:"topK,omitempty"`

	sluicemodels.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *Model) UnmarshalJSON(data []byte) error {
	return sluicemodels.UnmarshalDynamic(data, m)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m Model) MarshalJSON() ([]byte, error) {
	return sluicemodels.MarshalDynamic(m)
}
