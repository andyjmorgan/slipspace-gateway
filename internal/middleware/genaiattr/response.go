package genaiattr

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/sseframe"
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
	"chat":             extractOpenAIChatResponse,
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

	// argsOverride, when set, replaces the fragment-accumulated args.
	// Server-tool output items carry their argument fields whole on every
	// appearance (output_item.added, .done, the final response.output), so
	// assignment — not append — keeps the repeated emissions idempotent.
	argsOverride json.RawMessage

	// result, when non-empty, emits a paired tool_call_response part right
	// after the tool_call — Responses server-tool items carry call and
	// (optional, include-gated) result on the same item.
	result string

	// server marks a provider-hosted tool item (web_search_call,
	// file_search_call, code_interpreter_call, mcp_call, …) the upstream
	// executes inline, as opposed to a client-executed function_call. Set by
	// mergeServerItem; drives the emitted part's Executor.
	server bool
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
		if a.name == "" && a.args.Len() == 0 && a.id == "" && len(a.argsOverride) == 0 {
			continue
		}
		args := a.argsOverride
		if args == nil {
			args = argsRaw(a.args.String())
		}
		// function_call (a.server false) is client-executed; server-tool items
		// run inline upstream.
		exec := "client"
		if a.server {
			exec = "server"
		}
		parts = append(parts, Part{
			Type:      "tool_call",
			ID:        a.id,
			Name:      a.name,
			Arguments: args,
			Executor:  exec,
		})
		if a.result != "" {
			parts = append(parts, Part{Type: "tool_call_response", ID: a.id, Result: a.result, Executor: exec})
		}
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

// bulkyResultKeys are the content-payload carriers stripped from server-tool
// result JSON before it rides telemetry: encrypted_content (opaque multi-KB
// citation blobs on web_search results) and data (document / base64 bodies on
// web_fetch results). Telemetry stays bounded — the full payload rides the
// connector Record, never the span (provider bodies are never logged in full).
var bulkyResultKeys = map[string]bool{"encrypted_content": true, "data": true}

// compactToolResult renders a server-tool result payload as a bounded result
// string: a plain JSON string passes through as text; structured content is
// compact-marshalled after recursively stripping the bulky payload carriers in
// bulkyResultKeys. Empty / null content yields "".
func compactToolResult(raw json.RawMessage) string {
	if len(nonEmptyRaw(raw)) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	stripBulkyKeys(v)
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// stripBulkyKeys removes every bulkyResultKeys entry from v in place,
// recursing through nested objects and arrays.
func stripBulkyKeys(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if bulkyResultKeys[k] {
				delete(t, k)
				continue
			}
			stripBulkyKeys(val)
		}
	case []any:
		for _, e := range t {
			stripBulkyKeys(e)
		}
	}
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
// (content parts with type "output_text"), a function_call item, or a
// server-tool item (web_search_call, file_search_call, code_interpreter_call,
// computer_call, mcp_call, ...). The fields past Content are the server-tool
// argument and result carriers — which are populated depends on the item type
// (action on web_search/computer/local_shell, queries+results on file_search,
// code+container_id+outputs on code_interpreter, arguments+output+server_label
// on mcp_call).
type responsesOutput struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	CallID    string `json:"call_id"`
	Content   []struct {
		Text string `json:"text"`
	} `json:"content"`
	Status      string          `json:"status"`
	Action      json.RawMessage `json:"action"`
	Queries     json.RawMessage `json:"queries"`
	Code        string          `json:"code"`
	ContainerID string          `json:"container_id"`
	Input       json.RawMessage `json:"input"`
	ServerLabel string          `json:"server_label"`
	Results     json.RawMessage `json:"results"`
	Outputs     json.RawMessage `json:"outputs"`
	Output      json.RawMessage `json:"output"`
	Error       string          `json:"error"`
}

// responsesServerToolName maps a Responses output-item type to the gen_ai
// tool_call part name for items that represent provider-side tool activity:
// any "*_call" item (web_search_call → web_search, code_interpreter_call →
// code_interpreter, future built-ins included) plus the suffix-less MCP
// catalogue/approval items, which keep their full type as the name.
// function_call is the client-executed path and message/reasoning are not
// tool activity — all return ok=false, as do the *_output input-item types
// (function_call_output, computer_call_output) so a confused echo never
// mints a phantom call.
func responsesServerToolName(itemType string) (string, bool) {
	switch itemType {
	case "", "function_call", "message", "reasoning", "function_call_output", "computer_call_output":
		return "", false
	case "mcp_list_tools", "mcp_approval_request", "mcp_approval_response":
		return itemType, true
	}
	if name, ok := strings.CutSuffix(itemType, "_call"); ok {
		return name, true
	}
	return "", false
}

