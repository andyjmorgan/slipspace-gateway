package accumulator

import (
	"encoding/json"
	"strconv"
	"strings"
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
	// Stream that only carries a text delta — no snapshot event and no
	// output_item.added envelope. The accumulator emits a minimal shell so
	// the caller still has parseable JSON to render, and the orphan delta
	// is kept on a synthesized in-flight message item rather than dropped
	// (#563; pre-fix this test pinned the drop as correct).
	raw := []byte("" +
		`event: response.output_text.delta` + "\n" +
		`data: {"type":"response.output_text.delta","item_id":"msg_orphan","output_index":0,"content_index":0,"delta":"hi"}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if !got.Partial {
		t.Error("Partial=false on snapshot-less stream")
	}
	resp := parseResponses(t, got.Assembled)
	if resp.Object != "response" {
		t.Errorf("Object=%q want response (shell)", resp.Object)
	}
	if len(resp.Output) != 1 {
		t.Fatalf("Output=%d want 1 synthesized message item carrying the orphan delta: %s", len(resp.Output), got.Assembled)
	}
	item := resp.Output[0]
	if item.Type != "message" || item.Role != "assistant" || item.ID != "msg_orphan" || item.Status != "in_progress" {
		t.Errorf("synthesized envelope = %+v", item)
	}
	if text := outputTexts(t, item.Content); len(text) != 1 || text[0] != "hi" {
		t.Errorf("content texts=%q want [hi]", text)
	}
}

// outputTexts decodes a message item's content array and returns the
// `text` of each part in order.
func outputTexts(t *testing.T, content json.RawMessage) []string {
	t.Helper()
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &parts); err != nil {
		t.Fatalf("content does not decode as a part array: %v\nbytes: %s", err, content)
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p.Type != "output_text" {
			t.Errorf("part type=%q want output_text", p.Type)
		}
		out = append(out, p.Text)
	}
	return out
}

func TestAccumulate_OpenAIResponses_InFlightCustomToolCallKept(t *testing.T) {
	t.Parallel()
	// Issue #563's captured shape (correlation 5cb073d6…): two
	// output_item.added (reasoning @0, custom_tool_call @1), one
	// output_item.done (@0 only), eleven custom_tool_call_input.delta
	// frames for @1, then response.completed with output:[] — the client
	// aborted mid-tool-call. Pre-fix the rollup carried only the reasoning
	// item; the custom_tool_call envelope and all its input vanished.
	deltas := []string{"const", " cmds", " =", " [", "\n", "  ", "\"", "rg", " -n", " \\\"", "tool"}
	var b strings.Builder
	b.WriteString(`event: response.created` + "\n")
	b.WriteString(`data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_563","object":"response","status":"in_progress","model":"gpt-5.1-codex","output":[]}}` + "\n\n")
	b.WriteString(`event: response.output_item.added` + "\n")
	b.WriteString(`data: {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}` + "\n\n")
	b.WriteString(`event: response.output_item.done` + "\n")
	b.WriteString(`data: {"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENC=="}}` + "\n\n")
	b.WriteString(`event: response.output_item.added` + "\n")
	b.WriteString(`data: {"type":"response.output_item.added","sequence_number":3,"output_index":1,"item":{"type":"custom_tool_call","id":"ctc_0187","call_id":"call_Bvxe","name":"exec","input":"","status":"in_progress"}}` + "\n\n")
	for i, d := range deltas {
		enc, _ := json.Marshal(d)
		b.WriteString(`event: response.custom_tool_call_input.delta` + "\n")
		b.WriteString(`data: {"type":"response.custom_tool_call_input.delta","sequence_number":` + strconv.Itoa(4+i) + `,"output_index":1,"item_id":"ctc_0187","delta":` + string(enc) + `}` + "\n\n")
	}
	b.WriteString(`event: response.completed` + "\n")
	b.WriteString(`data: {"type":"response.completed","sequence_number":99,"response":{"id":"resp_563","object":"response","status":"completed","model":"gpt-5.1-codex","output":[]}}` + "\n\n")

	got := Accumulate("openai", "responses", []byte(b.String()))
	if !got.Recognised {
		t.Fatalf("got %+v", got)
	}
	if !got.Partial {
		t.Error("Partial=false although an in-flight item was emitted")
	}
	resp := parseResponses(t, got.Assembled)
	if resp.ID != "resp_563" || resp.Status != "completed" {
		t.Errorf("envelope id=%q status=%q", resp.ID, resp.Status)
	}
	if len(resp.Output) != 2 {
		t.Fatalf("Output=%d want 2 (reasoning + in-flight custom_tool_call): %s", len(resp.Output), got.Assembled)
	}
	if resp.Output[0].Type != "reasoning" || string(resp.Output[0].EncryptedContent) != `"ENC=="` {
		t.Errorf("Output[0]=%+v want finalized reasoning item", resp.Output[0])
	}
	ctc := resp.Output[1]
	if ctc.Type != "custom_tool_call" || ctc.ID != "ctc_0187" || ctc.CallID != "call_Bvxe" || ctc.Name != "exec" {
		t.Errorf("custom_tool_call envelope = %+v", ctc)
	}
	if ctc.Status != "in_progress" {
		t.Errorf("Status=%q want in_progress (streamed value, never finalized)", ctc.Status)
	}
	// `input` has no typed field on OutputItem — a non-streaming decode
	// lands it in Extra, so the rollup must too.
	wantInput := `const cmds = [` + "\n" + `  "rg -n \"tool`
	var gotInput string
	if err := json.Unmarshal(ctc.Extra["input"], &gotInput); err != nil {
		t.Fatalf("Extra[input]=%s does not decode: %v", ctc.Extra["input"], err)
	}
	if gotInput != wantInput {
		t.Errorf("input=%q want %q", gotInput, wantInput)
	}
}

func TestAccumulate_OpenAIResponses_InFlightFunctionCallArgsMerged(t *testing.T) {
	t.Parallel()
	// function_call added, arguments streamed, stream cut before
	// output_item.done and before response.completed. The partial
	// arguments string is kept verbatim (it is a string field, so a
	// fragment is still a valid value).
	raw := []byte("" +
		`event: response.in_progress` + "\n" +
		`data: {"type":"response.in_progress","response":{"id":"resp_fc","object":"response","status":"in_progress","model":"gpt-5","output":[]}}` + "\n\n" +
		`event: response.output_item.added` + "\n" +
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"","status":"in_progress"}}` + "\n\n" +
		`event: response.function_call_arguments.delta` + "\n" +
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"city\":"}` + "\n\n" +
		`event: response.function_call_arguments.delta` + "\n" +
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"\"S"}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if !got.Partial {
		t.Error("Partial=false on truncated stream")
	}
	resp := parseResponses(t, got.Assembled)
	if len(resp.Output) != 1 {
		t.Fatalf("Output=%d want 1: %s", len(resp.Output), got.Assembled)
	}
	fc := resp.Output[0]
	if fc.Type != "function_call" || fc.Name != "get_weather" || fc.CallID != "call_1" || fc.Status != "in_progress" {
		t.Errorf("function_call envelope = %+v", fc)
	}
	if fc.Arguments != `{"city":"S` {
		t.Errorf("Arguments=%q want the merged partial fragment", fc.Arguments)
	}
}

