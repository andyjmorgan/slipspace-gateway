package genaiattr_test

import (
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/genaiattr"
)

func f64(v float64) *float64 { return &v }
func i(v int) *int           { return &v }

func TestExtractRequest_OpenAIChat(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"model": "gpt-4o-mini",
		"temperature": 0.7,
		"top_p": 0.9,
		"max_completion_tokens": 256,
		"frequency_penalty": 0.1,
		"presence_penalty": 0.2,
		"stop": ["\n\n", "END"],
		"seed": 42,
		"n": 3,
		"response_format": {"type": "json_object"}
	}`)
	got := genaiattr.ExtractRequest("chat_completions", raw)

	assertF64(t, "temperature", got.Temperature, 0.7)
	assertF64(t, "top_p", got.TopP, 0.9)
	assertInt(t, "max_tokens", got.MaxTokens, 256)
	assertF64(t, "frequency_penalty", got.FrequencyPenalty, 0.1)
	assertF64(t, "presence_penalty", got.PresencePenalty, 0.2)
	assertInt(t, "seed", got.Seed, 42)
	assertInt(t, "choice_count", got.ChoiceCount, 3)
	if len(got.StopSequences) != 2 || got.StopSequences[1] != "END" {
		t.Errorf("stop = %v, want [\\n\\n END]", got.StopSequences)
	}
	if got.OutputType != "json" {
		t.Errorf("output_type = %q, want json", got.OutputType)
	}
}

func TestExtractRequest_OpenAIChat_StopString_MaxTokensAlias(t *testing.T) {
	t.Parallel()
	// Single-string stop + the deprecated max_tokens alias + text format.
	raw := []byte(`{"stop":"STOP","max_tokens":100,"response_format":{"type":"text"}}`)
	got := genaiattr.ExtractRequest("chat_completions", raw)
	if len(got.StopSequences) != 1 || got.StopSequences[0] != "STOP" {
		t.Errorf("stop = %v, want [STOP]", got.StopSequences)
	}
	assertInt(t, "max_tokens", got.MaxTokens, 100)
	if got.OutputType != "text" {
		t.Errorf("output_type = %q, want text", got.OutputType)
	}
	// Absent params stay nil.
	if got.Temperature != nil {
		t.Errorf("temperature should be nil when absent")
	}
}

func TestExtractRequest_Anthropic(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"temperature":1.0,"top_p":0.95,"top_k":40,"max_tokens":1024,"stop_sequences":["\n\nHuman:"]}`)
	got := genaiattr.ExtractRequest("messages", raw)
	assertF64(t, "temperature", got.Temperature, 1.0)
	assertF64(t, "top_p", got.TopP, 0.95)
	assertF64(t, "top_k", got.TopK, 40)
	assertInt(t, "max_tokens", got.MaxTokens, 1024)
	if len(got.StopSequences) != 1 {
		t.Errorf("stop_sequences = %v", got.StopSequences)
	}
	// Anthropic carries no n / output format here.
	if got.ChoiceCount != nil || got.OutputType != "" {
		t.Errorf("anthropic should not set choice_count/output_type")
	}
}

func TestExtractRequest_Gemini(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"generationConfig":{"temperature":0.4,"topP":0.8,"topK":32,"maxOutputTokens":512,"stopSequences":["X"],"candidateCount":2,"seed":7,"responseMimeType":"application/json"}}`)
	got := genaiattr.ExtractRequest("generate_content", raw)
	assertF64(t, "temperature", got.Temperature, 0.4)
	assertF64(t, "top_p", got.TopP, 0.8)
	assertF64(t, "top_k", got.TopK, 32)
	assertInt(t, "max_tokens", got.MaxTokens, 512)
	assertInt(t, "choice_count", got.ChoiceCount, 2)
	assertInt(t, "seed", got.Seed, 7)
	if got.OutputType != "json" {
		t.Errorf("output_type = %q, want json", got.OutputType)
	}
}

func TestExtractRequest_Gemini_NoGenerationConfig(t *testing.T) {
	t.Parallel()
	got := genaiattr.ExtractRequest("generate_content", []byte(`{"contents":[]}`))
	if got.Temperature != nil || got.MaxTokens != nil {
		t.Errorf("expected zero attrs when generationConfig absent, got %+v", got)
	}
	// text/plain mime maps to text.
	got2 := genaiattr.ExtractRequest("generate_content", []byte(`{"generationConfig":{"responseMimeType":"text/plain"}}`))
	if got2.OutputType != "text" {
		t.Errorf("output_type = %q, want text", got2.OutputType)
	}
}

func TestExtractRequest_OpenAIResponses(t *testing.T) {
	t.Parallel()
	got := genaiattr.ExtractRequest("responses", []byte(`{"temperature":0.5,"top_p":0.6,"max_output_tokens":2048}`))
	assertF64(t, "temperature", got.Temperature, 0.5)
	assertF64(t, "top_p", got.TopP, 0.6)
	assertInt(t, "max_tokens", got.MaxTokens, 2048)
}

func TestExtractRequest_OpenAIServiceTier(t *testing.T) {
	t.Parallel()
	if got := genaiattr.ExtractRequest("chat_completions", []byte(`{"service_tier":"flex"}`)); got.ServiceTier != "flex" {
		t.Errorf("chat service_tier = %q, want flex", got.ServiceTier)
	}
	if got := genaiattr.ExtractRequest("responses", []byte(`{"service_tier":"priority"}`)); got.ServiceTier != "priority" {
		t.Errorf("responses service_tier = %q, want priority", got.ServiceTier)
	}
	// Non-OpenAI endpoints carry no service tier.
	if got := genaiattr.ExtractRequest("messages", []byte(`{"service_tier":"x"}`)); got.ServiceTier != "" {
		t.Errorf("anthropic should not parse service_tier, got %q", got.ServiceTier)
	}
}

func TestExtractRequest_EdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		endpoint string
		raw      string
	}{
		{"empty body", "chat_completions", ""},
		{"unrecognised endpoint", "models", `{"temperature":1}`},
		{"malformed json", "chat_completions", `{not json`},
		{"malformed anthropic", "messages", `[]`},
		{"malformed gemini", "generate_content", `"x"`},
		{"malformed responses", "responses", `42x`},
		{"unknown response_format type", "chat_completions", `{"response_format":{"type":"weird"}}`},
		{"null stop", "chat_completions", `{"stop":null}`},
		{"object stop (invalid)", "chat_completions", `{"stop":{"a":1}}`},
		{"empty string stop", "chat_completions", `{"stop":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := genaiattr.ExtractRequest(tc.endpoint, []byte(tc.raw))
			if got.Temperature != nil || got.MaxTokens != nil || len(got.StopSequences) != 0 || got.OutputType != "" {
				t.Errorf("expected zero RequestAttrs, got %+v", got)
			}
		})
	}
}

func assertF64(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func assertInt(t *testing.T, name string, got *int, want int) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}
