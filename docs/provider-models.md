# Provider model types

The `protocols/` packages model the on-the-wire shapes of the three providers
SlipSpace fronts. Each package mirrors one wire surface of one provider and exists
so the gateway can parse a request, apply rules, and re-marshal it without
dropping fields. Every exported struct embeds `models.DynamicProperties` and
every polymorphic union has an `UnknownX` fallback — with two deliberate
exceptions, both following the same raw-preservation pattern:
`MessageContent` (`protocols/openai/chat/content_parts.go:328-330`) and
`ThinkOption` (`protocols/openai/chat/think.go:25-27`). Each wraps a bare
unexported `raw json.RawMessage` and embeds nothing, because it re-emits the
provider's bytes verbatim and is therefore lossless by construction rather
than by unknown-field capture. The round-tripping
machinery is documented in [models.md](models.md); this page is the catalogue of
the **notable concrete types** per provider.

Treat this page (and the code it cites) as a moving target: `protocols/` chases
a spec the providers change without notice. When a field here drifts from the
struct, the struct is the source of truth — fix the doc.

| Provider | Package | Wire surface |
|---|---|---|
| OpenAI | `protocols/openai/chat` | `POST /v1/chat/completions` |
| OpenAI | `protocols/openai/responses` | `POST /v1/responses` |
| OpenAI | `protocols/openai/models` | `GET /v1/models` |
| Anthropic | `protocols/anthropic/messages` | `POST /v1/messages` |
| Anthropic | `protocols/anthropic/models` | `GET /v1/models` |
| Gemini | `protocols/gemini/content` | `:generateContent` + `:streamGenerateContent` |
| Gemini | `protocols/gemini/models` | `GET /v1beta/models` |

## OpenAI — chat completions (`protocols/openai/chat`)

The request body is `ChatCompletionRequest` (`chat.go:21-116`); the
non-streaming reply is `ChatCompletionResponse` (`chat.go:132-162`); a streaming
reply is a sequence of `ChatCompletionChunk` (`chat.go:178-209`) whose
`ChunkChoice.Delta` fragments the receiver concatenates.

### Messages — the `role`-discriminated union

`ChatCompletionRequest.Messages` is `RequestMessages`, a polymorphic slice keyed
on `role` rather than `type` — chat-completions is the only OpenAI surface where
`role` is the discriminator (`messages.go:7-29`). The registry
(`messages.go:275-286`) maps:

| Role | Type | Notes |
|---|---|---|
| `system` | `SystemMessage` (`messages.go:34`) | Classic system prompt |
| `developer` | `DeveloperMessage` (`messages.go:65`) | OpenAI's replacement for `system` on newer (o-series) models. Both are modelled; the gateway preserves whichever the client sent and does not rewrite one into the other |
| `user` | `UserMessage` (`messages.go:98`) | `Content` is string-or-array (see below) |
| `assistant` | `AssistantMessage` (`messages.go:128`) | Carries `ToolCalls`, `Refusal`, `Audio` for replayed turns |
| `tool` | `ToolMessage` (`messages.go:171`) | `ToolCallID` correlates to a prior call |
| `function` | `FunctionMessage` (`messages.go:202`) | Legacy pre-`tool` shape, retained for back-compat |
| *(any other)* | `UnknownRequestMessage` (`messages.go:234`) | Fallback; preserves the role + all sibling fields |

`UserMessage.Content`, `AssistantMessage.Content`, `ToolMessage.Content`, and
`FunctionMessage.Content` are typed `MessageContent` (`content_parts.go:328-330`),
the string-or-array shape: a chat message's content may be a bare JSON string or
an array of content parts. The raw bytes are retained verbatim, with
`IsString`/`IsArray`/`IsNull` to inspect and `AsString`/`AsParts` to project
(`content_parts.go:363-402`).

### Content parts — the `type`-discriminated union

`MessageContent.AsParts()` dispatches through the content-part registry
(`content_parts.go:294-305`):

