package accumulator

import (
	"encoding/json"
	"sort"

	openairesponses "github.com/andyjmorgan/slipspace-gateway/protocols/openai/responses"
)

// accumulateOpenAIResponses walks a stream of OpenAI /v1/responses
// SSE events and emits the JSON-encoded ResponsesResponse that the
// non-streaming endpoint would have returned.
//
// The lifecycle snapshot (`response.created` / `response.in_progress`
// / `response.completed` / `response.failed`) supplies the envelope —
// the most recent terminal snapshot wins, falling back to in_progress
// then created. But for streamed responses the snapshot's `output`
// ships as an empty `[]`; the finalized output items arrive separately
// as `response.output_item.done` events. We collect those keyed by
// `OutputIndex` and fold them into the assembled snapshot when its
// `output` is empty, so streaming↔non-streaming parity holds (#362).
func accumulateOpenAIResponses(raw []byte) Result {
	res := Result{}
	var (
		snapshot   json.RawMessage
		hasFinal   bool
		hasPartial bool
	)
	// finalizedItems holds the latest `response.output_item.done` payload
	// per OutputIndex. A later done for the same index overwrites the
	// earlier one (OpenAI may re-emit a finalized item).
	finalizedItems := map[int]json.RawMessage{}

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
		case *openairesponses.OutputItemDoneEvent:
			if len(e.Item) > 0 {
				finalizedItems[e.OutputIndex] = e.Item
			}
		}
	}

	if len(snapshot) == 0 {
		// No snapshot event ever arrived (truncated stream, all
		// content-part deltas, etc.). If finalized items did arrive,
		// emit a shell carrying them so callers still see the output;
		// otherwise emit a minimal parseable object.
		shell := openairesponses.ResponsesResponse{Object: "response"}
		if items := orderedFinalizedItems(finalizedItems); len(items) > 0 {
			shell.Output = items
		}
		out, err := json.Marshal(shell)
		if err != nil {
			res.Partial = true
			return res
		}
		res.Assembled = out
		res.Partial = true
		return res
	}

	res.Assembled = foldFinalizedOutput(snapshot, finalizedItems)
	if !hasFinal {
		res.Partial = true
	}
	return res
}

// foldFinalizedOutput returns snapshot with its `output` array populated
// from the finalized `response.output_item.done` items when the snapshot
// itself carries an empty `output` (the streaming case). A snapshot that
// already has output (a non-streaming-shaped completed event) is returned
// verbatim, as is any snapshot we cannot decode — the verbatim bytes are
// always a safe fallback.
func foldFinalizedOutput(snapshot json.RawMessage, finalized map[int]json.RawMessage) []byte {
	if len(finalized) == 0 {
		return []byte(snapshot)
	}
	var resp openairesponses.ResponsesResponse
	if err := json.Unmarshal(snapshot, &resp); err != nil {
		return []byte(snapshot)
	}
	if len(resp.Output) > 0 {
		// Snapshot already carries output — don't clobber it.
		return []byte(snapshot)
	}
	items := orderedFinalizedItems(finalized)
	if len(items) == 0 {
		return []byte(snapshot)
	}
	resp.Output = items
	out, err := json.Marshal(resp)
	if err != nil {
		return []byte(snapshot)
	}
	return out
}

// orderedFinalizedItems decodes the collected `response.output_item.done`
// payloads into OutputItems ordered by their OutputIndex. Items that fail
// to decode are skipped (the typed shape evolves; a malformed item must
// not drop the rest). Returns nil when nothing decodes.
func orderedFinalizedItems(finalized map[int]json.RawMessage) []openairesponses.OutputItem {
	if len(finalized) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(finalized))
	for idx := range finalized {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	items := make([]openairesponses.OutputItem, 0, len(indexes))
	for _, idx := range indexes {
		var item openairesponses.OutputItem
		if err := json.Unmarshal(finalized[idx], &item); err != nil {
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil
	}
	return items
}
