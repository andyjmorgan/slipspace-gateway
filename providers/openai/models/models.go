// Package models models the OpenAI /v1/models wire surface — a flat list of
// models with no polymorphism. Every exported struct embeds
// models.DynamicProperties so provider-added fields round-trip intact.
package models

import (
	rootmodels "github.com/andyjmorgan/sluice-gateway/models"
)

// ListModelsResponse is the JSON body returned from GET /v1/models.
type ListModelsResponse struct {
	Object string `json:"object"`

	Data []Model `json:"data"`

	rootmodels.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (r *ListModelsResponse) UnmarshalJSON(data []byte) error {
	return rootmodels.UnmarshalDynamic(data, r)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (r ListModelsResponse) MarshalJSON() ([]byte, error) { return rootmodels.MarshalDynamic(r) }

// Model is one model entry returned from GET /v1/models.
type Model struct {
	ID string `json:"id"`

	Object string `json:"object"`

	Created int64 `json:"created"`

	OwnedBy string `json:"owned_by"`

	rootmodels.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *Model) UnmarshalJSON(data []byte) error { return rootmodels.UnmarshalDynamic(data, m) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m Model) MarshalJSON() ([]byte, error) { return rootmodels.MarshalDynamic(m) }
