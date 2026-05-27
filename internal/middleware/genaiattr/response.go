package genaiattr

import (
	"encoding/json"
	"strings"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/sseframe"
)

// ResponseAttrs holds the GenAI response descriptors the spans convention
// defines as Recommended span attributes. Empty/nil fields are omitted by
// the caller. FinishReasons preserves first-seen order across choices and
// streamed chunks, deduplicated.
type ResponseAttrs struct {
	ID              string
	Model           string
	FinishReasons   []string
	ReasoningTokens *int

	// OutputText is the model's response text, reassembled from the frames
	// (concatenated streaming deltas, or the single non-streaming body).
	// Used as the bounded output content on the operation-details event;
	// works for both streaming and non-streaming since it reads the same
	// collated frames as the other descriptors.
	OutputText string

	// ServiceTier and SystemFingerprint are OpenAI-specific response
	// descriptors (openai.response.service_tier / system_fingerprint).
	// Empty for non-OpenAI responses.
	ServiceTier       string
	SystemFingerprint string
}

type responseExtractorFn func(frames [][]byte) ResponseAttrs

var responseRegistry = map[string]responseExtractorFn{
	"chat_completions": extractOpenAIChatResponse,
	"responses":        extractOpenAIResponsesResponse,
	"messages":         extractAnthropicResponse,
	"generate_content": extractGeminiResponse,
}

// ExtractResponse parses the response body for the named endpoint and
// returns the GenAI response descriptors it carries. The body may be a
// non-streaming JSON object or an SSE stream; it is collated once via
// sseframe. Prefer ExtractResponseFrames when the caller already holds
// collated frames (so the body is split only once per request).
func ExtractResponse(endpoint string, raw []byte) ResponseAttrs {
	return ExtractResponseFrames(endpoint, sseframe.Collate(raw))
}

// ExtractResponseFrames extracts the response descriptors from frames
// already collated by sseframe.Collate. An unrecognised endpoint or empty
// frames yield a zero ResponseAttrs.
func ExtractResponseFrames(endpoint string, frames [][]byte) ResponseAttrs {
	if len(frames) == 0 {
		return ResponseAttrs{}
	}
	fn, ok := responseRegistry[endpoint]
	if !ok {
		return ResponseAttrs{}
	}
	return fn(frames)
}

func extractOpenAIChatResponse(frames [][]byte) ResponseAttrs {
	var a ResponseAttrs
	var out strings.Builder
	for _, f := range frames {
		var ch struct {
			ID                string `json:"id"`
			Model             string `json:"model"`
			ServiceTier       string `json:"service_tier"`
			SystemFingerprint string `json:"system_fingerprint"`
			Choices           []struct {
				FinishReason *string `json:"finish_reason"`
				Delta        struct {
					Content json.RawMessage `json:"content"`
				} `json:"delta"`
				Message struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Usage *struct {
				CompletionTokensDetails *struct {
					ReasoningTokens int `json:"reasoning_tokens"`
				} `json:"completion_tokens_details"`
			} `json:"usage"`
		}
		if json.Unmarshal(f, &ch) != nil {
			continue
		}
		a.ID = firstNonEmpty(a.ID, ch.ID)
		a.Model = firstNonEmpty(a.Model, ch.Model)
		a.ServiceTier = firstNonEmpty(a.ServiceTier, ch.ServiceTier)
		a.SystemFingerprint = firstNonEmpty(a.SystemFingerprint, ch.SystemFingerprint)
		for _, choice := range ch.Choices {
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				a.FinishReasons = appendUnique(a.FinishReasons, *choice.FinishReason)
			}
			// Streaming carries delta.content per chunk; non-streaming
			// carries the whole message.content — one is empty, so writing
			// both reassembles either shape.
			out.WriteString(textFromContent(choice.Delta.Content))
			out.WriteString(textFromContent(choice.Message.Content))
		}
		if ch.Usage != nil && ch.Usage.CompletionTokensDetails != nil && ch.Usage.CompletionTokensDetails.ReasoningTokens > 0 {
			n := ch.Usage.CompletionTokensDetails.ReasoningTokens
			a.ReasoningTokens = &n
		}
	}
	a.OutputText = out.String()
	return a
}

