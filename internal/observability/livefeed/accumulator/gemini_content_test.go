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