// responsesServerToolArgs is the best-effort argument projection of a
// server-tool item: whichever argument-bearing fields the item carries, keyed
// by their wire names, compact-marshalled. Result payloads (results, outputs,
// output) are deliberately excluded — they ride the paired
// tool_call_response part instead.
type responsesServerToolArgs struct {
	Name        string          `json:"name,omitempty"`
	Arguments   json.RawMessage `json:"arguments,omitempty"`
	Action      json.RawMessage `json:"action,omitempty"`
	Queries     json.RawMessage `json:"queries,omitempty"`
	Code        string          `json:"code,omitempty"`
	ContainerID string          `json:"container_id,omitempty"`
	Input       json.RawMessage `json:"input,omitempty"`
	ServerLabel string          `json:"server_label,omitempty"`
}

// mergeServerItem folds one appearance of a server-tool output item into the
// accumulator. Items repeat (output_item.added, .done, the final
// response.output), each time carrying the whole item, so every field is
// assigned — never appended — and only when the new appearance has it.
func (a *toolAcc) mergeServerItem(o responsesOutput, name string) {
	a.name = name
	a.server = true
	if id := firstNonEmpty(o.CallID, o.ID); id != "" {
		a.id = id
	}
	args := responsesServerToolArgs{
		Action:      nonEmptyRaw(o.Action),
		Queries:     nonEmptyRaw(o.Queries),
		Code:        o.Code,
		ContainerID: o.ContainerID,
		Input:       nonEmptyRaw(o.Input),
		ServerLabel: o.ServerLabel,
	}
	// mcp_call carries the MCP tool's own name and a JSON-encoded arguments
	// string; both ride inside the argument object (the part name stays the
	// item-type-derived family name).
	if o.Name != "" {
		args.Name = o.Name
	}
	if o.Arguments != "" {
		args.Arguments = argsRaw(o.Arguments)
	}
	hasArgs := len(args.Action)+len(args.Queries)+len(args.Input)+len(args.Arguments) > 0 ||
		args.Name != "" || args.Code != "" || args.ContainerID != "" || args.ServerLabel != ""
	if hasArgs {
		if b, err := json.Marshal(args); err == nil {
			a.argsOverride = json.RawMessage(b)
		}
	}
	if res := o.serverToolResult(); res != "" {
		a.result = res
	}
}

// serverToolResult renders the item's inline result, when it carries one:
// results (file_search / web_search behind include), outputs
// (code_interpreter behind include), output (mcp_call), an mcp_call error
// string, or — absent all of those — a {"status":...} marker on a
// failed/incomplete call so the failure stays visible in telemetry.
func (o responsesOutput) serverToolResult() string {
	switch {
	case len(nonEmptyRaw(o.Results)) > 0:
		return compactToolResult(o.Results)
	case len(nonEmptyRaw(o.Outputs)) > 0:
		return compactToolResult(o.Outputs)
	case len(nonEmptyRaw(o.Output)) > 0:
		return compactToolResult(o.Output)
	case o.Error != "":
		return marshalString(struct {
			Error string `json:"error"`
		}{o.Error})
	case o.Status == "failed" || o.Status == "incomplete":
		return marshalString(struct {
			Status string `json:"status"`
		}{o.Status})
	}
	return ""
}

