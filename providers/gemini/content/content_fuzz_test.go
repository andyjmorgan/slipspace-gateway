package content

import (
	"encoding/json"
	"testing"
)

func FuzzUnmarshalGenerateContentRequest(f *testing.F) {
	seeds := []string{
		`{"contents":[{"parts":[{"text":"hi"}],"role":"user"}]}`,
		`{"contents":[{"parts":[{"inlineData":{"data":"AAA","mimeType":"image/png"}}],"role":"user"}]}`,
		`{"contents":[{"parts":[{"functionCall":{"args":{},"name":"x"}}],"role":"model"}]}`,
		`{"contents":[{"parts":[{"functionCall":{"args":{},"name":"x"},"thoughtSignature":"Cs"}],"role":"model"}],"tools":[{"functionDeclarations":[{"name":"x","parametersJsonSchema":{"type":"object"}}]}]}`,
		`{"contents":[{"parts":[{"text":"hi"}],"role":"user"}],"generationConfig":{"temperature":0.1}}`,
		`{"contents":[],"systemInstruction":{"parts":[{"text":"sys"}],"role":"system"}}`,
		`{"contents":[{"parts":[{"futurePart":{"a":1}}],"role":"user"}]}`,
		`{"contents":[{"parts":[{"text":"hi"}],"role":"user"}],"futureField":"keep"}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var req GenerateContentRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return
		}
		out, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var again GenerateContentRequest
		if err := json.Unmarshal(out, &again); err != nil {
			t.Fatalf("re-unmarshal of marshalled output failed: %v\norig: %s\nmarshalled: %s", err, data, out)
		}
	})
}

func FuzzUnmarshalGenerateContentResponse(f *testing.F) {
	seeds := []string{
		`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"finishReason":"STOP","index":0}]}`,
		`{"candidates":[],"promptFeedback":{"blockReason":"SAFETY"}}`,
		`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":1,"totalTokenCount":2}}`,
		`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"}}],"futureField":42}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"args":{},"name":"x"},"thoughtSignature":"Cs"}],"role":"model"},"finishMessage":"done","finishReason":"STOP"}],"usageMetadata":{"serviceTier":"standard","totalTokenCount":1}}`,
		`{"error":{"code":404,"message":"nope","status":"NOT_FOUND"}}`,
		`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"groundingMetadata":{"searchEntryPoint":{"renderedContent":"<div/>"},"groundingChunks":[{"web":{"uri":"u","title":"t"}}],"groundingSupports":[{"segment":{"endIndex":2,"text":"hi"},"groundingChunkIndices":[0]}],"webSearchQueries":["q"]}}],"usageMetadata":{"toolUsePromptTokensDetails":[{"modality":"TEXT","tokenCount":1}],"totalTokenCount":1}}`,
		`{"modelVersion":"v","responseId":"r"}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var resp GenerateContentResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return
		}
		out, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var again GenerateContentResponse
		if err := json.Unmarshal(out, &again); err != nil {
			t.Fatalf("re-unmarshal failed: %v\norig: %s\nmarshalled: %s", err, data, out)
		}
	})
}
