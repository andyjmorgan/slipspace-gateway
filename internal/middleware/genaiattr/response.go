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
	// Retained for text-only consumers; OutputParts is the full structured
	// form including tool calls.
	OutputText string

	// OutputParts is the model's response as ordered message parts: the
	// reassembled text (if any) followed by tool_call parts. Works for both
	// streaming and non-streaming since it reads the same collated frames.
	// On a finish_reason=tool_use turn the text part is absent and only the
	// tool_call parts remain — so tool-only responses are no longer dropped.
	OutputParts []Part

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

// toolAcc accumulates one tool call across streamed fragments (id and name
// arrive once, arguments stream in pieces).
type toolAcc struct {
	id   string
	name string
	args strings.Builder
}

// toolAccList is an insertion-ordered set of tool-call accumulators keyed by
// the provider's per-call index, so fragments reassemble into the original
// call order regardless of frame interleaving.
type toolAccList struct {
	order []int
	byIdx map[int]*toolAcc
}

func (l *toolAccList) get(idx int) *toolAcc {
	if l.byIdx == nil {
		l.byIdx = map[int]*toolAcc{}
	}
	a, ok := l.byIdx[idx]
	if !ok {
		a = &toolAcc{}
		l.byIdx[idx] = a
		l.order = append(l.order, idx)
	}
	return a
}

func (l *toolAccList) parts() []Part {
	var parts []Part
	for _, idx := range l.order {
		a := l.byIdx[idx]
		if a.name == "" && a.args.Len() == 0 && a.id == "" {
			continue
		}
		parts = append(parts, Part{
			Type:      "tool_call",
			ID:        a.id,
			Name:      a.name,
			Arguments: argsRaw(a.args.String()),
		})
	}
	return parts
}

// argsRaw normalises an accumulated tool-arguments string to raw JSON: the
// fully-reassembled fragments are valid JSON; a still-partial or non-JSON
// string is wrapped as a JSON string so the value stays well-formed.
func argsRaw(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	return json.RawMessage(b)
}

// outputParts assembles the ordered output parts: the reassembled text first
// (when non-empty), then the tool calls in call order.
func outputParts(text string, tools []Part) []Part {
	var parts []Part
	if text != "" {
		parts = append(parts, Part{Type: "text", Content: text})
	}
	return append(parts, tools...)
}