func TestAccumulate_OpenAIResponses_ArgumentsDoneIsAuthoritative(t *testing.T) {
	t.Parallel()
	// function_call_arguments.done carries the full string; it wins over
	// the delta concatenation (which may have lost a frame).
	raw := []byte("" +
		`event: response.output_item.added` + "\n" +
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"f","status":"in_progress"}}` + "\n\n" +
		`event: response.function_call_arguments.delta` + "\n" +
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"a\":"}` + "\n\n" +
		`event: response.function_call_arguments.done` + "\n" +
		`data: {"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc_1","arguments":"{\"a\":1}"}` + "\n\n" +
		`event: response.completed` + "\n" +
		`data: {"type":"response.completed","response":{"id":"resp_ad","object":"response","status":"completed","model":"gpt-5","output":[]}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	resp := parseResponses(t, got.Assembled)
	if len(resp.Output) != 1 || resp.Output[0].Arguments != `{"a":1}` {
		t.Errorf("Output=%+v want arguments from the done event", resp.Output)
	}
	if !got.Partial {
		t.Error("Partial=false although the item never reached output_item.done")
	}
}

func TestAccumulate_OpenAIResponses_CustomToolCallInputDoneIsAuthoritative(t *testing.T) {
	t.Parallel()
	raw := []byte("" +
		`event: response.output_item.added` + "\n" +
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","id":"ctc_1","call_id":"call_1","name":"exec","input":"","status":"in_progress"}}` + "\n\n" +
		`event: response.custom_tool_call_input.delta` + "\n" +
		`data: {"type":"response.custom_tool_call_input.delta","output_index":0,"item_id":"ctc_1","delta":"ls"}` + "\n\n" +
		`event: response.custom_tool_call_input.done` + "\n" +
		`data: {"type":"response.custom_tool_call_input.done","output_index":0,"item_id":"ctc_1","input":"ls -la"}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	resp := parseResponses(t, got.Assembled)
	if len(resp.Output) != 1 {
		t.Fatalf("Output=%d want 1", len(resp.Output))
	}
	if string(resp.Output[0].Extra["input"]) != `"ls -la"` {
		t.Errorf("Extra[input]=%s want \"ls -la\" (done wins over deltas)", resp.Output[0].Extra["input"])
	}
}

func TestAccumulate_OpenAIResponses_InFlightMessageTextMerged(t *testing.T) {
	t.Parallel()
	// The 128-delta case from #563: a message item added, its content
	// part added, text streamed, then the stream dies. The reconstructed
	// part keeps the added part's scaffolding (annotations) and carries
	// the concatenated text.
	raw := []byte("" +
		`event: response.created` + "\n" +
		`data: {"type":"response.created","response":{"id":"resp_msg","object":"response","status":"in_progress","model":"gpt-5","output":[]}}` + "\n\n" +
		`event: response.output_item.added` + "\n" +
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}` + "\n\n" +
		`event: response.content_part.added` + "\n" +
		`data: {"type":"response.content_part.added","output_index":0,"item_id":"msg_1","content_index":0,"part":{"type":"output_text","text":"","annotations":[],"logprobs":[]}}` + "\n\n" +
		`event: response.output_text.delta` + "\n" +
		`data: {"type":"response.output_text.delta","output_index":0,"item_id":"msg_1","content_index":0,"delta":"Hello, "}` + "\n\n" +
		`event: response.output_text.delta` + "\n" +
		`data: {"type":"response.output_text.delta","output_index":0,"item_id":"msg_1","content_index":0,"delta":"world"}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if !got.Partial {
		t.Error("Partial=false on truncated stream")
	}
	resp := parseResponses(t, got.Assembled)
	if len(resp.Output) != 1 {
		t.Fatalf("Output=%d want 1: %s", len(resp.Output), got.Assembled)
	}
	msg := resp.Output[0]
	if msg.Type != "message" || msg.ID != "msg_1" || msg.Status != "in_progress" {
		t.Errorf("message envelope = %+v", msg)
	}
	if texts := outputTexts(t, msg.Content); len(texts) != 1 || texts[0] != "Hello, world" {
		t.Errorf("content texts=%q want [Hello, world]", texts)
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(msg.Content, &parts); err != nil || len(parts) != 1 {
		t.Fatalf("content=%s: %v", msg.Content, err)
	}
	if string(parts[0]["annotations"]) != `[]` {
		t.Errorf("added part scaffolding dropped: %s", msg.Content)
	}
}

func TestAccumulate_OpenAIResponses_ContentPartDoneAndTextDoneWin(t *testing.T) {
	t.Parallel()
	// Two content parts on one in-flight message: part 0 reached
	// content_part.done (used verbatim), part 1 only reached
	// output_text.done (its full text wins over the delta buffer).
	// Ordered by content_index regardless of arrival order.
	raw := []byte("" +
		`event: response.output_item.added` + "\n" +
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}` + "\n\n" +
		`event: response.output_text.delta` + "\n" +
		`data: {"type":"response.output_text.delta","output_index":0,"item_id":"msg_1","content_index":1,"delta":"seco"}` + "\n\n" +
		`event: response.output_text.done` + "\n" +
		`data: {"type":"response.output_text.done","output_index":0,"item_id":"msg_1","content_index":1,"text":"second"}` + "\n\n" +
		`event: response.content_part.done` + "\n" +
		`data: {"type":"response.content_part.done","output_index":0,"item_id":"msg_1","content_index":0,"part":{"type":"output_text","text":"first","annotations":[]}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	resp := parseResponses(t, got.Assembled)
	if len(resp.Output) != 1 {
		t.Fatalf("Output=%d want 1: %s", len(resp.Output), got.Assembled)
	}
	if texts := outputTexts(t, resp.Output[0].Content); len(texts) != 2 || texts[0] != "first" || texts[1] != "second" {
		t.Errorf("content texts=%q want [first second]", texts)
	}
}

func TestAccumulate_OpenAIResponses_DoneSupersedesPending(t *testing.T) {
	t.Parallel()
	// The normal, complete case: added → deltas → done → completed. The
	// finalized item wins over the in-flight reconstruction at the same
	// index and the result is not Partial.
	raw := []byte("" +
		`event: response.output_item.added` + "\n" +
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"f","arguments":"","status":"in_progress"}}` + "\n\n" +
		`event: response.function_call_arguments.delta` + "\n" +
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"a\":1}"}` + "\n\n" +
		`event: response.output_item.done` + "\n" +
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"f","arguments":"{\"a\":1}","status":"completed"}}` + "\n\n" +
		`event: response.completed` + "\n" +
		`data: {"type":"response.completed","response":{"id":"resp_ok","object":"response","status":"completed","model":"gpt-5","output":[]}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if got.Partial {
		t.Error("Partial=true although every item was finalized")
	}
	resp := parseResponses(t, got.Assembled)
	if len(resp.Output) != 1 || resp.Output[0].Status != "completed" || resp.Output[0].Arguments != `{"a":1}` {
		t.Errorf("Output=%+v want the finalized item only", resp.Output)
	}
}

func TestAccumulate_OpenAIResponses_OrphanDeltasSynthesizeEnvelope(t *testing.T) {
	t.Parallel()
	// Deltas with no output_item.added at all (truncated capture): the
	// item type is inferred from the delta kind so the bytes survive.
	raw := []byte("" +
		`event: response.function_call_arguments.delta` + "\n" +
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_x","delta":"{\"q\":1}"}` + "\n\n" +
		`event: response.custom_tool_call_input.delta` + "\n" +
		`data: {"type":"response.custom_tool_call_input.delta","output_index":1,"item_id":"ctc_x","delta":"pwd"}` + "\n\n" +
		`event: response.completed` + "\n" +
		`data: {"type":"response.completed","response":{"id":"resp_orphan","object":"response","status":"completed","model":"gpt-5","output":[]}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if !got.Partial {
		t.Error("Partial=false although synthesized in-flight items were emitted")
	}
	resp := parseResponses(t, got.Assembled)
	if len(resp.Output) != 2 {
		t.Fatalf("Output=%d want 2: %s", len(resp.Output), got.Assembled)
	}
	if fc := resp.Output[0]; fc.Type != "function_call" || fc.ID != "fc_x" || fc.Arguments != `{"q":1}` || fc.Status != "in_progress" {
		t.Errorf("Output[0]=%+v want synthesized function_call", fc)
	}
	if ctc := resp.Output[1]; ctc.Type != "custom_tool_call" || ctc.ID != "ctc_x" || string(ctc.Extra["input"]) != `"pwd"` {
		t.Errorf("Output[1]=%+v want synthesized custom_tool_call", ctc)
	}
}

func TestAccumulate_OpenAIResponses_UndecodableAddedItemSkipped(t *testing.T) {
	t.Parallel()
	// output_item.added whose `item` is not an object cannot be
	// reconstructed; it is skipped (and so is a part whose `part` is not
	// an object — the text still lands on a minimal output_text part).
	raw := []byte("" +
		`event: response.output_item.added` + "\n" +
		`data: {"type":"response.output_item.added","output_index":0,"item":"not-an-object"}` + "\n\n" +
		`event: response.output_item.added` + "\n" +
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}` + "\n\n" +
		`event: response.content_part.added` + "\n" +
		`data: {"type":"response.content_part.added","output_index":1,"item_id":"msg_1","content_index":0,"part":"not-an-object"}` + "\n\n" +
		`event: response.output_text.delta` + "\n" +
		`data: {"type":"response.output_text.delta","output_index":1,"item_id":"msg_1","content_index":0,"delta":"ok"}` + "\n\n" +
		`event: response.completed` + "\n" +
		`data: {"type":"response.completed","response":{"id":"resp_bad","object":"response","status":"completed","model":"gpt-5","output":[]}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	resp := parseResponses(t, got.Assembled)
	if len(resp.Output) != 1 || resp.Output[0].ID != "msg_1" {
		t.Fatalf("Output=%+v want only the decodable message item", resp.Output)
	}
	if texts := outputTexts(t, resp.Output[0].Content); len(texts) != 1 || texts[0] != "ok" {
		t.Errorf("content texts=%q want [ok]", texts)
	}
}

func TestAccumulate_OpenAIResponses_EmptyPendingAndUnknownEventsYieldBareShell(t *testing.T) {
	t.Parallel()
	// Nothing reconstructable: a keep-alive comment, an unmodelled event,
	// and a content_part.added with no part (a pending entry with neither
	// envelope nor deltas). The shell carries no output and is Partial.
	raw := []byte("" +
		`: keep-alive` + "\n\n" +
		`event: response.reasoning_summary_text.delta` + "\n" +
		`data: {"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"thinking"}` + "\n\n" +
		`event: response.content_part.added` + "\n" +
		`data: {"type":"response.content_part.added","output_index":2,"item_id":"msg_x","content_index":0}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if !got.Partial {
		t.Error("Partial=false on snapshot-less stream")
	}
	resp := parseResponses(t, got.Assembled)
	if resp.Object != "response" || len(resp.Output) != 0 {
		t.Errorf("want bare shell, got %s", got.Assembled)
	}
}

func TestAccumulate_OpenAIResponses_AddedWithoutDeltasKept(t *testing.T) {
	t.Parallel()
	// An added envelope with no deltas at all (stream cut immediately
	// after the announce) is still emitted as-is.
	raw := []byte("" +
		`event: response.output_item.added` + "\n" +
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}` + "\n\n" +
		`event: response.in_progress` + "\n" +
		`data: {"type":"response.in_progress","response":{"id":"resp_cut","object":"response","status":"in_progress","model":"gpt-5","output":[]}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	resp := parseResponses(t, got.Assembled)
	if len(resp.Output) != 1 || resp.Output[0].Type != "reasoning" || resp.Output[0].ID != "rs_1" {
		t.Errorf("Output=%+v want the added reasoning envelope", resp.Output)
	}
}

func TestAccumulate_OpenAIResponses_IncompleteIsTerminal(t *testing.T) {
	t.Parallel()
	// response.incomplete is a terminal snapshot like completed/failed:
	// the stream ended normally, the provider just stopped short.
	raw := []byte("" +
		`event: response.in_progress` + "\n" +
		`data: {"type":"response.in_progress","response":{"id":"resp_inc","object":"response","status":"in_progress","model":"gpt-5","output":[]}}` + "\n\n" +
		`event: response.output_item.done` + "\n" +
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"incomplete","content":[{"type":"output_text","text":"trunc"}]}}` + "\n\n" +
		`event: response.incomplete` + "\n" +
		`data: {"type":"response.incomplete","response":{"id":"resp_inc","object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"model":"gpt-5","output":[]}}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if got.Partial {
		t.Error("Partial=true on incomplete-terminal snapshot — terminal events should clear Partial")
	}
	resp := parseResponses(t, got.Assembled)
	if resp.Status != "incomplete" {
		t.Errorf("Status=%q want incomplete", resp.Status)
	}
	if resp.IncompleteDetails == nil || resp.IncompleteDetails.Reason != "max_output_tokens" {
		t.Errorf("IncompleteDetails=%+v", resp.IncompleteDetails)
	}
	if len(resp.Output) != 1 || resp.Output[0].ID != "msg_1" {
		t.Errorf("Output=%+v want the finalized item folded in", resp.Output)
	}
}

func TestAccumulate_OpenAIResponses_ErrorEventMarksPartial(t *testing.T) {
	t.Parallel()
	// A flat `error` event means the stream broke; even with a completed
	// snapshot the reconstruction is flagged Partial.
	raw := []byte("" +
		`event: response.completed` + "\n" +
		`data: {"type":"response.completed","response":{"id":"resp_err","object":"response","status":"completed","model":"gpt-5","output":[]}}` + "\n\n" +
		`event: error` + "\n" +
		`data: {"type":"error","code":"server_error","message":"upstream reset","param":null,"sequence_number":3}` + "\n\n")
	got := Accumulate("openai", "responses", raw)
	if !got.Partial {
		t.Error("Partial=false despite an error event")
	}
	resp := parseResponses(t, got.Assembled)
	if resp.ID != "resp_err" {
		t.Errorf("ID=%q want resp_err", resp.ID)
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
	// encrypted_content is a typed OutputItem field and must survive the fold.
	if got := string(resp.Output[0].EncryptedContent); got != `"ENC=="` {
		t.Errorf("reasoning encrypted_content dropped; EncryptedContent=%q Extra=%v", got, resp.Output[0].Extra)
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
