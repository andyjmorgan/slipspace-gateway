package accumulator

import (
	"encoding/json"
	"sort"
	"strings"

	openairesponses "github.com/andyjmorgan/slipspace-gateway/protocols/openai/responses"
)

// accumulateOpenAIResponses walks a stream of OpenAI /v1/responses
// SSE events and emits the JSON-encoded ResponsesResponse that the
// non-streaming endpoint would have returned.
//
// The lifecycle snapshot (`response.created` / `response.in_progress`
// / `response.completed` / `response.incomplete` / `response.failed`)
// supplies the envelope — the most recent terminal snapshot wins,
// falling back to in_progress then created. But for streamed responses
// the snapshot's `output` ships as an empty `[]`; the output items
// arrive separately. Finalized items come whole on
// `response.output_item.done`, collected keyed by `OutputIndex` (#362).
//
// Items that were announced by `response.output_item.added` but never
// finalized — the normal shape when the client aborts mid-tool-call or
// the upstream ends the stream early — are reconstructed from the added
// envelope plus every delta accumulated for them (`output_text.delta`,
// `function_call_arguments.delta`, `custom_tool_call_input.delta`),
// with `status` left at the streamed value, and the result is flagged
// Partial. A non-streaming request the client abandoned would still have
// carried the item in the provider's body, so the rollup keeps it too;
// this mirrors the gen_ai span path in
// internal/middleware/genaiattr/response.go (#563).
func accumulateOpenAIResponses(raw []byte) Result {
	res := Result{}
	var (
		snapshot   json.RawMessage
		hasFinal   bool
		hasPartial bool
		sawError   bool
	)
	// finalizedItems holds the latest `response.output_item.done` payload
	// per OutputIndex. A later done for the same index overwrites the
	// earlier one (OpenAI may re-emit a finalized item).
	finalizedItems := map[int]json.RawMessage{}
	// pending holds the in-flight reconstruction of every item seen on
	// `response.output_item.added` or referenced by a delta, keyed by
	// OutputIndex. An entry whose index also appears in finalizedItems is
	// superseded by the finalized payload at fold time.
	pending := map[int]*responsesItemState{}
	pendingFor := func(idx int) *responsesItemState {
		st, ok := pending[idx]
		if !ok {
			st = &responsesItemState{parts: map[int]*responsesPartState{}}
			pending[idx] = st
		}
		return st
	}

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
		case *openairesponses.ResponseIncompleteEvent:
			// Terminal like completed: the provider stopped generating
			// (max_output_tokens, content filter) and says so in the
			// snapshot's status / incomplete_details.
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
		case *openairesponses.ErrorEvent:
			// The stream errored; whatever we reconstruct is not the body
			// a non-streaming call would have returned.
			sawError = true
		case *openairesponses.OutputItemAddedEvent:
			if len(e.Item) > 0 {
				pendingFor(e.OutputIndex).added = e.Item
			}
		case *openairesponses.OutputItemDoneEvent:
			if len(e.Item) > 0 {
				finalizedItems[e.OutputIndex] = e.Item
			}
		case *openairesponses.ContentPartAddedEvent:
			st := pendingFor(e.OutputIndex)
			st.itemID = firstNonEmptyString(st.itemID, e.ItemID)
			st.partFor(e.ContentIndex).added = e.Part
		case *openairesponses.ContentPartDoneEvent:
			st := pendingFor(e.OutputIndex)
			st.itemID = firstNonEmptyString(st.itemID, e.ItemID)
			st.partFor(e.ContentIndex).done = e.Part
		case *openairesponses.OutputTextDeltaEvent:
			st := pendingFor(e.OutputIndex)
			st.itemID = firstNonEmptyString(st.itemID, e.ItemID)
			st.partFor(e.ContentIndex).text.WriteString(e.Delta)
		case *openairesponses.OutputTextDoneEvent:
			st := pendingFor(e.OutputIndex)
			st.itemID = firstNonEmptyString(st.itemID, e.ItemID)
			t := e.Text
			st.partFor(e.ContentIndex).textDone = &t
		case *openairesponses.FunctionCallArgumentsDeltaEvent:
			st := pendingFor(e.OutputIndex)
			st.itemID = firstNonEmptyString(st.itemID, e.ItemID)
			st.args.WriteString(e.Delta)
		case *openairesponses.FunctionCallArgumentsDoneEvent:
			st := pendingFor(e.OutputIndex)
			st.itemID = firstNonEmptyString(st.itemID, e.ItemID)
			a := e.Arguments
			st.argsDone = &a
		case *openairesponses.CustomToolCallInputDeltaEvent:
			st := pendingFor(e.OutputIndex)
			st.itemID = firstNonEmptyString(st.itemID, e.ItemID)
			st.input.WriteString(e.Delta)
		case *openairesponses.CustomToolCallInputDoneEvent:
			st := pendingFor(e.OutputIndex)
			st.itemID = firstNonEmptyString(st.itemID, e.ItemID)
			in := e.Input
			st.inputDone = &in
		}
	}

	if len(snapshot) == 0 {
		// No snapshot event ever arrived (truncated stream, all
		// content-part deltas, etc.). Emit a shell carrying whatever
		// items — finalized or in-flight — did arrive, so callers still
		// see the output; otherwise a minimal parseable object.
		shell := openairesponses.ResponsesResponse{Object: "response"}
		if items, _ := orderedOutputItems(finalizedItems, pending); len(items) > 0 {
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

	var inflight bool
	res.Assembled, inflight = foldOutput(snapshot, finalizedItems, pending)
	if !hasFinal || inflight || sawError {
		res.Partial = true
	}
	return res
}

// responsesItemState carries the in-flight reconstruction of one output
// item: the envelope from `response.output_item.added` plus the deltas
// streamed for it. Only the buffers matching the item's type end up
// populated — arguments for function_call, input for custom_tool_call,
// content parts for message.
type responsesItemState struct {
	// added is the OutputItem envelope as emitted by
	// response.output_item.added; empty when only deltas were observed.
	added json.RawMessage
	// itemID is the item_id the deltas referenced, used to label a
	// synthesized envelope when no added event was seen.
	itemID string
	// args accumulates function_call_arguments.delta fragments; argsDone
	// is the authoritative full string from function_call_arguments.done.
	args     strings.Builder
	argsDone *string
	// input accumulates custom_tool_call_input.delta fragments; inputDone
	// is the authoritative full string from custom_tool_call_input.done.
	input     strings.Builder
	inputDone *string
	// parts holds the per-content_index reconstruction of a message
	// item's content array.
	parts map[int]*responsesPartState
}

// responsesPartState carries the in-flight reconstruction of one content
// part inside a message item.
type responsesPartState struct {
	// added is the part shape from response.content_part.added (text
	// empty, annotations/logprobs scaffolding present).
	added json.RawMessage
	// done is the finalized part from response.content_part.done; when
	// present it wins over the reconstruction verbatim.
	done json.RawMessage
	// text accumulates output_text.delta fragments; textDone is the
	// authoritative full string from output_text.done.
	text     strings.Builder
	textDone *string
}

func (s *responsesItemState) partFor(idx int) *responsesPartState {
	p, ok := s.parts[idx]
	if !ok {
		p = &responsesPartState{}
		s.parts[idx] = p
	}
	return p
}

// hasDeltas reports whether any streamed fragment was observed for the
// item — the signal that a pending entry with no added envelope still
// carries bytes worth emitting.
func (s *responsesItemState) hasDeltas() bool {
	if s.args.Len() > 0 || s.argsDone != nil || s.input.Len() > 0 || s.inputDone != nil {
		return true
	}
	for _, p := range s.parts {
		if p.text.Len() > 0 || p.textDone != nil || len(p.added) > 0 || len(p.done) > 0 {
			return true
		}
	}
	return false
}

// item materializes the in-flight state into the OutputItem a
// non-streaming body would have carried. Returns ok=false when there is
// nothing to emit (no envelope and no deltas) or the envelope does not
// decode.
func (s *responsesItemState) item() (openairesponses.OutputItem, bool) {
	var item openairesponses.OutputItem
	switch {
	case len(s.added) > 0:
		if err := json.Unmarshal(s.added, &item); err != nil {
			return item, false
		}
	case s.hasDeltas():
		// Deltas arrived without an added envelope (truncated capture).
		// Synthesize the envelope from the delta kind so the bytes are
		// not dropped — the same defensive stance anthropic_messages.go
		// takes for a delta with no block_start.
		item.ID = s.itemID
		item.Status = "in_progress"
		switch {
		case s.args.Len() > 0 || s.argsDone != nil:
			item.Type = "function_call"
		case s.input.Len() > 0 || s.inputDone != nil:
			item.Type = "custom_tool_call"
		default:
			item.Type = "message"
			item.Role = "assistant"
		}
	default:
		return item, false
	}

	// function_call: arguments is a plain string field, so even a
	// truncated fragment is a valid value — keep it verbatim (that is the
	// point of surfacing the aborted call).
	if s.argsDone != nil {
		item.Arguments = *s.argsDone
	} else if s.args.Len() > 0 {
		item.Arguments = s.args.String()
	}

	// custom_tool_call: `input` has no typed field on OutputItem, so a
	// non-streaming decode lands it in DynamicProperties.Extra — write
	// the reconstruction to the same place for parity.
	var input *string
	if s.inputDone != nil {
		input = s.inputDone
	} else if s.input.Len() > 0 {
		v := s.input.String()
		input = &v
	}
	if input != nil {
		if enc, err := json.Marshal(*input); err == nil {
			if item.Extra == nil {
				item.Extra = map[string]json.RawMessage{}
			}
			item.Extra["input"] = enc
		}
	}

	if content := s.content(); content != nil {
		item.Content = content
	}
	return item, true
}

// content rebuilds a message item's content array from the per-part
// state, ordered by content_index. Returns nil when no part state exists
// so the envelope's own content (typically `[]`) is left untouched.
func (s *responsesItemState) content() json.RawMessage {
	if len(s.parts) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(s.parts))
	for idx := range s.parts {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	parts := make([]json.RawMessage, 0, len(indexes))
	for _, idx := range indexes {
		p := s.parts[idx]
		if len(p.done) > 0 {
			parts = append(parts, p.done)
			continue
		}
		var text string
		if p.textDone != nil {
			text = *p.textDone
		} else {
			text = p.text.String()
		}
		// Start from the added part so its scaffolding (annotations,
		// logprobs, any field added later) survives; fall back to the
		// minimal output_text shape.
		part := map[string]json.RawMessage{}
		if len(p.added) > 0 {
			if err := json.Unmarshal(p.added, &part); err != nil {
				part = map[string]json.RawMessage{}
			}
		}
		if _, ok := part["type"]; !ok {
			part["type"] = json.RawMessage(`"output_text"`)
		}
		enc, err := json.Marshal(text)
		if err != nil {
			continue
		}
		part["text"] = enc
		raw, err := json.Marshal(part)
		if err != nil {
			continue
		}
		parts = append(parts, raw)
	}
	if len(parts) == 0 {
		return nil
	}
	out, err := json.Marshal(parts)
	if err != nil {
		return nil
	}
	return out
}

// foldOutput returns snapshot with its `output` array populated from the
// finalized `response.output_item.done` items plus any in-flight items
// when the snapshot itself carries an empty `output` (the streaming
// case). The returned bool reports whether at least one in-flight item
// was emitted, so the caller can flag the result Partial. A snapshot that
// already has output (a non-streaming-shaped completed event) is returned
// verbatim, as is any snapshot we cannot decode — the verbatim bytes are
// always a safe fallback.
func foldOutput(snapshot json.RawMessage, finalized map[int]json.RawMessage, pending map[int]*responsesItemState) ([]byte, bool) {
	if len(finalized) == 0 && len(pending) == 0 {
		return []byte(snapshot), false
	}
	var resp openairesponses.ResponsesResponse
	if err := json.Unmarshal(snapshot, &resp); err != nil {
		return []byte(snapshot), false
	}
	if len(resp.Output) > 0 {
		// Snapshot already carries output — don't clobber it.
		return []byte(snapshot), false
	}
	items, inflight := orderedOutputItems(finalized, pending)
	if len(items) == 0 {
		return []byte(snapshot), false
	}
	resp.Output = items
	out, err := json.Marshal(resp)
	if err != nil {
		return []byte(snapshot), false
	}
	return out, inflight
}

// orderedOutputItems merges the finalized `response.output_item.done`
// payloads with the in-flight items into OutputItems ordered by
// OutputIndex. A finalized item supersedes the pending state at the same
// index. Items that fail to decode are skipped (the typed shape evolves; a
// malformed item must not drop the rest). The bool reports whether any
// in-flight item was emitted. Returns nil when nothing decodes.
func orderedOutputItems(finalized map[int]json.RawMessage, pending map[int]*responsesItemState) ([]openairesponses.OutputItem, bool) {
	if len(finalized) == 0 && len(pending) == 0 {
		return nil, false
	}
	seen := map[int]struct{}{}
	indexes := make([]int, 0, len(finalized)+len(pending))
	for idx := range finalized {
		seen[idx] = struct{}{}
		indexes = append(indexes, idx)
	}
	for idx := range pending {
		if _, ok := seen[idx]; !ok {
			indexes = append(indexes, idx)
		}
	}
	sort.Ints(indexes)

	var inflight bool
	items := make([]openairesponses.OutputItem, 0, len(indexes))
	for _, idx := range indexes {
		if raw, ok := finalized[idx]; ok {
			var item openairesponses.OutputItem
			if err := json.Unmarshal(raw, &item); err != nil {
				continue
			}
			items = append(items, item)
			continue
		}
		item, ok := pending[idx].item()
		if !ok {
			continue
		}
		items = append(items, item)
		inflight = true
	}
	if len(items) == 0 {
		return nil, false
	}
	return items, inflight
}

func firstNonEmptyString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