func extractOpenAIChatResponse(frames [][]byte) ResponseAttrs {
	var a ResponseAttrs
	var out strings.Builder
	var tools toolAccList
	for _, f := range frames {
		var ch struct {
			ID                string `json:"id"`
			Model             string `json:"model"`
			ServiceTier       string `json:"service_tier"`
			SystemFingerprint string `json:"system_fingerprint"`
			Choices           []struct {
				FinishReason *string `json:"finish_reason"`
				Delta        struct {
					Content   json.RawMessage  `json:"content"`
					ToolCalls []openAIToolCall `json:"tool_calls"`
				} `json:"delta"`
				Message struct {
					Content   json.RawMessage  `json:"content"`
					ToolCalls []openAIToolCall `json:"tool_calls"`
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
			// Streamed tool calls carry an explicit index; arguments stream
			// in fragments. Non-streaming tool calls arrive whole, indexed by
			// position. A response is one shape or the other, so the index
			// spaces never collide.
			for _, tc := range choice.Delta.ToolCalls {
				acc := tools.get(tc.Index)
				acc.merge(tc)
			}
			for i, tc := range choice.Message.ToolCalls {
				acc := tools.get(i)
				acc.merge(tc)
			}
		}
		if ch.Usage != nil && ch.Usage.CompletionTokensDetails != nil && ch.Usage.CompletionTokensDetails.ReasoningTokens > 0 {
			n := ch.Usage.CompletionTokensDetails.ReasoningTokens
			a.ReasoningTokens = &n
		}
	}
	a.OutputText = out.String()
	a.OutputParts = outputParts(a.OutputText, tools.parts())
	return a
}

// openAIToolCall is one entry of choices[].{delta,message}.tool_calls — the
// streamed delta form carries an index and fragmented function.arguments;
// the non-streaming form carries the whole call.
type openAIToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (a *toolAcc) merge(tc openAIToolCall) {
	if tc.ID != "" {
		a.id = tc.ID
	}
	if tc.Function.Name != "" {
		a.name = tc.Function.Name
	}
	a.args.WriteString(tc.Function.Arguments)
}

// responsesOutput is one Responses-API output item: an assistant message
// (content parts with type "output_text") or a function_call item.
type responsesOutput struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	CallID    string `json:"call_id"`
	Content   []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func extractOpenAIResponsesResponse(frames [][]byte) ResponseAttrs {
	var a ResponseAttrs
	var out strings.Builder
	var tools toolAccList
	// A function call is keyed by its stable item id — the same id the
	// streaming function_call_arguments.delta events reference — so the header
	// (added/output item) and the fragmented arguments reassemble together.
	// Part.ID carries the tool-call call_id the model uses in the transcript.
	itemIdx := map[string]int{}
	next := 0
	accFor := func(itemID string) *toolAcc {
		idx, ok := itemIdx[itemID]
		if !ok {
			idx = next
			next++
			itemIdx[itemID] = idx
		}
		return tools.get(idx)
	}
	writeOutputs := func(items []responsesOutput) {
		for _, o := range items {
			if o.Type == "function_call" {
				acc := accFor(firstNonEmpty(o.ID, o.CallID))
				if o.Name != "" {
					acc.name = o.Name
				}
				if id := firstNonEmpty(o.CallID, o.ID); id != "" {
					acc.id = id
				}
				acc.args.WriteString(o.Arguments)
				continue
			}
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
			ItemID      string            `json:"item_id"`
			Output      []responsesOutput `json:"output"`
			Item        *responsesOutput  `json:"item"`
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
		switch ch.Type {
		case "response.output_text.delta":
			out.WriteString(ch.Delta)
		case "response.function_call_arguments.delta":
			accFor(ch.ItemID).args.WriteString(ch.Delta)
		case "response.output_item.added", "response.output_item.done":
			if ch.Item != nil && ch.Item.Type == "function_call" {
				acc := accFor(firstNonEmpty(ch.Item.ID, ch.ItemID))
				if ch.Item.Name != "" {
					acc.name = ch.Item.Name
				}
				if id := firstNonEmpty(ch.Item.CallID, ch.Item.ID); id != "" {
					acc.id = id
				}
			}
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
	a.OutputParts = outputParts(a.OutputText, tools.parts())
	return a
}

func extractAnthropicResponse(frames [][]byte) ResponseAttrs {
	var a ResponseAttrs
	var out strings.Builder
	var tools toolAccList
	for _, f := range frames {
		var ch struct {
			ID         string  `json:"id"`
			Model      string  `json:"model"`
			Type       string  `json:"type"`
			Index      int     `json:"index"`
			StopReason *string `json:"stop_reason"`
			// Content is the non-streaming top-level content block array.
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
			// ContentBlock carries the tool_use header on content_block_start.
			ContentBlock *struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Message *struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"message"`
			// Delta carries stop_reason on message_delta, text on
			// content_block_delta (text_delta), and tool-argument fragments on
			// input_json_delta — one struct decodes all three.
			Delta *struct {
				Type        string  `json:"type"`
				StopReason  *string `json:"stop_reason"`
				Text        string  `json:"text"`
				PartialJSON string  `json:"partial_json"`
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
		// Non-streaming: text + tool_use blocks arrive whole, indexed by
		// position.
		for i, blk := range ch.Content {
			switch blk.Type {
			case "text":
				out.WriteString(blk.Text)
			case "tool_use":
				acc := tools.get(i)
				acc.id = blk.ID
				acc.name = blk.Name
				acc.args.WriteString(string(nonEmptyRaw(blk.Input)))
			}
		}
		// Streaming: a tool_use header on content_block_start, then argument
		// fragments on input_json_delta, keyed by the block index.
		if ch.Type == "content_block_start" && ch.ContentBlock != nil && ch.ContentBlock.Type == "tool_use" {
			acc := tools.get(ch.Index)
			acc.id = ch.ContentBlock.ID
			acc.name = ch.ContentBlock.Name
		}
		if ch.Delta != nil {
			if ch.Delta.StopReason != nil && *ch.Delta.StopReason != "" {
				a.FinishReasons = appendUnique(a.FinishReasons, *ch.Delta.StopReason)
			}
			out.WriteString(ch.Delta.Text)
			if ch.Delta.PartialJSON != "" {
				tools.get(ch.Index).args.WriteString(ch.Delta.PartialJSON)
			}
		}
	}
	a.OutputText = out.String()
	a.OutputParts = outputParts(a.OutputText, tools.parts())
	return a
}

func extractGeminiResponse(frames [][]byte) ResponseAttrs {
	var a ResponseAttrs
	var out strings.Builder
	var toolParts []Part
	for _, f := range frames {
		var ch struct {
			ResponseID   string `json:"responseId"`
			ModelVersion string `json:"modelVersion"`
			Candidates   []struct {
				FinishReason string `json:"finishReason"`
				Content      struct {
					Parts []struct {
						Text         string `json:"text"`
						FunctionCall *struct {
							Name string          `json:"name"`
							Args json.RawMessage `json:"args"`
						} `json:"functionCall"`
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
				if p.FunctionCall != nil {
					toolParts = append(toolParts, Part{
						Type:      "tool_call",
						Name:      p.FunctionCall.Name,
						Arguments: nonEmptyRaw(p.FunctionCall.Args),
					})
					continue
				}
				out.WriteString(p.Text)
			}
		}
		if ch.UsageMetadata != nil && ch.UsageMetadata.ThoughtsTokenCount > 0 {
			n := ch.UsageMetadata.ThoughtsTokenCount
			a.ReasoningTokens = &n
		}
	}
	a.OutputText = out.String()
	a.OutputParts = outputParts(a.OutputText, toolParts)
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
