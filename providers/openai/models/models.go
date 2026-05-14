// Package models models OpenAI's GET /v1/models wire surface — a flat list
// of model descriptors with no polymorphism.
//
// Both ListModelsResponse and Model embed models.DynamicProperties so any
// field OpenAI adds after this package was built still round-trips when the
// response flows through the gateway.
package models

import (
	rootmodels "github.com/andyjmorgan/sluice-gateway/models"
)

// ListModelsResponse is the response body returned by OpenAI's GET /v1/models
// endpoint. Unknown fields round-trip via the embedded DynamicProperties.
type ListModelsResponse struct {
	// Object is the OpenAI object discriminator (typically "list").
	Object string `json:"object"`

	// Data is the list of available models.
	Data []Model `json:"data"`

	rootmodels.DynamicProperties
}

// UnmarshalJSON decodes data into r, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (r *ListModelsResponse) UnmarshalJSON(data []byte) error {
	return rootmodels.UnmarshalDynamic(data, r)
}

// MarshalJSON encodes r and merges DynamicProperties.Extra back into the
// resulting object.
func (r ListModelsResponse) MarshalJSON() ([]byte, error) { return rootmodels.MarshalDynamic(r) }

// Model is one entry in ListModelsResponse.Data. Unknown fields round-trip
// via the embedded DynamicProperties.
type Model struct {
	// ID is the OpenAI model identifier (e.g., "gpt-4o").
	ID string `json:"id"`

	// Object is the OpenAI object discriminator (typically "model").
	Object string `json:"object"`

	// Created is the model creation time as a Unix epoch in seconds.
	Created int64 `json:"created"`

	// OwnedBy is the organisation that owns the model (e.g., "openai",
	// "system").
	OwnedBy string `json:"owned_by"`

	rootmodels.DynamicProperties
}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *Model) UnmarshalJSON(data []byte) error { return rootmodels.UnmarshalDynamic(data, m) }

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m Model) MarshalJSON() ([]byte, error) { return rootmodels.MarshalDynamic(m) }
