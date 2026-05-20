// Package accumulator reassembles streamed provider responses (SSE-shaped
// payloads from OpenAI, Anthropic, Gemini) into the human-readable text
// + structured tool calls that the admin console's body endpoint surfaces.
//
// The package is consumed by the reporter at OnComplete — after the
// response has finished writing to the client, the captured raw bytes
// are run through Accumulate, and the result lands in the per-event
// BodyEnvelope alongside the raw chunks.
//
// Per-provider/endpoint dispatch lives in accumulator.go. Each
// provider-shape implementation lives in its own file (openai_chat.go,
// anthropic_messages.go, gemini_content.go). Unknown endpoints return
// an empty Result; the caller falls back to displaying the raw bytes.
package accumulator
