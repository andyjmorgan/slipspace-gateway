package content

import (
	"encoding/json"
	"testing"
)

func FuzzUnmarshalPart(f *testing.F) {
	seeds := []string{
		`{"text":"hi"}`,
		`{"text":"hi","thought":true,"thoughtSignature":"s"}`,
		`{"inlineData":{"data":"AAA","mimeType":"image/png"}}`,
		`{"fileData":{"fileUri":"gs://b/f","mimeType":"text/plain"}}`,
		`{"functionCall":{"name":"x","args":{"q":"r"}}}`,
		`{"functionResponse":{"name":"x","response":{"r":1}}}`,
		`{"executableCode":{"code":"1","language":"PYTHON"}}`,
		`{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":"1"}}`,
		`{"futurePart":{"a":1}}`,
		`{"text":"hi","extraField":"keep"}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := UnmarshalPart(data)
		if err != nil {
			return
		}
		out, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if _, err := UnmarshalPart(out); err != nil {
			t.Fatalf("re-unmarshal failed: %v\norig: %s\nmarshalled: %s", err, data, out)
		}
	})
}
