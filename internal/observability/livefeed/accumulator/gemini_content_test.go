package accumulator

import (
	"encoding/json"
	"strings"
	"testing"

	geminicontent "github.com/andyjmorgan/slipspace-gateway/protocols/gemini/content"
)

// TestAccumulate_GeminiToolThoughtSignature locks the load-bearing behaviour
// that a function-call part's thoughtSignature survives the streaming
// reassembly. The gemini accumulator rebuilds the response from typed parts
// (it is not raw passthrough), so a part field it cannot model would be lost
// on reassembly even though it round-trips on a single non-streamed response.
func TestAccumulate_GeminiToolThoughtSignature(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Checking."}],"role":"model"},"index":0}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"args":{"city":"Dublin"},"name":"get_weather"},"thoughtSignature":"CsABAQw51se"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"candidatesTokenCount":2,"promptTokenCount":1,"totalTokenCount":3}}`,
		"",
		"",
	}, "\n"))

	got := Accumulate("gemini", "generate_content", raw)
	if got.Partial {
		t.Fatalf("unexpected partial reassembly")
	}

	var resp geminicontent.GenerateContentResponse
	if err := json.Unmarshal(got.Assembled, &resp); err != nil {
		t.Fatalf("assembled not parseable: %v\n%s", err, got.Assembled)
	}
	var sig *string
	for _, c := range resp.Candidates {
		if c.Content == nil {
			continue
		}
		for _, p := range c.Content.Parts {
			if fc, ok := p.(*geminicontent.FunctionCallPart); ok {
				sig = fc.ThoughtSignature
			}
		}
	}
	if sig == nil || *sig != "CsABAQw51se" {
		t.Fatalf("thoughtSignature did not survive reassembly: %s", got.Assembled)
	}
}

