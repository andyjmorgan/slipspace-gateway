package accumulator

import (
	"encoding/json"
	"testing"

	openairesponses "github.com/andyjmorgan/sluice-gateway/protocols/openai/responses"
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