| `type` | Type | Purpose |
|---|---|---|
| `text` | `TextContentPart` (`content_parts.go:37`) | Plain text |
| `image_url` | `ImageURLContentPart` (`content_parts.go:86`) | Inline (`data:`) or remote image + detail tier |
| `input_audio` | `InputAudioContentPart` (`content_parts.go:134`) | Base64 audio sent **to** the model |
| `audio` | `OutputAudioContentPart` (`content_parts.go:162`) | Audio the assistant emitted (wraps `AudioMessage`) |
| `refusal` | `RefusalContentPart` (`content_parts.go:190`) | The assistant's refusal text as a content part |
| `file` | `FileContentPart` (`content_parts.go:243`) | A `file_id` reference **or** inlined `file_data` bytes (`File`, `content_parts.go:219`) |
| *(any other)* | `UnknownContentPart` (`content_parts.go:272`) | Fallback |

### Audio, refusals, and request extensions

- **Audio** is end-to-end: the request opts in via `Modalities` (`["text","audio"]`)
  and `Audio *AudioOptions` (voice + format) (`chat.go:92-103`); the assistant's
  audio reply comes back as `ResponseMessage.Audio` (`chat.go:556`), an
  `AudioMessage` carrying an `ID` (replayable in a later turn), base64 `Data`,
  `Transcript`, and `ExpiresAt` (`chat.go:582-595`).
- **Refusals** appear both as a top-level `ResponseMessage.Refusal *string`
  (`chat.go:537`) and, in array content, as a `RefusalContentPart`.
- **Reasoning models** are served by `ReasoningEffort` on the request
  (`chat.go:95-97`) and `MaxCompletionTokens` (`chat.go:33-35`);
  `CompletionTokensDetails.ReasoningTokens` (`chat.go:339-342`) reports the
  hidden-reasoning spend.
- **Stored completions** use `Store *bool` + `Metadata map[string]string`
  (`chat.go:105-110`).

### ToolChoice polymorphism (raw bytes)

`ChatCompletionRequest.ToolChoice` is OpenAI's string-or-object field — either
the string `"auto"`/`"none"`/`"required"` or an object pinning a specific tool.
Rather than model both shapes, it is kept as `json.RawMessage` so the caller
picks the projection and the field round-trips untouched (`chat.go:81-84`). The
same raw-bytes treatment applies to `Stop` (string or array of strings,
`chat.go:53-56`) and the `logprobs` detail on `Choice`/`ChunkChoice`.

> **`ToolCallFunction.Arguments` is a JSON string, not an object.** OpenAI ships
> function-call arguments as a stringified JSON document, so `Arguments` is a Go
> `string`; callers must `json.Unmarshal` it to recover the structured payload
> (`chat.go:759-767`, `Arguments` at `chat.go:766`). In streaming,
> `ToolCallFunctionDelta.Arguments` is emitted *without* `omitempty` because
> OpenAI sends an empty-string delta as the "tool call begins" marker — dropping
> it would lose the start signal (`chat.go:790-797`, `Arguments` at `chat.go:796`;
> rationale godoc at `chat.go:786-789`).

## OpenAI — Responses API (`protocols/openai/responses`)

The `/v1/responses` endpoint is OpenAI's newer, stateful surface; it is modelled
separately from chat completions. `ResponsesRequest` (`responses.go:21-125`) and
`ResponsesResponse` (`responses.go:140-272`) carry a large, fast-moving field
set; the design leans hard on `DynamicProperties` and `json.RawMessage` for the
parts of the spec that change most.

Request highlights:

- **`Input`** is polymorphic (bare string *or* an array of typed input items),
  kept raw (`responses.go:25-28`).
- **`Reasoning *ReasoningOptions`** (`effort` + `summary`) configures reasoning
  models (`responses.go:48`; `ReasoningOptions` at `responses.go:303-313`, `Effort`
  `:306`, `Summary` `:310` — note `responses.go:328` is the separate
  response-side `ReasoningOutput`); `MaxOutputTokens` caps generated
  *and* reasoning tokens (`responses.go:42-44`).
