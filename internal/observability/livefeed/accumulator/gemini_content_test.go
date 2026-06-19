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
