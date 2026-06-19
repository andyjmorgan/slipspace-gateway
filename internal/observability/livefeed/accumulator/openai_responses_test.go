package accumulator

import (
	"encoding/json"
	"testing"

	openairesponses "github.com/andyjmorgan/slipspace-gateway/protocols/openai/responses"
)

func parseResponses(t *testing.T, raw []byte) openairesponses.ResponsesResponse {
	t.Helper()
	var got openairesponses.ResponsesResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Assembled does not parse as ResponsesResponse: %v\nbytes: %s", err, raw)
	}
	return got
}

func TestAccumulate_OpenAIResponses_PrefersCompletedSnapshot(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: response.created` + "\n" +
		`data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1","object":"response","status":"in_progress","model":"gpt-4o-mini"}}` + "\n\n" +
		`event: response.in_progress` + "\n" +
		`data: {"type":"response.in_progress","sequence_number":1,"response":{"id":"resp_1","object":"response","status":"in_progress","model":"gpt-4o-mini","output":[]}}` + "\n\n" +
		`event: response.completed` + "\n" +
		`data: {"type":"response.completed","sequence_number":7,"response":{"id":"resp_1","object":"response","status":"completed","model":"gpt-4o-mini","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello, world!"}]}]}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if !got.Recognised || got.Partial {
		t.Fatalf("got %+v", got)
	}
	resp := parseResponses(t, got.Assembled)
	if resp.ID != "resp_1" {
		t.Errorf("ID=%q", resp.ID)
	}
	if resp.Status != "completed" {
		t.Errorf("Status=%q want completed (should reflect terminal snapshot)", resp.Status)
	}
	if len(resp.Output) != 1 {
		t.Errorf("Output=%d want 1", len(resp.Output))
	}
}

func TestAccumulate_OpenAIResponses_FailedTakesPrecedenceOverInProgress(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: response.in_progress` + "\n" +
		`data: {"type":"response.in_progress","response":{"id":"resp_x","object":"response","status":"in_progress","model":"gpt-4o-mini"}}` + "\n\n" +
		`event: response.failed` + "\n" +
		`data: {"type":"response.failed","response":{"id":"resp_x","object":"response","status":"failed","model":"gpt-4o-mini"},"error":{"type":"server_error"}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if got.Partial {
		t.Error("Partial=true on failed-terminal snapshot — terminal events should clear Partial")
	}
	resp := parseResponses(t, got.Assembled)
	if resp.Status != "failed" {
		t.Errorf("Status=%q want failed", resp.Status)
	}
}

func TestAccumulate_OpenAIResponses_PartialWhenOnlyInProgress(t *testing.T) {
	t.Parallel()
	// Stream truncated before response.completed — the in-progress
	// snapshot is the best we have and the result should be flagged
	// Partial so the UI surfaces it.
	raw := []byte("" +
		`event: response.created` + "\n" +
		`data: {"type":"response.created","response":{"id":"resp_t","object":"response","status":"in_progress","model":"gpt-4o-mini"}}` + "\n\n" +
		`event: response.in_progress` + "\n" +
		`data: {"type":"response.in_progress","response":{"id":"resp_t","object":"response","status":"in_progress","model":"gpt-4o-mini","output":[]}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if !got.Partial {
		t.Error("Partial=false on truncated stream")
	}
	resp := parseResponses(t, got.Assembled)
	if resp.ID != "resp_t" {
		t.Errorf("ID=%q", resp.ID)
	}
}

func TestAccumulate_OpenAIResponses_FallsBackToCreatedWhenNothingElse(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: response.created` + "\n" +
		`data: {"type":"response.created","response":{"id":"resp_c","object":"response","status":"in_progress","model":"gpt-4o-mini"}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if !got.Partial {
		t.Error("Partial=false even though no terminal event arrived")
	}
	resp := parseResponses(t, got.Assembled)
	if resp.ID != "resp_c" {
		t.Errorf("ID=%q want resp_c", resp.ID)
	}
}

func TestAccumulate_OpenAIResponses_NoSnapshotEventsYieldsShell(t *testing.T) {
	t.Parallel()
	// Stream that only carries content-part deltas and no snapshot
	// event — accumulator emits a minimal shell so the caller still
	// has parseable JSON to render.
	raw := []byte("" +
		`event: response.output_text.delta` + "\n" +
		`data: {"type":"response.output_text.delta","delta":"hi"}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	resp := parseResponses(t, got.Assembled)
	if resp.Object != "response" {
		t.Errorf("Object=%q want response (shell)", resp.Object)
	}
}

func TestAccumulate_OpenAIResponses_FoldsOutputItemDoneIntoEmptyCompleted(t *testing.T) {
	t.Parallel()
	// Streamed /v1/responses (store:false, gpt-5-class): the completed
	// snapshot legitimately ships output:[] and the real items arrive as
	// response.output_item.done. The rollup must fold them back in so it
	// matches a non-streaming decode (#362).
	raw := []byte("" +
		`event: response.created` + "\n" +
		`data: {"type":"response.created","response":{"id":"resp_s","object":"response","status":"in_progress","model":"gpt-5"}}` + "\n\n" +
		`event: response.output_item.done` + "\n" +
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","status":"completed","name":"get_weather","arguments":"{\"city\":\"SF\"}","call_id":"call_1"}}` + "\n\n" +
		`event: response.output_item.done` + "\n" +
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENC=="}}` + "\n\n" +
		`event: response.completed` + "\n" +
		`data: {"type":"response.completed","response":{"id":"resp_s","object":"response","status":"completed","model":"gpt-5","output":[]}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if !got.Recognised || got.Partial {
		t.Fatalf("got %+v", got)
	}
	resp := parseResponses(t, got.Assembled)
	if resp.Status != "completed" {
		t.Errorf("Status=%q want completed", resp.Status)
	}
	if len(resp.Output) != 2 {
		t.Fatalf("Output=%d want 2 (folded from output_item.done)", len(resp.Output))
	}
	// Ordered by OutputIndex: reasoning(0) then function_call(1).
	if resp.Output[0].Type != "reasoning" {
		t.Errorf("Output[0].Type=%q want reasoning (index 0 first)", resp.Output[0].Type)
	}
	if resp.Output[1].Type != "function_call" {
		t.Errorf("Output[1].Type=%q want function_call", resp.Output[1].Type)
	}
	if resp.Output[1].Name != "get_weather" || resp.Output[1].Arguments != `{"city":"SF"}` {
		t.Errorf("function_call name/args not preserved: name=%q args=%q", resp.Output[1].Name, resp.Output[1].Arguments)
	}
	// encrypted_content rides DynamicProperties.Extra on the reasoning item.
	if _, ok := resp.Output[0].Extra["encrypted_content"]; !ok {
		t.Errorf("reasoning encrypted_content dropped; Extra=%v", resp.Output[0].Extra)
	}
}

func TestAccumulate_OpenAIResponses_LaterOutputItemDoneOverwrites(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: response.output_item.done` + "\n" +
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","status":"in_progress","role":"assistant"}}` + "\n\n" +
		`event: response.output_item.done` + "\n" +
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","status":"completed","role":"assistant"}}` + "\n\n" +
		`event: response.completed` + "\n" +
		`data: {"type":"response.completed","response":{"id":"resp_o","object":"response","status":"completed","model":"gpt-5","output":[]}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	resp := parseResponses(t, got.Assembled)
	if len(resp.Output) != 1 {
		t.Fatalf("Output=%d want 1", len(resp.Output))
	}
	if resp.Output[0].Status != "completed" {
		t.Errorf("Output[0].Status=%q want completed (later done overwrites)", resp.Output[0].Status)
	}
}

func TestAccumulate_OpenAIResponses_PopulatedSnapshotNotClobbered(t *testing.T) {
	t.Parallel()
	// A snapshot that already carries output (non-streaming-shaped completed
	// event) must win over any stray output_item.done.
	raw := []byte("" +
		`event: response.output_item.done` + "\n" +
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"stray","role":"assistant"}}` + "\n\n" +
		`event: response.completed` + "\n" +
		`data: {"type":"response.completed","response":{"id":"resp_p","object":"response","status":"completed","model":"gpt-5","output":[{"type":"message","id":"real","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hi"}]}]}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	resp := parseResponses(t, got.Assembled)
	if len(resp.Output) != 1 || resp.Output[0].ID != "real" {
		t.Errorf("snapshot output clobbered: %+v", resp.Output)
	}
}

func TestAccumulate_OpenAIResponses_OutputItemDoneWithoutSnapshot(t *testing.T) {
	t.Parallel()
	// Truncated stream: output_item.done arrived but no response.* snapshot
	// ever did. The shell still carries the finalized items, flagged Partial.
	raw := []byte("" +
		`event: response.output_item.done` + "\n" +
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","role":"assistant"}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if !got.Partial {
		t.Error("Partial=false on snapshot-less stream")
	}
	resp := parseResponses(t, got.Assembled)
	if len(resp.Output) != 1 || resp.Output[0].ID != "m1" {
		t.Errorf("shell Output=%+v want the finalized item", resp.Output)
	}
}

func TestAccumulate_OpenAIResponses_UndecodableSnapshotReturnedVerbatim(t *testing.T) {
	t.Parallel()
	// A completed snapshot whose `response` is not an object can't be
	// decoded into ResponsesResponse; with finalized items present, the fold
	// must fall back to the verbatim snapshot rather than dropping it.
	raw := []byte("" +
		`event: response.output_item.done` + "\n" +
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","role":"assistant"}}` + "\n\n" +
		`event: response.completed` + "\n" +
		`data: {"type":"response.completed","response":[1,2,3]}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if string(got.Assembled) != "[1,2,3]" {
		t.Errorf("Assembled=%s want verbatim [1,2,3]", got.Assembled)
	}
}

func TestAccumulate_OpenAIResponses_UndecodableItemsLeaveOutputEmpty(t *testing.T) {
	t.Parallel()
	// output_item.done whose `item` is not an object fails to decode; with
	// no decodable item the empty-output snapshot is returned untouched
	// rather than folding garbage.
	raw := []byte("" +
		`event: response.output_item.done` + "\n" +
		`data: {"type":"response.output_item.done","output_index":0,"item":"not-an-object"}` + "\n\n" +
		`event: response.completed` + "\n" +
		`data: {"type":"response.completed","response":{"id":"resp_u","object":"response","status":"completed","model":"gpt-5","output":[]}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	resp := parseResponses(t, got.Assembled)
	if len(resp.Output) != 0 {
		t.Errorf("Output=%d want 0 (undecodable item skipped, nothing folded)", len(resp.Output))
	}
	if resp.ID != "resp_u" {
		t.Errorf("ID=%q want resp_u", resp.ID)
	}
}

func TestAccumulate_OpenAIResponses_MalformedEventMarksPartial(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: response.completed` + "\n" +
		`data: not json` + "\n\n" +
		`event: response.in_progress` + "\n" +
		`data: {"type":"response.in_progress","response":{"id":"resp_m","object":"response","status":"in_progress","model":"gpt-4o-mini"}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if !got.Partial {
		t.Error("Partial=false despite malformed event")
	}
	resp := parseResponses(t, got.Assembled)
	if resp.ID != "resp_m" {
		t.Errorf("ID=%q want resp_m (fallback to in_progress)", resp.ID)
	}
}
