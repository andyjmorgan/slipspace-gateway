package genaiattr

import "encoding/json"

// RequestAttrs holds the GenAI request parameters the spans convention
// defines as Conditionally Required or Recommended span attributes. Scalar
// fields are pointers so an absent parameter (nil) is distinguishable from
// a present zero value; only fields actually carried in the request body
// are populated.
type RequestAttrs struct {
	// Temperature, TopP, TopK, MaxTokens, FrequencyPenalty, PresencePenalty
	// are the Recommended sampling parameters. TopK is a double per the spec
	// even though providers send an integer.
	Temperature      *float64
	TopP             *float64
	TopK             *float64
	MaxTokens        *int
	FrequencyPenalty *float64
	PresencePenalty  *float64

	// StopSequences is the Recommended gen_ai.request.stop_sequences.
	StopSequences []string

	// Seed is Conditionally Required (if the request includes a seed).
	Seed *int

	// ChoiceCount is Conditionally Required when present and != 1
	// (OpenAI "n", Gemini "candidateCount").
	ChoiceCount *int

	// OutputType is the Conditionally Required gen_ai.output.type, normalized
	// to the spec enum (text|json|image|speech). Empty when the request did
	// not request a specific output format.
	OutputType string

	// ServiceTier is the OpenAI-specific openai.request.service_tier, when
	// the request carried one. Empty for non-OpenAI requests.
	ServiceTier string
}

type requestExtractorFn func(raw []byte) RequestAttrs

// requestRegistry keys extractors by endpoint name, matching the tokens
// and livefeed/accumulator registries so a new provider extends all three.
var requestRegistry = map[string]requestExtractorFn{
	"chat_completions": extractOpenAIChatRequest,
	"chat":             extractOpenAIChatRequest,
	"responses":        extractOpenAIResponsesRequest,
	"messages":         extractAnthropicRequest,
	"generate_content": extractGeminiRequest,
}

// ExtractRequest parses the request body for the named endpoint and returns
// the GenAI request attributes it carries. An unrecognised endpoint or an
// unparseable body yields a zero RequestAttrs (all pointers nil), so the
// caller emits nothing rather than wrong values.
func ExtractRequest(endpoint string, raw []byte) RequestAttrs {
	if len(raw) == 0 {
		return RequestAttrs{}
	}
	fn, ok := requestRegistry[endpoint]
	if !ok {
		return RequestAttrs{}
	}
	return fn(raw)
}

func extractOpenAIChatRequest(raw []byte) RequestAttrs {
	var body struct {
		Temperature      *float64        `json:"temperature"`
		TopP             *float64        `json:"top_p"`
		MaxTokens        *int            `json:"max_tokens"`
		MaxCompletion    *int            `json:"max_completion_tokens"`
		FrequencyPenalty *float64        `json:"frequency_penalty"`
		PresencePenalty  *float64        `json:"presence_penalty"`
		Stop             json.RawMessage `json:"stop"`
		Seed             *int            `json:"seed"`
		N                *int            `json:"n"`
		ServiceTier      string          `json:"service_tier"`
		ResponseFormat   *struct {
			Type string `json:"type"`
		} `json:"response_format"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return RequestAttrs{}
	}
	a := RequestAttrs{
		Temperature:      body.Temperature,
		TopP:             body.TopP,
		FrequencyPenalty: body.FrequencyPenalty,
		PresencePenalty:  body.PresencePenalty,
		Seed:             body.Seed,
		ChoiceCount:      body.N,
		StopSequences:    parseStop(body.Stop),
		ServiceTier:      body.ServiceTier,
	}
	// max_completion_tokens is the current field; max_tokens is the
	// deprecated alias. Prefer the explicit current name.
	if body.MaxCompletion != nil {
		a.MaxTokens = body.MaxCompletion
	} else if body.MaxTokens != nil {
		a.MaxTokens = body.MaxTokens
	}
	if body.ResponseFormat != nil {
		a.OutputType = openAIOutputType(body.ResponseFormat.Type)
	}
	return a
}

func extractOpenAIResponsesRequest(raw []byte) RequestAttrs {
	var body struct {
		Temperature     *float64 `json:"temperature"`
		TopP            *float64 `json:"top_p"`
		MaxOutputTokens *int     `json:"max_output_tokens"`
		ServiceTier     string   `json:"service_tier"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return RequestAttrs{}
	}
	return RequestAttrs{
		Temperature: body.Temperature,
		TopP:        body.TopP,
		MaxTokens:   body.MaxOutputTokens,
		ServiceTier: body.ServiceTier,
	}
}

func extractAnthropicRequest(raw []byte) RequestAttrs {
	var body struct {
		Temperature   *float64 `json:"temperature"`
		TopP          *float64 `json:"top_p"`
		TopK          *float64 `json:"top_k"`
		MaxTokens     *int     `json:"max_tokens"`
		StopSequences []string `json:"stop_sequences"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return RequestAttrs{}
	}
	return RequestAttrs{
		Temperature:   body.Temperature,
		TopP:          body.TopP,
		TopK:          body.TopK,
		MaxTokens:     body.MaxTokens,
		StopSequences: body.StopSequences,
	}
}

func extractGeminiRequest(raw []byte) RequestAttrs {
	var body struct {
		GenerationConfig *struct {
			Temperature      *float64 `json:"temperature"`
			TopP             *float64 `json:"topP"`
			TopK             *float64 `json:"topK"`
			MaxOutputTokens  *int     `json:"maxOutputTokens"`
			StopSequences    []string `json:"stopSequences"`
			CandidateCount   *int     `json:"candidateCount"`
			Seed             *int     `json:"seed"`
			ResponseMimeType string   `json:"responseMimeType"`
		} `json:"generationConfig"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return RequestAttrs{}
	}
	g := body.GenerationConfig
	if g == nil {
		return RequestAttrs{}
	}
	return RequestAttrs{
		Temperature:   g.Temperature,
		TopP:          g.TopP,
		TopK:          g.TopK,
		MaxTokens:     g.MaxOutputTokens,
		StopSequences: g.StopSequences,
		ChoiceCount:   g.CandidateCount,
		Seed:          g.Seed,
		OutputType:    geminiOutputType(g.ResponseMimeType),
	}
}

// parseStop normalizes OpenAI's stop field, which may be a single string,
// an array of strings, or absent/null, into a slice.
func parseStop(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		if one == "" {
			return nil
		}
		return []string{one}
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many
	}
	return nil
}

func openAIOutputType(t string) string {
	switch t {
	case "json_object", "json_schema":
		return "json"
	case "text":
		return "text"
	default:
		return ""
	}
}

func geminiOutputType(mime string) string {
	switch mime {
	case "application/json":
		return "json"
	case "text/plain":
		return "text"
	default:
		return ""
	}
}
