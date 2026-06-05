package accumulator

import (
	"encoding/json"

	openairesponses "github.com/andyjmorgan/sluice-gateway/protocols/openai/responses"
)

// accumulateOpenAIResponses walks a stream of OpenAI /v1/responses
// SSE events and emits the JSON-encoded ResponsesResponse that the
// non-streaming endpoint would have returned.
//
// Reassembly is trivial because OpenAI ships the full snapshot in
// the `response.created`, `response.in_progress`, and
// `response.completed` events as a `response` field. The most recent
// snapshot wins — `response.completed` if seen, otherwise the latest
// `response.in_progress`, otherwise the initial `response.created`.
// `response.failed` carries a final-state snapshot too and takes
// precedence over a still-in-progress one.
func accumulateOpenAIResponses(raw []byte) Result {
	res := Result{}
	var (
		snapshot   json.RawMessage
		hasFinal   bool
		hasPartial bool
	)

	for _, ev := range parseSSE(raw) {
		if ev.Data == "" {
			continue
		}
		decoded, err := openairesponses.UnmarshalStreamEvent([]byte(ev.Data))
		if err != nil {
			res.Partial = true
			continue
		}
		switch e := decoded.(type) {
		case *openairesponses.ResponseCompletedEvent:
			if len(e.Response) > 0 {
				snapshot = e.Response
				hasFinal = true
			}
		case *openairesponses.ResponseFailedEvent:
			if len(e.Response) > 0 {
				snapshot = e.Response
				hasFinal = true
			}
		case *openairesponses.ResponseInProgressEvent:
			if !hasFinal && len(e.Response) > 0 {
				snapshot = e.Response
				hasPartial = true
			}
		case *openairesponses.ResponseCreatedEvent:
			if !hasFinal && !hasPartial && len(e.Response) > 0 {
				snapshot = e.Response
			}
		}
	}

	if len(snapshot) == 0 {
		// No snapshot event ever arrived (truncated stream, all
		// content-part deltas, etc.) — emit a minimal shell so the
		// caller still sees a parseable JSON object.
		out, err := json.Marshal(openairesponses.ResponsesResponse{Object: "response"})
		if err != nil {
			res.Partial = true
			return res
		}
		res.Assembled = out
		return res
	}

	res.Assembled = []byte(snapshot)
	if !hasFinal {
		res.Partial = true
	}
	return res
}