- **Statefulness:** `PreviousResponseID` chains to a prior stored response
  server-side (`responses.go:70-72`); `Store` + `Metadata` persist it
  (`responses.go:60-68`); `Background` runs async and returns a pollable status
  (`responses.go:78-80`).
- **Caching / safety identity:** `PromptCacheKey`, `PromptCacheRetention`, and
  `SafetyIdentifier` are the supported replacements for the deprecated `User`
  field (`responses.go:103-113`), all kept; `User` still round-trips.
- **`Text`** (text / structured-output format) is kept raw because its `format`
  sub-object is polymorphic (`responses.go:119-122`).

Response highlights:

- **`Output []OutputItem`** is the ordered list of items the model emitted.
  `OutputItem` (`responses.go:462-518`) is modelled as a **single struct**, not a
  registry union, because the item shape evolves frequently: `Type` is the
  discriminator and the variant-specific payloads `Content`, `Output`, and
  `Summary` are kept as `json.RawMessage` so callers dispatch on `Type` and
  decode the shape they need; `Arguments` (`responses.go:495`) is a Go `string`
  because OpenAI ships function arguments as a JSON-encoded string, matching the
  chat package's `ToolCallFunction.Arguments` convention. The struct also carries
  `Phase` (`responses.go:487`) and the encrypted reasoning trace
  `EncryptedContent json.RawMessage` (`responses.go:515`). `OutputText` (`responses.go:164`) is
  the convenience concatenated-text projection.
- **`Usage`** uses `input_tokens`/`output_tokens` naming (not chat's
  `prompt`/`completion`), so it is a **distinct local type** from the chat
  package's `Usage` (`responses.go:380-399`); `OutputTokensDetails.ReasoningTokens`
  reports reasoning spend (`OutputTokensDetails` at `responses.go:433-441`,
  `ReasoningTokens` at `responses.go:439`).
- Many echoed scalars (`MaxToolCalls`, `PromptCacheKey`, `SafetyIdentifier`,
  `User`, `Moderation`, `Instructions`, `Tools`, `ToolChoice`) are kept raw
  precisely because OpenAI emits them as `null` when unset and the gateway must
  round-trip the `null` rather than collapse the field.

### Responses streaming events

The streaming variant emits a polymorphic SSE event hierarchy
(`responses/events.go`), dispatched on the event `type`:
`ResponseCreatedEvent`, `ResponseInProgressEvent`, `OutputItemAddedEvent` /
`OutputItemDoneEvent`, `ContentPartAddedEvent` / `ContentPartDoneEvent`,
`OutputTextDeltaEvent` / `OutputTextDoneEvent`, `ResponseCompletedEvent`,
`ResponseFailedEvent`, and the `UnknownEvent` fallback
(`events.go:36-411`).

## Anthropic — messages (`protocols/anthropic/messages`)

`MessagesRequest` (`messages.go:16-79`) and `MessagesResponse`
(`response.go:14-56`). `MaxTokens` is required on every request
(`messages.go:21-23`).

### System and Content: string-or-array accessors

Two fields accept either a bare string or an array and are kept as
`json.RawMessage` with typed accessors rather than a forced projection:

- **`MessagesRequest.System`** — a string or an array of `SystemBlock`. Read with
  `SystemAsString` / `SystemAsBlocks`, write with `SetSystemString` /
  `SetSystemBlocks` (`messages.go:89-133`). A `SystemBlock` (`messages.go:330-342`)
  carries `Text` plus an optional `CacheControl`.
- **`Message.Content`** — a string or an array of `ContentBlock`. Read with
  `ContentAsString` / `ContentAsBlocks`, write with `SetContentString` /
  `SetContentBlocks` (`messages.go:162-206`). `ContentAsBlocks` dispatches through
  the content-block registry, so unknown block types survive.

### Content blocks — the `type`-discriminated union

Registry at `contentblock.go:597-619` (`blockRegistry`, discriminator field
`type`; 11 factories including `server_tool_use`, `web_search_tool_result`,
`web_fetch_tool_result`, `tool_search_tool_result`, `tool_reference`):