// TestAccumulate_GeminiCodeExecutionAndGrounding locks rollup fidelity for the
// built-in tools: the code-execution parts (executableCode /
// codeExecutionResult) arrive whole on separate chunks and must survive
// reassembly intact, and a candidate's grounding metadata (carried on the
// final chunk) must be preserved on the assembled response. These are the
// shapes the span projection now surfaces as server tool parts; the connector
// record relies on the assembled body keeping them verbatim.
func TestAccumulate_GeminiCodeExecutionAndGrounding(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Computing."}],"role":"model"},"index":0}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"executableCode":{"language":"PYTHON","code":"print(1+1)"}}],"role":"model"},"index":0}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":"2\n"}}],"role":"model"},"index":0}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"text":"Done."}],"role":"model"},"finishReason":"STOP","finishMessage":"complete","index":0,` +
			`"groundingMetadata":{"webSearchQueries":["sum of 1 and 1"],"groundingChunks":[{"web":{"uri":"https://ex.com","title":"Math"}}]},` +
			`"urlContextMetadata":{"urlMetadata":[{"retrievedUrl":"https://ex.com"}]},` +
			`"citationMetadata":{"citationSources":[{"uri":"https://ex.com"}]},` +
			`"safetyRatings":[{"category":"HARM_CATEGORY_HARASSMENT","probability":"NEGLIGIBLE"}],` +
			`"logprobsResult":{"topCandidates":[]},` +
			`"futureCandidateField":{"x":1}}],"usageMetadata":{"totalTokenCount":9}}`,
		"",
		"",
	}, "\n"))

	got := Accumulate("gemini", "generate_content", raw)
	if got.Partial {
		t.Fatalf("unexpected partial reassembly")
	}
	var resp geminicontent.GenerateContentResponse
	if err := json.Unmarshal(got.Assembled, &resp); err != nil {
		t.Fatalf("assembled not parseable: %v\n%s", err, got.Assembled)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].Content == nil {
		t.Fatalf("candidates = %+v", resp.Candidates)
	}
	var haveCode, haveResult bool
	for _, p := range resp.Candidates[0].Content.Parts {
		switch v := p.(type) {
		case *geminicontent.ExecutableCodePart:
			haveCode = true
			if v.ExecutableCode.Code != "print(1+1)" {
				t.Errorf("executableCode code = %q", v.ExecutableCode.Code)
			}
		case *geminicontent.CodeExecutionResultPart:
			haveResult = true
			if v.CodeExecutionResult.Outcome != "OUTCOME_OK" {
				t.Errorf("codeExecutionResult outcome = %q", v.CodeExecutionResult.Outcome)
			}
		}
	}
	if !haveCode || !haveResult {
		t.Errorf("code-exec parts lost in reassembly: code=%v result=%v\n%s", haveCode, haveResult, got.Assembled)
	}
	if gm := resp.Candidates[0].GroundingMetadata; gm == nil || len(gm.WebSearchQueries) == 0 {
		t.Errorf("groundingMetadata lost in reassembly: %s", got.Assembled)
	}
	// Every candidate-level metadata field Gemini delivered on the terminal
	// chunk — including an unknown future field — must survive reassembly.
	asm := string(got.Assembled)
	for _, want := range []string{
		"finishMessage", "urlContextMetadata", "citationMetadata",
		"safetyRatings", "logprobsResult", "futureCandidateField",
	} {
		if !strings.Contains(asm, want) {
			t.Errorf("candidate metadata %q dropped in reassembly:\n%s", want, asm)
		}
	}
}

// TestAccumulate_GeminiTopLevelErrorAndUnknownFields locks the fix for the
// rollup dropping response-level data absorb() didn't explicitly copy: an
// unknown top-level field (invariant #1's DynamicProperties safety net) on an
// early chunk, a mid-stream top-level error envelope, and the typed
// modelStatus member must all survive into the assembled rollup exactly as a
// non-streaming decode would keep them. This replays the empirical scenario
// from the rollup-fidelity audit (issue #476).
func TestAccumulate_GeminiTopLevelErrorAndUnknownFields(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"ab"}],"role":"model"},"index":0}],"someNewGoogleField":{"k":1},"modelStatus":{"modelStage":"LEGACY","retirementTime":"2026-09-24T00:00:00Z","message":"deprecated"}}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"text":"c"}],"role":"model"},"finishReason":"STOP","index":0}],"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}`,
		"",
		"",
	}, "\n"))

	got := Accumulate("gemini", "generate_content", raw)
	if got.Partial {
		t.Fatalf("unexpected partial reassembly")
	}
	var resp geminicontent.GenerateContentResponse
	if err := json.Unmarshal(got.Assembled, &resp); err != nil {
		t.Fatalf("assembled not parseable: %v\n%s", err, got.Assembled)
	}
	if len(resp.Error) == 0 || !strings.Contains(string(resp.Error), "RESOURCE_EXHAUSTED") {
		t.Errorf("top-level error envelope dropped in reassembly: %s", got.Assembled)
	}
	if raw, ok := resp.Extra["someNewGoogleField"]; !ok || string(raw) != `{"k":1}` {
		t.Errorf("unknown top-level field dropped in reassembly: %s", got.Assembled)
	}
	if resp.ModelStatus == nil || resp.ModelStatus.ModelStage != "LEGACY" ||
		resp.ModelStatus.RetirementTime != "2026-09-24T00:00:00Z" {
		t.Errorf("modelStatus dropped in reassembly: %s", got.Assembled)
	}
}

