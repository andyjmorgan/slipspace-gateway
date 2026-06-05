// Package unmapped detects provider JSON fields the gateway's typed models do
// not model — the early-warning signal for provider drift. It walks the typed
// request body captured by bodycapture and the typed response reconstructed
// from the wire, surfacing every field that landed in a DynamicProperties.Extra
// map so the caller can record gateway.unmapped_fields.total with the field
// identities attached.
//
// Reporting and telemetry stay separate (CLAUDE.md invariant #4): this package
// only computes field-path sets; the reporter owns metric and log emission.
package unmapped

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/models"
	anthropicmessages "github.com/andyjmorgan/sluice-gateway/protocols/anthropic/messages"
	geminicontent "github.com/andyjmorgan/sluice-gateway/protocols/gemini/content"
	openaichat "github.com/andyjmorgan/sluice-gateway/protocols/openai/chat"
	openairesponses "github.com/andyjmorgan/sluice-gateway/protocols/openai/responses"
)

// RequestFields returns the sorted unmapped field paths on the typed request
// body captured by bodycapture (a *messages.MessagesRequest,
// *chat.ChatCompletionRequest, etc.). Returns nil for a nil body or one that
// carried no unknown fields. Passthrough requests have a nil body and yield
// nil — there is no typed shape to inspect.
func RequestFields(body any) []string {
	if body == nil {
		return nil
	}
	return models.CollectUnmapped(body)
}

// ResponseFields returns the sorted unmapped field paths on the response for
// endpoint. raw must be a complete, non-streaming-shaped response object —
// the single-frame body for a non-streaming response, or the accumulator's
// reassembled bytes for a streamed one. An unrecognised endpoint or a body
// that fails to parse into the typed response yields nil; partial accounting
// is worse than none.
func ResponseFields(endpoint string, raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	v, ok := typedResponse(endpoint)
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return nil
	}
	return models.CollectUnmapped(v)
}

// typedResponse returns a fresh pointer to the typed response struct for
// endpoint, dispatching on the same endpoint names the accumulator and token
// extractor use. ok is false for an endpoint with no modelled response shape.
func typedResponse(endpoint string) (any, bool) {
	switch endpoint {
	case "chat_completions", "chat":
		return &openaichat.ChatCompletionResponse{}, true
	case "responses":
		return &openairesponses.ResponsesResponse{}, true
	case "messages":
		return &anthropicmessages.MessagesResponse{}, true
	case "generate_content":
		return &geminicontent.GenerateContentResponse{}, true
	default:
		return nil, false
	}
}