// responsesOutput is one Responses-API output item; its content parts
// carry the assistant text (type "output_text").
type responsesOutput struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func extractOpenAIResponsesResponse(frames [][]byte) ResponseAttrs {
	var a ResponseAttrs
	var out strings.Builder
	writeOutputs := func(items []responsesOutput) {
		for _, o := range items {
			for _, c := range o.Content {
				out.WriteString(c.Text)
			}
		}
	}
	for _, f := range frames {
		var ch struct {
			ID          string            `json:"id"`
			Model       string            `json:"model"`
			ServiceTier string            `json:"service_tier"`
			Type        string            `json:"type"`
			Delta       string            `json:"delta"`
			Output      []responsesOutput `json:"output"`
			Response    *struct {
				ID          string            `json:"id"`
				Model       string            `json:"model"`
				ServiceTier string            `json:"service_tier"`
				Output      []responsesOutput `json:"output"`
			} `json:"response"`
		}
		if json.Unmarshal(f, &ch) != nil {
			continue
		}
		a.ID = firstNonEmpty(a.ID, ch.ID)
		a.Model = firstNonEmpty(a.Model, ch.Model)
		a.ServiceTier = firstNonEmpty(a.ServiceTier, ch.ServiceTier)
		// Streaming text arrives on response.output_text.delta events.
		if ch.Type == "response.output_text.delta" {
			out.WriteString(ch.Delta)
		}
		writeOutputs(ch.Output)
		if ch.Response != nil {
			a.ID = firstNonEmpty(a.ID, ch.Response.ID)
			a.Model = firstNonEmpty(a.Model, ch.Response.Model)
			a.ServiceTier = firstNonEmpty(a.ServiceTier, ch.Response.ServiceTier)
			writeOutputs(ch.Response.Output)
		}
	}
	a.OutputText = out.String()
	return a
}

func extractAnthropicResponse(frames [][]byte) ResponseAttrs {
	var a ResponseAttrs
	var out strings.Builder
	for _, f := range frames {
		var ch struct {
			ID         string  `json:"id"`
			Model      string  `json:"model"`
			StopReason *string `json:"stop_reason"`
			// Content is the non-streaming top-level content block array.
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Message *struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"message"`
			// Delta carries stop_reason on message_delta and text on
			// content_block_delta (text_delta) — one struct decodes both.
			Delta *struct {
				StopReason *string `json:"stop_reason"`
				Text       string  `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal(f, &ch) != nil {
			continue
		}
		a.ID = firstNonEmpty(a.ID, ch.ID)
		a.Model = firstNonEmpty(a.Model, ch.Model)
		if ch.Message != nil {
			a.ID = firstNonEmpty(a.ID, ch.Message.ID)
			a.Model = firstNonEmpty(a.Model, ch.Message.Model)
		}
		if ch.StopReason != nil && *ch.StopReason != "" {
			a.FinishReasons = appendUnique(a.FinishReasons, *ch.StopReason)
		}
		for _, blk := range ch.Content {
			if blk.Type == "text" {
				out.WriteString(blk.Text)
			}
		}
		if ch.Delta != nil {
			if ch.Delta.StopReason != nil && *ch.Delta.StopReason != "" {
				a.FinishReasons = appendUnique(a.FinishReasons, *ch.Delta.StopReason)
			}
			out.WriteString(ch.Delta.Text)
		}
	}
	a.OutputText = out.String()
	return a
}

func extractGeminiResponse(frames [][]byte) ResponseAttrs {
	var a ResponseAttrs
	var out strings.Builder
	for _, f := range frames {
		var ch struct {
			ResponseID   string `json:"responseId"`
			ModelVersion string `json:"modelVersion"`
			Candidates   []struct {
				FinishReason string `json:"finishReason"`
				Content      struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
			UsageMetadata *struct {
				ThoughtsTokenCount int `json:"thoughtsTokenCount"`
			} `json:"usageMetadata"`
		}
		if json.Unmarshal(f, &ch) != nil {
			continue
		}
		a.ID = firstNonEmpty(a.ID, ch.ResponseID)
		a.Model = firstNonEmpty(a.Model, ch.ModelVersion)
		for _, cand := range ch.Candidates {
			if cand.FinishReason != "" {
				a.FinishReasons = appendUnique(a.FinishReasons, cand.FinishReason)
			}
			for _, p := range cand.Content.Parts {
				out.WriteString(p.Text)
			}
		}
		if ch.UsageMetadata != nil && ch.UsageMetadata.ThoughtsTokenCount > 0 {
			n := ch.UsageMetadata.ThoughtsTokenCount
			a.ReasoningTokens = &n
		}
	}
	a.OutputText = out.String()
	return a
}

func firstNonEmpty(have, candidate string) string {
	if have != "" {
		return have
	}
	return candidate
}

func appendUnique(list []string, v string) []string {
	for _, e := range list {
		if e == v {
			return list
		}
	}
	return append(list, v)
}