| `type` | Type | Notes |
|---|---|---|
| `text` | `TextBlock` (`contentblock.go:43`) | |
| `image` | `ImageBlock` (`contentblock.go:144`) | `ImageSource` is base64 *or* URL (`contentblock.go:116`) |
| `tool_use` | `ToolUseBlock` (`contentblock.go:170`) | `Input` raw; optional `Caller` field at `contentblock.go:188` attributes the call (`ToolCaller` type at `contentblock.go:195`) |
| `tool_result` | `ToolResultBlock` (`contentblock.go:226`) | `Content` is itself string-or-array, kept raw |
| `server_tool_use` | `ServerToolUseBlock` | Server-side tool invocation |
| `web_search_tool_result` | `WebSearchToolResultBlock` | Server web-search result |
| `web_fetch_tool_result` | `WebFetchToolResultBlock` | Server web-fetch result |
| `tool_search_tool_result` | `ToolSearchToolResultBlock` | Tool-search result (PR #470) |
| `tool_reference` | `ToolReferenceBlock` | Tool reference (PR #470) |
| `thinking` | `ThinkingBlock` (`contentblock.go:514`) | See signature echo below |
| `redacted_thinking` | `RedactedThinkingBlock` (`contentblock.go:547`) | Opaque encrypted `Data` |
| *(any other)* | `UnknownBlock` (`contentblock.go:577`) | Fallback |

### Thinking blocks and the signature-echo requirement

Extended thinking is **load-bearing for round-tripping**, not just informational:

- `ThinkingBlock.Signature` (`contentblock.go:524`; invariant documented at
  `contentblock.go:510-513`) is a cryptographic
  attestation over the thinking trace. The client **MUST echo it back verbatim**
  on the assistant turn or tool use cannot resume — so it must round-trip
  exactly. In streaming it arrives as a terminal `signature_delta` after the
  `thinking_delta` fragments (`stream.go`, `SignatureDelta`).
- `RedactedThinkingBlock.Data` (`RedactedThinkingBlock` at `contentblock.go:547`,
  `Data` at `contentblock.go:552`; invariant documented at
  `contentblock.go:542-546`) is thinking the
  provider encrypted after tripping a safety classifier. It carries no
  human-readable content but must likewise be echoed back verbatim alongside any
  sibling thinking blocks.

The request enables thinking via `MessagesRequest.Thinking *ThinkingConfig`
(`messages.go:59`; `ThinkingConfig` type + `budget_tokens` at `messages.go:308-317`).

### Response-side fields: context management, containers, cache tiers

`MessagesResponse` (`response.go:14-56`) and its `Usage` (`response.go:188-240`)
carry several beta / accounting structures:

- **`ContextManagement`** (`response.go:61-69`) reports the context-editing
  operations the server applied under the context-management beta. `AppliedEdits`
  is kept raw so an empty array round-trips intact rather than collapsing.
- **`Container`** (`response.go:358-378`) describes the code-execution sandbox
  (`ID` + `ExpiresAt`) when the request used the code-execution tool; the `ID`
  can be reused across requests to preserve workspace state.
- **`Usage.ServerToolUse`** (`ServerToolUseUsage`, `response.go:242-276`) counts
  server-side tool calls (e.g. web search).
- **Cache accounting** is tiered: `Usage.CacheCreationInputTokens` /
  `CacheReadInputTokens` (`response.go:198-202`) plus `CacheCreation`
  (`response.go:316-336`) which splits writes into the 5-minute and 1-hour
  ephemeral tiers (`Ephemeral5mInputTokens` / `Ephemeral1hInputTokens`). The
  request marks cacheable spans with `CacheControl` (`type: "ephemeral"`,
  `messages.go:427-443`, type at `:430`) on system blocks, tools, and content
  blocks.
- **`Usage.OutputTokensDetails.ThinkingTokens`** (`response.go:340-343`) reports
  output tokens spent inside thinking blocks.
- **`Usage.Iterations`** (`[]IterationUsage`) breaks usage down per server-side
  agent-loop iteration on the beta server tool-use loop (sampling `message`,
  `compaction`, `advisor_message`, `fallback_message`). The wire shape is a
  discriminated union on `type`, but every variant shares one field set, so it
  is modelled as a single flat struct keyed by `Type` rather than a
  concrete-type hierarchy; unknown variant kinds and fields round-trip via
  `DynamicProperties`.

The request also exposes `OutputConfig.Effort` (reasoning-effort hint,
`messages.go:356-369`, `Effort` at `messages.go:359`) and `ServiceTier`
(`messages.go:62`).

### Messages streaming events

The streaming union (`stream.go`, dispatched on event `type`) is:
`MessageStartEvent`, `ContentBlockStartEvent`, `ContentBlockDeltaEvent`,
`ContentBlockStopEvent`, `MessageDeltaEvent`, `MessageStopEvent`, `PingEvent`,
`ErrorEvent`, and `UnknownStreamEvent` (`stream.go:36-394`, `UnknownStreamEvent`
at `stream.go:372`). The `ContentBlockDelta` payload is itself a union:
`TextDelta`, `InputJSONDelta`, `ThinkingDelta`, `SignatureDelta`,
`CitationsDelta`, `UnknownContentBlockDelta` (`stream.go:431-598`,
`CitationsDelta` (type `citations_delta`) at `stream.go:607`,
`UnknownContentBlockDelta` at `stream.go:578`).

## Gemini — generateContent (`protocols/gemini/content`)

`GenerateContentRequest` (`content.go:14-41`) and `GenerateContentResponse`
(`response.go:15-40`). Gemini has **no separate streaming event hierarchy in
v1.0** — `streamGenerateContent` emits a sequence of values with the same
`GenerateContentResponse` shape, one per chunk, so the one type serves both
(`response.go:9-14`).

### Parts — key-presence polymorphism

Gemini distinguishes content parts by *which top-level key is present*
(`{"text":…}`, `{"inlineData":…}`, `{"functionCall":…}`) rather than by a
discriminator field. `PolymorphicRegistry` assumes a single discriminator, so
Gemini parts use a hand-rolled `UnmarshalPart` (`parts.go:409`) that inspects
the key set against `partFactories` (`parts.go:395-403`):

| Key | Type | Notes |
|---|---|---|
| `text` | `TextPart` (`parts.go:58`) | Carries `Thought` + `ThoughtSignature` (see below) |
| `inlineData` | `InlineDataPart` (`parts.go:110`) | `Blob` = mimeType + base64 |
| `fileData` | `FileDataPart` (`parts.go:154`) | URI reference to an uploaded file |
| `functionCall` | `FunctionCallPart` (`parts.go:202`) | Carries `ThoughtSignature` |
| `functionResponse` | `FunctionResponsePart` (`parts.go:256`) | Client's tool result |
| `executableCode` | `ExecutableCodePart` (`parts.go:301`) | Code the model wants run |
| `codeExecutionResult` | `CodeExecutionResultPart` (`parts.go:349`) | Outcome + output of a run |
| *(no known key)* | `UnknownPart` (`parts.go:375`) | Fallback; whole object kept in `Extra` |

An object with **no keys at all** returns `ErrEmptyPart` rather than producing an
empty `UnknownPart` (`parts.go:25-29`, `:415`).

### Thinking, thought signatures, and code execution

- **`TextPart.Thought *bool`** marks a part as a thinking-trace fragment (emitted
  only when `ThinkingConfig.IncludeThoughts` is true), and
  **`TextPart.ThoughtSignature`** is the signature attached to it
  (`parts.go:62-68`).
- **`FunctionCallPart.ThoughtSignature`** (`parts.go:206-211`) is load-bearing,
  same as Anthropic's thinking signature: Gemini 2.5 attaches it to a
  function-call produced after a thinking step, and the client must echo it back
  on the next turn so the model can resume the interrupted thought. Dropping it
  breaks multi-turn thinking with tools.
- **`ExecutableCode`** (`parts.go:280-288`, language + code) and
  **`CodeExecutionResult`** (`parts.go:325-333`, outcome + output) model the
  hosted code-execution tool round-trip.

### GenerationConfig and ThinkingConfig

`GenerationConfig` (`content.go:110-167`) is the sampling/output/reasoning knob
bag: `Temperature`/`TopP`/`TopK`, `MaxOutputTokens`, `StopSequences`,
`ResponseMimeType` + `ResponseSchema` (structured output),
`ResponseModalities`, `Seed`, `MediaResolution`, penalties, logprobs, and
`ThinkingConfig` (`content.go:179-190`, `IncludeThoughts` + `ThinkingBudget`).
Tools are advertised on the request via `Tool` (`content.go:205-225`): function
declarations plus the built-in marker tools `GoogleSearch`,
`GoogleSearchRetrieval`, `CodeExecution`, and `URLContext`.

> **Two function-schema encodings.** `FunctionDeclaration` (`content.go:312-341`)
> carries both `Parameters` (the Gemini Schema / OpenAPI-3.0-subset form) and
> `ParametersJsonSchema` (full JSON Schema draft 2020-12, which gemini-cli and
> recent SDKs emit), plus their `Response` counterparts. They are mutually
> exclusive upstream; both are kept raw so whichever the client sent
> round-trips.

### Response metadata: grounding, citations, safety, usage

`GenerateContentResponse.Candidates` (`Candidate`, `response.go:54-97`) carry the
rich response metadata:

- **`GroundingMetadata`** (`response.go:110-130`) is the Google Search /
  web-grounding evidence: `SearchEntryPoint` (a rendered widget the caller must
  display, `response.go:144-149`), `GroundingChunks` (retrieved web sources,
  `response.go:161-184`), `GroundingSupports` (spans of the answer tied to chunks
  with confidence scores, `response.go:194-206`), and `WebSearchQueries`.
- **`CitationMetadata`** (`response.go:277-282`) lists `CitationSource` entries
  (`response.go:296-312`) locating cited spans by start/end index with URI +
  license.
- **Safety** is reported per category: `SafetyRating` (`response.go:241-264`,
  bucketed + numeric probability/severity, `Blocked`) appears both on each
  `Candidate` and on `PromptFeedback` (`response.go:325-335`, which also carries
  a `BlockReason` if the prompt itself was rejected).
- **`UsageMetadata`** (`response.go:378-424`, type at `:380`) — carried on
  `GenerateContentResponse` (`response.go:25`), not on `Candidate` — accounts
  tokens with per-modality breakdowns (`ModalityTokenCount`, `response.go:437`):
  prompt, candidates, cached-content, tool-use, and `ThoughtsTokenCount`
  (`response.go:406`) for the thinking trace.
- **`ModelStatus *ModelStatus`** (`response.go:37`) reports the lifecycle stage of
  the model that served the response, plus its retirement time when scheduled.

## Models-list surfaces

The three `*/models` packages are flat, non-polymorphic lists, each embedding
`DynamicProperties` so new descriptor fields round-trip:

- **OpenAI** `GET /v1/models` — `ListModelsResponse` + `Model`
  (`protocols/openai/models/models.go:15-52`); `object: "list"`, flat `data`.
- **Anthropic** `GET /v1/models` — `ListModelsResponse` + `Model`
  (`protocols/anthropic/models/models.go:16-61`); cursor-paged via
  `HasMore`/`FirstID`/`LastID`.
- **Gemini** `GET /v1beta/models` — `ListModelsResponse` + `Model`
  (`protocols/gemini/models/models.go:16-82`); page-token paged, rich
  per-model capability fields (`SupportedGenerationMethods`, token limits,
  default sampling params).

## See also

- [models.md](models.md) — `DynamicProperties`, `PolymorphicRegistry`, and
  `CollectUnmapped`, the primitives every type here is built on.
- [genai-conformance-audit.md](genai-conformance-audit.md) — how these typed
  models map onto OTel GenAI semantic conventions.
- [routing.md](routing.md) — how an inbound request is matched to a protocol and
  thus to the right package above.
</content>