// TestAccumulate_GeminiEmptyPlaceholderDoesNotWipeMetadata locks the
// documented last-non-EMPTY-wins carry for candidate metadata: a wire
// placeholder ("groundingMetadata":{} etc., or null) arriving on a later
// chunk unmarshals to a non-nil pointer-to-zero-struct (or a 2-byte
// RawMessage) and, pre-fix, overwrote the populated subtree — destroying
// every citation and source URL for a grounded answer.
func TestAccumulate_GeminiEmptyPlaceholderDoesNotWipeMetadata(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Grounded."}],"role":"model"},"index":0,` +
			`"groundingMetadata":{"webSearchQueries":["q"],"groundingChunks":[{"web":{"uri":"https://ex.com","title":"t"}}]},` +
			`"citationMetadata":{"citationSources":[{"uri":"https://ex.com"}]},` +
			`"urlContextMetadata":{"urlMetadata":[{"retrievedUrl":"https://ex.com"}]}}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"text":" Done."}],"role":"model"},"finishReason":"STOP","index":0,` +
			`"groundingMetadata":{},"citationMetadata":{},"urlContextMetadata":{}}],"usageMetadata":{"totalTokenCount":3}}`,
		"",
		"",
	}, "\n"))

	got := Accumulate("gemini", "generate_content", raw)
	if got.Partial {
		t.Fatalf("unexpected partial reassembly")
	}
	var resp geminicontent.GenerateContentResponse
	if err := json.Unmarshal(got.Assembled, &resp); err != nil {
		t.Fatalf("assembled not parseable: %v\n%s", err, got.Assembled)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("candidates = %+v", resp.Candidates)
	}
	c := resp.Candidates[0]
	if gm := c.GroundingMetadata; gm == nil || len(gm.WebSearchQueries) != 1 || len(gm.GroundingChunks) != 1 {
		t.Errorf("populated groundingMetadata wiped by empty placeholder: %s", got.Assembled)
	}
	if cm := c.CitationMetadata; cm == nil || len(cm.CitationSources) != 1 {
		t.Errorf("populated citationMetadata wiped by empty placeholder: %s", got.Assembled)
	}
	if !strings.Contains(string(c.URLContextMetadata), "retrievedUrl") {
		t.Errorf("populated urlContextMetadata wiped by empty placeholder: %s", got.Assembled)
	}
}

// TestAccumulate_GeminiPreservesPartOrder locks the rollup-fidelity fix for
// interleaved code-execution transcripts: text/code/text must round-trip in the
// order Gemini streamed them, with consecutive text deltas merged but a text
// run that straddles a code block kept on the correct side. Pre-fix the
// accumulator concatenated ALL text into one part and emitted it first, sinking
// the code parts to the end and destroying the transcript order an operator
// reads.
func TestAccumulate_GeminiPreservesPartOrder(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Let me compute "}],"role":"model"},"index":0}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"text":"1+1.\n"}],"role":"model"},"index":0}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"executableCode":{"language":"PYTHON","code":"print(1+1)"}}],"role":"model"},"index":0}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":"2\n"}}],"role":"model"},"index":0}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"text":"The answer is 2."}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"totalTokenCount":9}}`,
		"",
		"",
	}, "\n"))

	got := Accumulate("gemini", "generate_content", raw)
	if got.Partial {
		t.Fatalf("unexpected partial reassembly")
	}
	var resp geminicontent.GenerateContentResponse
	if err := json.Unmarshal(got.Assembled, &resp); err != nil {
		t.Fatalf("assembled not parseable: %v\n%s", err, got.Assembled)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].Content == nil {
		t.Fatalf("candidates = %+v", resp.Candidates)
	}
	parts := resp.Candidates[0].Content.Parts
	// Expected order: merged leading text, code, result, trailing text.
	if len(parts) != 4 {
		t.Fatalf("part count = %d, want 4 (text, code, result, text):\n%s", len(parts), got.Assembled)
	}
	t0, ok := parts[0].(*geminicontent.TextPart)
	if !ok || t0.Text != "Let me compute 1+1.\n" {
		t.Errorf("part[0] = %+v, want merged leading text", parts[0])
	}
	if _, ok := parts[1].(*geminicontent.ExecutableCodePart); !ok {
		t.Errorf("part[1] = %T, want executableCode", parts[1])
	}
	if _, ok := parts[2].(*geminicontent.CodeExecutionResultPart); !ok {
		t.Errorf("part[2] = %T, want codeExecutionResult", parts[2])
	}
	t3, ok := parts[3].(*geminicontent.TextPart)
	if !ok || t3.Text != "The answer is 2." {
		t.Errorf("part[3] = %+v, want trailing text after code", parts[3])
	}
}
