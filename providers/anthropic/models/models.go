// Package models models Anthropic's GET /v1/models endpoint.
package models

import (
	sluicemodels "github.com/andyjmorgan/sluice-gateway/models"
)

// ListModelsResponse is the response shape for Anthropic's GET /v1/models.
type ListModelsResponse struct {
	Data []Model `json:"data"`

	HasMore bool `json:"has_more"`

	FirstID *string `json:"first_id,omitempty"`

	LastID *string `json:"last_id,omitempty"`

	sluicemodels.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (r *ListModelsResponse) UnmarshalJSON(data []byte) error {
	return sluicemodels.UnmarshalDynamic(data, r)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (r ListModelsResponse) MarshalJSON() ([]byte, error) { return sluicemodels.MarshalDynamic(r) }

// Model describes one entry in ListModelsResponse.Data.
type Model struct {
	ID string `json:"id"`

	DisplayName string `json:"display_name,omitempty"`

	Type string `json:"type,omitempty"`

	CreatedAt string `json:"created_at,omitempty"`

	sluicemodels.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *Model) UnmarshalJSON(data []byte) error { return sluicemodels.UnmarshalDynamic(data, m) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m Model) MarshalJSON() ([]byte, error) { return sluicemodels.MarshalDynamic(m) }