// marshalString compact-marshals v, returning "" on the (practically
// impossible for plain structs) marshal error.
func marshalString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
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
			// Server-tool items (web_search_call, file_search_call, ...)
			// become tool_call parts; without this branch they would fall
			// through to text extraction and contribute nothing.
			if name, ok := responsesServerToolName(o.Type); ok {
				accFor(firstNonEmpty(o.ID, o.CallID)).mergeServerItem(o, name)
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
			} else if ch.Item != nil {
				// Streamed server-tool items arrive whole on the added/done
				// events (arguments don't fragment like function_call's);
				// merging both keeps the richer .done fields and stays
				// idempotent when response.completed repeats the item.
				if name, ok := responsesServerToolName(ch.Item.Type); ok {
					accFor(firstNonEmpty(ch.Item.ID, ch.ItemID)).mergeServerItem(*ch.Item, name)
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
	blocks := anthropicBlockList{byIdx: map[int]*anthropicBlock{}}
	for _, f := range frames {
		var ch struct {
			ID         string  `json:"id"`
			Model      string  `json:"model"`
			Type       string  `json:"type"`
			Index      int     `json:"index"`
			StopReason *string `json:"stop_reason"`
			// Content is the non-streaming top-level content block array.
			// ToolUseID + InnerContent carry the *_tool_result correlation id
			// and payload (server-side tool results: web_search_tool_result,
			// mcp_tool_result, ...).
			Content []struct {
				Type         string          `json:"type"`
				Text         string          `json:"text"`
				ID           string          `json:"id"`
				Name         string          `json:"name"`
				Input        json.RawMessage `json:"input"`
				Thinking     string          `json:"thinking"`
				ToolUseID    string          `json:"tool_use_id"`
				InnerContent json.RawMessage `json:"content"`
			} `json:"content"`
			// ContentBlock carries the tool_use / server_tool_use / thinking
			// header on content_block_start. Server-side *_tool_result blocks
			// arrive whole inside content_block_start (no deltas follow), so
			// it also carries their tool_use_id and content payload.
			ContentBlock *struct {
				Type         string          `json:"type"`
				ID           string          `json:"id"`
				Name         string          `json:"name"`
				Thinking     string          `json:"thinking"`
				ToolUseID    string          `json:"tool_use_id"`
				InnerContent json.RawMessage `json:"content"`
			} `json:"content_block"`
			Message *struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"message"`
			// Delta carries stop_reason on message_delta, text on
			// content_block_delta (text_delta), thinking on thinking_delta,
			// and tool-argument fragments on input_json_delta — one struct
			// decodes them all.
			Delta *struct {
				Type        string  `json:"type"`
				StopReason  *string `json:"stop_reason"`
				Text        string  `json:"text"`
				PartialJSON string  `json:"partial_json"`
				Thinking    string  `json:"thinking"`
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
		// Non-streaming: thinking + text + tool blocks arrive whole, indexed
		// by array position. Server-executed calls (server_tool_use,
		// mcp_tool_use) share the tool_use shape; any *_tool_result block
		// carries the paired result.
		for i, blk := range ch.Content {
			b := blocks.get(i)
			b.setKind(blk.Type)
			switch blk.Type {
			case "text":
				b.text.WriteString(blk.Text)
			case "tool_use", "server_tool_use", "mcp_tool_use":
				b.toolID = blk.ID
				b.toolName = blk.Name
				b.toolArgs.WriteString(string(nonEmptyRaw(blk.Input)))
			case "thinking":
				b.thinking.WriteString(blk.Thinking)
			default:
				if strings.HasSuffix(blk.Type, "_tool_result") {
					b.toolID = blk.ToolUseID
					b.result = compactToolResult(blk.InnerContent)
				}
			}
		}
		// Streaming: each block opens with a content_block_start carrying its
		// kind, then accumulates on the matching delta, keyed by block index.
		// Thinking and text can interleave (think, text, think again), so each
		// index keeps its own ordered slot. server_tool_use / mcp_tool_use
		// stream exactly like tool_use (header here, input_json_delta after);
		// *_tool_result blocks arrive whole in this event with no deltas, so
		// the result is captured here.
		if ch.Type == "content_block_start" && ch.ContentBlock != nil {
			b := blocks.get(ch.Index)
			b.setKind(ch.ContentBlock.Type)
			switch ch.ContentBlock.Type {
			case "tool_use", "server_tool_use", "mcp_tool_use":
				b.toolID = ch.ContentBlock.ID
				b.toolName = ch.ContentBlock.Name
			case "thinking":
				b.thinking.WriteString(ch.ContentBlock.Thinking)
			default:
				if strings.HasSuffix(ch.ContentBlock.Type, "_tool_result") {
					b.toolID = ch.ContentBlock.ToolUseID
					b.result = compactToolResult(ch.ContentBlock.InnerContent)
				}
			}
		}
		if ch.Delta != nil {
			if ch.Delta.StopReason != nil && *ch.Delta.StopReason != "" {
				a.FinishReasons = appendUnique(a.FinishReasons, *ch.Delta.StopReason)
			}
			if ch.Delta.Text != "" {
				b := blocks.get(ch.Index)
				b.setKind("text")
				b.text.WriteString(ch.Delta.Text)
			}
			if ch.Delta.Thinking != "" {
				b := blocks.get(ch.Index)
				b.setKind("thinking")
				b.thinking.WriteString(ch.Delta.Thinking)
			}
			if ch.Delta.PartialJSON != "" {
				b := blocks.get(ch.Index)
				b.setKind("tool_use")
				b.toolArgs.WriteString(ch.Delta.PartialJSON)
			}
		}
	}
	a.OutputText = blocks.text()
	a.OutputParts = blocks.parts()
	return a
}

// anthropicBlock is the in-flight reconstruction of one Anthropic response
// content block. kind is fixed on first sight; only the buffers matching kind
// are populated. The tool fields serve both client tool calls (tool_use) and
// server-executed calls (server_tool_use, mcp_tool_use); result carries the
// compacted payload of a *_tool_result block (toolID then holds the block's
// tool_use_id).
type anthropicBlock struct {
	kind     string
	text     strings.Builder
	thinking strings.Builder
	toolID   string
	toolName string
	toolArgs strings.Builder
	result   string
}

func (b *anthropicBlock) setKind(k string) {
	if b.kind == "" {
		b.kind = k
	}
}

// anthropicBlockList keeps response content blocks in index order so the
// emitted parts preserve the wire order — thinking, text, and tool blocks can
// interleave (the model may think, answer, then think again), and collapsing
// them into one aggregate part per kind would lose that ordering.
type anthropicBlockList struct {
	order []int
	byIdx map[int]*anthropicBlock
}

func (l *anthropicBlockList) get(idx int) *anthropicBlock {
	if b, ok := l.byIdx[idx]; ok {
		return b
	}
	b := &anthropicBlock{}
	l.byIdx[idx] = b
	l.order = append(l.order, idx)
	return b
}

// text concatenates every text block in index order — the flat OutputText
// retained for text-only consumers.
func (l *anthropicBlockList) text() string {
	sort.Ints(l.order)
	var s strings.Builder
	for _, idx := range l.order {
		if b := l.byIdx[idx]; b.kind == "text" {
			s.WriteString(b.text.String())
		}
	}
	return s.String()
}

// parts emits one ordered Part per content block: reasoning for thinking /
// redacted_thinking (redacted carries no text), text for text, tool_call for
// tool_use and the server-executed call blocks (server_tool_use,
// mcp_tool_use), tool_call_response for any *_tool_result block
// (web_search_tool_result, code_execution_tool_result, mcp_tool_result and
// whatever the provider adds next — matched by suffix, not by name). Empty
// text blocks are dropped; a thinking block is always emitted so its presence
// (and any content) reaches telemetry. A block of any other kind degrades to
// a bare typed part rather than vanishing, so a future server-side block type
// is at least visible in telemetry.
func (l *anthropicBlockList) parts() []Part {
	sort.Ints(l.order)
	var parts []Part
	for _, idx := range l.order {
		b := l.byIdx[idx]
		switch b.kind {
		case "text":
			if b.text.Len() > 0 {
				parts = append(parts, Part{Type: "text", Content: b.text.String()})
			}
		case "thinking":
			parts = append(parts, Part{Type: "reasoning", Content: b.thinking.String()})
		case "redacted_thinking":
			parts = append(parts, Part{Type: "reasoning"})
		case "tool_use", "server_tool_use", "mcp_tool_use":
			if b.toolID != "" || b.toolName != "" || b.toolArgs.Len() > 0 {
				// tool_use is the client-executed path; server_tool_use /
				// mcp_tool_use are provider-hosted and run inline.
				exec := "client"
				if b.kind != "tool_use" {
					exec = "server"
				}
				parts = append(parts, Part{Type: "tool_call", ID: b.toolID, Name: b.toolName, Arguments: argsRaw(b.toolArgs.String()), Executor: exec})
			}
		default:
			switch {
			case strings.HasSuffix(b.kind, "_tool_result"):
				// A *_tool_result block is always a server-executed tool's
				// inline result (web_search_tool_result, mcp_tool_result, …);
				// the client path returns results as a fresh user-turn
				// tool_result, never here.
				parts = append(parts, Part{Type: "tool_call_response", ID: b.toolID, Result: b.result, Executor: "server"})
			case b.kind != "":
				parts = append(parts, Part{Type: b.kind})
			}
		}
	}
	return parts
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
					// Gemini function calls are always client-executed (no
					// provider-hosted tools in this path).
					toolParts = append(toolParts, Part{
						Type:      "tool_call",
						Name:      p.FunctionCall.Name,
						Arguments: nonEmptyRaw(p.FunctionCall.Args),
						Executor:  "client",
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
