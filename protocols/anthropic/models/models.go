// Package models models Anthropic's GET /v1/models wire surface — a paged
// list of available models with no polymorphism.
//
// Both ListModelsResponse and Model embed models.DynamicProperties so any
// field Anthropic adds after this package was built still round-trips when
// the response flows through the gateway.
package models

import (
	slipspacemodels "github.com/andyjmorgan/slipspace-gateway/models"
)

// ListModelsResponse is the response body returned by Anthropic's GET
// /v1/models endpoint. Unknown fields round-trip via the embedded
// DynamicProperties.
type ListModelsResponse struct {
	// Data is the page of model entries.
	Data []Model `json:"data"`

	// HasMore reports whether more pages remain after this one.
	HasMore bool `json:"has_more"`

	// FirstID is the ID of the first model on this page (pagination
	// cursor anchor).
	FirstID *string `json:"first_id,omitempty"`

	// LastID is the ID of the last model on this page; pass as
	// before_id/after_id on a subsequent request to paginate.
	LastID *string `json:"last_id,omitempty"`

	slipspacemodels.DynamicProperties
}

// UnmarshalJSON decodes data into r, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (r *ListModelsResponse) UnmarshalJSON(data []byte) error {
	return slipspacemodels.UnmarshalDynamic(data, r)
}

// MarshalJSON encodes r and merges DynamicProperties.Extra back into the
// resulting object.
func (r ListModelsResponse) MarshalJSON() ([]byte, error) { return slipspacemodels.MarshalDynamic(r) }

// Model describes one entry in ListModelsResponse.Data. Unknown fields
// round-trip via the embedded DynamicProperties.
type Model struct {
	// ID is the Anthropic model identifier (e.g.,
	// "claude-sonnet-4-7-20250105").
	ID string `json:"id"`

	// DisplayName is the human-friendly model name surfaced in UIs.
	DisplayName string `json:"display_name,omitempty"`

	// Type is the Anthropic object discriminator (typically "model").
	Type string `json:"type,omitempty"`

	// CreatedAt is the model creation time as an RFC 3339 string.
	CreatedAt string `json:"created_at,omitempty"`

	slipspacemodels.DynamicProperties
}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *Model) UnmarshalJSON(data []byte) error { return slipspacemodels.UnmarshalDynamic(data, m) }

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m Model) MarshalJSON() ([]byte, error) { return slipspacemodels.MarshalDynamic(m) }
